package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/big"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"phoenix-v3/bot"
	"phoenix-v3/internal/api"
	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/dexstate"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/events"
	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/monitor"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/rebalancer"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

var abortFlag atomic.Bool
var pauseFlag atomic.Bool
var cleanupFlag atomic.Bool

type pauseController struct{}

func (p *pauseController) SetPaused(v bool) {
	pauseFlag.Store(v)
}

func (p *pauseController) Paused() bool {
	return pauseFlag.Load()
}

type cleanupController struct {
	ctx      context.Context
	gateways map[int64]*gateway.EthGateway
	cfgValue *atomic.Value
	store    *storage.Store
}

type rebalanceController struct {
	queue    *strategy.IntentQueue
	cfgValue *atomic.Value
}

func (r *rebalanceController) TriggerRebalance(poolID string) error {
	cfg := r.cfgValue.Load().(*config.AppConfig)
	if cfg == nil || len(cfg.Pools) == 0 {
		return fmt.Errorf("no pools configured")
	}
	if poolID == "" {
		poolID = cfg.Pools[0].ID
	}
	poolCfg, ok := findPoolConfig(cfg, poolID)
	if !ok {
		return fmt.Errorf("unknown pool %s", poolID)
	}

	// Get current pool runtime to calculate ticks
	runtimeState, hasRuntime := bot.GetPoolRuntimeSnapshot(poolID)
	if !hasRuntime || runtimeState == nil {
		return fmt.Errorf("pool runtime not available for %s", poolID)
	}
	if runtimeState.DexPrice == 0 {
		return fmt.Errorf("dex price not available for %s", poolID)
	}

	// Calculate ticks based on current DEX price with a reasonable spread
	spreadPct := 0.05 // 5% spread
	lowerPrice := runtimeState.DexPrice * (1 - spreadPct)
	upperPrice := runtimeState.DexPrice * (1 + spreadPct)

	// IMPORTANT: PriceToTickWithDecimals expects price = Token1/Token0 (WETH/TUSD)
	// But dexPrice is Token0/Token1 (TUSD/WETH), so we need to invert it
	lowerPriceInv := 1.0 / lowerPrice
	upperPriceInv := 1.0 / upperPrice

	lowerTick := engine.PriceToTickWithDecimals(lowerPriceInv, poolCfg.Token0Decimals, poolCfg.Token1Decimals)
	upperTick := engine.PriceToTickWithDecimals(upperPriceInv, poolCfg.Token0Decimals, poolCfg.Token1Decimals)

	// Align to tick spacing
	spacing := strategy.TickSpacingForFee(poolCfg.Fee)
	if spacing <= 0 {
		spacing = 10
	}
	lowerTick = (lowerTick / spacing) * spacing
	upperTick = (upperTick / spacing) * spacing
	if lowerTick >= upperTick {
		upperTick = lowerTick + spacing
	}

	intent := strategy.Intent{
		ID:                fmt.Sprintf("MANUAL_REBALANCE_%s_%d", poolID, time.Now().Unix()),
		Type:              strategy.IntentRebalance,
		PoolID:            poolID,
		ChainID:           poolCfg.ChainID,
		TargetNotionalPct: effectiveMaxCapPct(poolCfg.MaxCapPct, cfg.Risk.MaxUtilizationPct),
		StrategyVersion:   cfg.StrategyVersion,
		RiskMode:          string(risk.ModeNormal),
		Metadata: map[string]string{
			"token0":          poolCfg.Token0,
			"token1":          poolCfg.Token1,
			"fee":             strconv.Itoa(poolCfg.Fee),
			"lower_tick":      strconv.FormatInt(lowerTick, 10),
			"upper_tick":      strconv.FormatInt(upperTick, 10),
			"token0_decimals": strconv.Itoa(poolCfg.Token0Decimals),
			"token1_decimals": strconv.Itoa(poolCfg.Token1Decimals),
		},
	}
	r.queue.Enqueue(intent)
	return nil
}

func (c *cleanupController) InProgress() bool {
	return cleanupFlag.Load()
}

func (c *cleanupController) TriggerCleanup() error {
	if !cleanupFlag.CompareAndSwap(false, true) {
		return fmt.Errorf("cleanup already running")
	}
	go func() {
		defer cleanupFlag.Store(false)
		cfg := c.cfgValue.Load().(*config.AppConfig)
		runCleanup(c.ctx, c.gateways, cfg, c.store)
	}()
	return nil
}

type pricePoint struct {
	t     time.Time
	price float64
}

type volatilityEstimator struct {
	mu     sync.Mutex
	window time.Duration
	points []pricePoint
}

func newVolatilityEstimator(window time.Duration) *volatilityEstimator {
	if window <= 0 {
		window = 6 * time.Hour
	}
	return &volatilityEstimator{window: window}
}

func (v *volatilityEstimator) SetWindow(window time.Duration) {
	if v == nil || window <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.window = window
}

func (v *volatilityEstimator) Add(price float64, t time.Time) {
	if v == nil || price <= 0 {
		return
	}
	if t.IsZero() {
		t = time.Now()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.points = append(v.points, pricePoint{t: t, price: price})
	cutoff := t.Add(-v.window)
	keep := 0
	for _, p := range v.points {
		if p.t.After(cutoff) {
			break
		}
		keep++
	}
	if keep > 0 && keep < len(v.points) {
		v.points = append([]pricePoint(nil), v.points[keep:]...)
	}
}

// SigmaDaily estimates daily realized volatility from log-returns in the window.
func (v *volatilityEstimator) SigmaDaily() float64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	points := append([]pricePoint(nil), v.points...)
	v.mu.Unlock()

	if len(points) < 3 {
		return 0
	}
	// Build log returns.
	rets := make([]float64, 0, len(points)-1)
	var totalDt float64
	for i := 1; i < len(points); i++ {
		p0 := points[i-1].price
		p1 := points[i].price
		if p0 <= 0 || p1 <= 0 {
			continue
		}
		dt := points[i].t.Sub(points[i-1].t).Seconds()
		if dt <= 0 {
			continue
		}
		r := math.Log(p1 / p0)
		rets = append(rets, r)
		totalDt += dt
	}
	if len(rets) < 2 || totalDt <= 0 {
		return 0
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	varSum := 0.0
	for _, r := range rets {
		d := r - mean
		varSum += d * d
	}
	// Sample variance.
	if len(rets) < 2 {
		return 0
	}
	sigmaSample := math.Sqrt(varSum / float64(len(rets)-1))
	avgInterval := totalDt / float64(len(rets))
	if avgInterval <= 0 {
		return 0
	}
	samplesPerDay := 86400.0 / avgInterval
	if samplesPerDay <= 0 {
		return 0
	}
	return sigmaSample * math.Sqrt(samplesPerDay)
}

var rebalanceLimiter bot.PerPoolDailyLimiter
var lastRebalanceAt bot.LastActionTracker

func setLastRebalanceAt(poolID string, t time.Time) {
	lastRebalanceAt.Set(poolID, t)
}

func getLastRebalanceAt(poolID string) (time.Time, bool) {
	return lastRebalanceAt.Get(poolID)
}

func boolPtr(v bool) *bool { return &v }

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 0. Parse Flags
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	cleanupMode := flag.Bool("cleanup", false, "Cleanup Mode")
	dryRunOverride := flag.Bool("dry-run", false, "Force dry-run mode (no on-chain tx)")
	offlineMode := flag.Bool("offline", false, "Offline mode (no network): simulate ticker/pool_state")
	offlineFeedOnly := flag.Bool("offline-feed", false, "Offline feed only: simulate ticker but keep on-chain DEX watchers/tx")
	offlineFeedStepStd := flag.Float64("offline-feed-step-std", 0.0015, "Offline feed step stdev (e.g. 0.0015 == 0.15%)")
	offlineFeedJumpProb := flag.Float64("offline-feed-jump-prob", 0.05, "Offline feed jump probability per tick (0..1)")
	offlineFeedJumpMin := flag.Float64("offline-feed-jump-min", 0.003, "Offline feed jump min pct (e.g. 0.003 == 0.3%)")
	offlineFeedJumpMax := flag.Float64("offline-feed-jump-max", 0.015, "Offline feed jump max pct (e.g. 0.015 == 1.5%)")
	manualOnly := flag.Bool("manual-only", false, "Manual-only mode: disable automatic strategy evaluation loop (control-plane intents still execute)")
	disableAPI := flag.Bool("no-api", false, "Disable API server (no ListenAndServe)")
	disableMonitor := flag.Bool("no-monitor", false, "Disable monitor server (no ListenAndServe)")
	dbDefault := strings.TrimSpace(os.Getenv("PHOENIX_DB_PATH"))
	if dbDefault == "" {
		dbDefault = "phoenix.db"
	}
	dbPath := flag.String("db-path", dbDefault, "path to sqlite db file (PHOENIX_DB_PATH)")
	flag.Parse()

	// 1. Load Configuration & watch for hot reload
	cfgManager, err := config.NewManager(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.Current()
	if *dryRunOverride {
		cfg.Strategy.DryRun = boolPtr(true)
		log.Println("[Bot] dry-run override enabled")
	}
	safety := config.SafetyFromConfig(cfg)
	dryRun := safety.EffectiveDryRun
	log.Printf("Phoenix V3 Config Loaded. Chains: %d, Pools: %d", len(cfg.Chains), len(cfg.Pools))
	log.Printf("[Safety] dry_run=%v kill_switch=%v allow_tx_broadcast=%v effective_dry_run=%v", safety.DryRun, safety.KillSwitch, safety.AllowTxBroadcast, safety.EffectiveDryRun)
	bot.SyncPoolStatesFromConfig(cfg)
	bot.SyncMintGuardsFromConfig(cfg)

	var cfgValue atomic.Value
	cfgValue.Store(cfg)
	var apiServer *api.Server
	var tokenPriceStore atomic.Value
	tokenPriceStore.Store(map[string]float64{})

	volWindow, err := time.ParseDuration(cfg.Strategy.Range.VolWindow)
	if err != nil {
		volWindow = 6 * time.Hour
	}
	volEstimator := newVolatilityEstimator(volWindow)

	// 2. Initialize Monitor
	monitorService := monitor.NewMonitor(cfg.Monitoring)
	if !*disableMonitor {
		go monitorService.Start()
	} else {
		log.Printf("[Monitor] disabled")
	}

	// Create price cache to store real prices from Binance
	var currentPrice float64 = 2005.0 // Default fallback price
	var isBinanceConnected bool = false
	eventStream := initEventStream(cfg)

	priceAggregator := feed.NewAggregator()
	defer priceAggregator.Close()
	go func() {
		for t := range priceAggregator.Output() {
			_ = eventStream.Publish(ctx, events.TopicTicker, t)
		}
	}()

	// 3. Start CEX Feed (Binance)
	if *offlineMode || *offlineFeedOnly {
		ch := make(chan feed.Ticker, 16)
		priceAggregator.AddSource("offline", ch)
		go func() {
			// Stress-oriented synthetic feed: fast cadence + random walk + occasional jumps.
			// This is useful on testnet to quickly validate rebalance responsiveness without relying on CEX connectivity.
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					close(ch)
					return
				case now := <-t.C:
					// Random walk step.
					stepStd := *offlineFeedStepStd
					if stepStd <= 0 {
						stepStd = 0.0015
					}
					step := rng.NormFloat64() * stepStd
					currentPrice *= (1.0 + step)
					// Occasional jump (simulate "CEX price突变").
					jumpProb := *offlineFeedJumpProb
					if jumpProb < 0 {
						jumpProb = 0
					}
					if jumpProb > 1 {
						jumpProb = 1
					}
					jMin := *offlineFeedJumpMin
					jMax := *offlineFeedJumpMax
					if jMin < 0 {
						jMin = 0
					}
					if jMax < jMin {
						jMax = jMin
					}
					if rng.Float64() < jumpProb && jMax > 0 {
						jump := jMin
						if jMax > jMin {
							jump = jMin + rng.Float64()*(jMax-jMin)
						}
						if rng.Intn(2) == 0 {
							jump = -jump
						}
						currentPrice *= (1.0 + jump)
					}
					if currentPrice < 100 {
						currentPrice = 100
					}
					ch <- feed.Ticker{Symbol: "ETHUSDT", Price: currentPrice, Timestamp: now}
				}
			}
		}()
		monitorService.UpdateFeedMetric(monitor.FeedMetric{Source: "offline", Healthy: true, DelayMs: 0, LastUpdateAt: time.Now()})
	} else {
		binanceFeed := feed.NewBinanceFeed()
		binanceFeed.OnStatusUpdate(func(status feed.FeedStatus) {
			isBinanceConnected = status.Healthy
			monitorService.UpdateFeedMetric(monitor.FeedMetric{
				Source:       status.Source,
				Healthy:      status.Healthy,
				DelayMs:      status.DelayMs,
				LastUpdateAt: status.LastUpdateAt,
			})
			if apiServer != nil {
				apiServer.UpdateFeedStatus(status)
			}
		})
		// Try to subscribe to Binance, use fallback if fails
		tickerResult, err := binanceFeed.SubscribeTicker("ETHUSDT")
		if err != nil {
			log.Printf("⚠️ Failed to subscribe to Binance: %v", err)
		} else {
			priceAggregator.AddSource("binance", tickerResult)
		}
		coingeckoFeed := feed.NewCoinGeckoFeed(5 * time.Second)
		coingeckoFeed.OnStatusUpdate(func(status feed.FeedStatus) {
			monitorService.UpdateFeedMetric(monitor.FeedMetric{
				Source:       status.Source,
				Healthy:      status.Healthy,
				DelayMs:      status.DelayMs,
				LastUpdateAt: status.LastUpdateAt,
			})
			if apiServer != nil {
				apiServer.UpdateFeedStatus(status)
			}
		})
		if cgChan, err := coingeckoFeed.SubscribeTicker("ETHUSDT"); err != nil {
			log.Printf("⚠️ Failed to subscribe CoinGecko: %v", err)
		} else {
			priceAggregator.AddSource("coingecko", cgChan)
		}
	}

	priceEvents, cancelPriceSub, _ := eventStream.Subscribe(events.TopicTicker)
	defer cancelPriceSub()
	poolEvents, cancelPoolSub, _ := eventStream.Subscribe(events.TopicPoolState)
	defer cancelPoolSub()

	// 4. Start DEX State (RPC)
	chainStates := map[int64]*dexstate.UniV3State{}
	poolWatchers := bot.NewPoolWatchers()
	if *offlineMode {
		log.Printf("[DEX] offline mode: simulate pool_state")
		for _, pool := range cfg.Pools {
			p := pool
			go func() {
				t := time.NewTicker(3 * time.Second)
				defer t.Stop()
				baseTick := int64(200000)
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						baseTick += 10
						sqrt := rebalancer.TickToSqrtPriceX96(baseTick)
						payload := eventsPoolState{
							PoolID:       p.ID,
							ChainID:      p.ChainID,
							PoolAddress:  p.Address,
							CurrentTick:  baseTick,
							Liquidity:    "1000000000000000",
							SqrtPriceX96: sqrt.String(),
						}
						_ = eventStream.Publish(ctx, events.TopicPoolState, payload)
					}
				}
			}()
		}
	} else {
		chainStates = bot.InitDexStates(cfg.Chains)
		poolWatchers.Restart(ctx, chainStates, cfg, eventStream)
	}

	// 6. Initialize Strategy & Queue
	strategyMu := sync.RWMutex{}
	policyEngine := strategy.NewPolicyEngine(cfg.Strategy.Profiles)
	strategyMap := buildStrategyMapWithPolicy(cfg, policyEngine)
	intentQueue := strategy.NewIntentQueue()
	rebal := rebalancer.NewRebalancer()

	// 7. Initialize Storage (Phase 5)
	store, err := storage.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to init db: %v", err)
	}

	// 8. Initialize Gateways (per chain)
	privKey, err := loadBotPrivateKey()
	if err != nil {
		log.Fatal(err)
	}
	if privKey == "" && !dryRun {
		log.Fatal("BOT_PRIVATE_KEY not set")
	}
	chainGateways := make(map[int64]*gateway.EthGateway)
	if privKey == "" {
		log.Printf("[Gateway] BOT_PRIVATE_KEY not set; running without gateways (dry-run only)")
	} else {
		for _, ch := range cfg.Chains {
			gw, err := gateway.NewEthGateway(
				ch.RPC,
				privKey,
				gateway.WithGasMultiplier(cfg.Gateway.GasMultiplier),
				gateway.WithRetry(cfg.Gateway.MaxRetries, cfg.Gateway.RetryBackoffMs, cfg.Gateway.GasBumpPct),
				gateway.WithApprovalMultiplier(cfg.Gateway.ApprovalMultiplier),
				gateway.WithPreflight(cfg.Gateway.Preflight == nil || *cfg.Gateway.Preflight),
			)
			if err != nil {
				log.Printf("[Gateway] Failed to init chain %s (%d): %v", ch.Name, ch.ID, err)
				continue
			}
			chainGateways[ch.ID] = gw
		}
		if len(chainGateways) == 0 {
			log.Fatal("[Gateway] No usable gateway")
		}
	}
	selectGateway := func(chainID int64) gateway.Gateway {
		if gw, ok := chainGateways[chainID]; ok {
			return gw
		}
		for _, gw := range chainGateways {
			return gw
		}
		if dryRun {
			return gateway.NewDryRunGateway(chainID)
		}
		return nil
	}

	// Periodically sync on-chain positions into pool runtimes (prevents duplicate LP creation).
	go bot.StartPositionSync(ctx, &cfgValue, store, selectGateway)

	if *cleanupMode {
		if len(chainGateways) == 0 {
			log.Fatal("[Cleanup] no gateway available (missing BOT_PRIVATE_KEY?)")
		}
		runCleanup(ctx, chainGateways, cfg, store)
		return
	}

	// 9. Initialize Risk & PoolGuard (Phase 6)
	riskMgr := risk.NewManager(cfg.Risk.MaxDailyGas, cfg.Risk.ConsecutiveFails, cfg.Risk.MaxDrawdown)
	guard := poolguard.NewGuardWithConfig(cfg.PoolGuard)
	for _, gw := range chainGateways {
		if gw != nil {
			guard.SetChainCaller(gw.ChainID().Int64(), gw)
		}
	}

	// 10. Initialize API Server
	apiServer = api.NewServerWithConfig(intentQueue, store, riskMgr, guard, snapshotPoolsForAPI, api.ServerConfig{
		BinanceConnected: isBinanceConnected,
		PriceSource:      map[bool]string{true: "Binance", false: "Fallback"}[isBinanceConnected],
	})

	// Initialize API with current price
	apiServer.UpdateCEXPrice(feed.Ticker{
		Symbol:    "ETHUSDT",
		Price:     currentPrice,
		Timestamp: time.Now(),
	})

	if !*disableAPI {
		apiServer.Start("8081")
	} else {
		log.Printf("[API] disabled")
	}
	apiServer.SetManualOnly(*manualOnly)
	// Attach PnL store if supported
	apiServer.AttachPnLStore(store)
	apiServer.AttachPauseController(&pauseController{})
	apiServer.AttachCleanupController(&cleanupController{ctx: ctx, gateways: chainGateways, cfgValue: &cfgValue, store: store})
	apiServer.AttachRebalanceController(&rebalanceController{queue: intentQueue, cfgValue: &cfgValue})
	cfgProvider := func() *config.AppConfig {
		cfg, _ := cfgValue.Load().(*config.AppConfig)
		return cfg
	}
	apiServer.AttachConfigProvider(cfgProvider)
	apiServer.AttachEventStream(eventStream)
	apiServer.AttachRebalancer(rebal)
	apiServer.AttachBalanceProvider(api.NewDefaultBalanceProvider(selectGateway, cfgProvider, *offlineMode))
	apiServer.AttachPoolStateProvider(func(poolID string) (api.PoolStateSnapshot, bool) {
		rt, ok := bot.GetPoolRuntimeSnapshot(poolID)
		if !ok || rt == nil {
			return api.PoolStateSnapshot{}, false
		}
		liqStr := "0"
		if rt.PoolLiquidity != nil {
			liqStr = rt.PoolLiquidity.String()
		}
		sqrtStr := ""
		if rt.SqrtPriceX96 != nil {
			sqrtStr = rt.SqrtPriceX96.String()
		}
		priceToken := rt.Cfg.CEXPriceToken
		if priceToken == "" {
			priceToken = rt.Cfg.Token1
		}
		cex := lookupTokenPrice(&tokenPriceStore, priceToken)
		if cex <= 0 {
			cex = currentPrice
		}
		posLiq := ""
		if rt.Position.Liquidity > 0 {
			posLiq = fmt.Sprintf("%.0f", rt.Position.Liquidity)
		}
		return api.PoolStateSnapshot{
			PoolID:          rt.Cfg.ID,
			ChainID:         rt.Cfg.ChainID,
			PoolAddress:     rt.Cfg.Address,
			Token0:          rt.Cfg.Token0,
			Token1:          rt.Cfg.Token1,
			Token0Decimals:  rt.Cfg.Token0Decimals,
			Token1Decimals:  rt.Cfg.Token1Decimals,
			Fee:             rt.Cfg.Fee,
			PositionTokenID: rt.PositionTokenID,
			PosTickLower:    rt.Position.LowerTick,
			PosTickUpper:    rt.Position.UpperTick,
			PosLiquidity:    posLiq,
			DexTick:         rt.CurrentTick,
			DexPrice:        rt.DexPrice,
			SqrtPriceX96:    sqrtStr,
			PoolLiquidity:   liqStr,
			CexPrice:        cex,
			SigmaDaily:      rt.LastSigmaDaily,
			WidthPct:        rt.LastWidthPct,
			VolWindow:       rt.LastVolWindow,
			Profile:         rt.LastProfile,
		}, true
	})

	monitorService.SetStatusProvider(func() map[string]interface{} {
		return map[string]interface{}{
			"intents": map[string]int{"pending": intentQueue.Len()},
			"risk":    riskMgr.Snapshot(),
			"pools":   snapshotPoolsForAPI(),
		}
	})
	monitorService.SetMetricsProvider(func() string {
		snap := riskMgr.Snapshot()
		return fmt.Sprintf("phoenix_risk_daily_gas_used %f\nphoenix_risk_consecutive_fails %d\nphoenix_intent_pending %d\n", snap.DailyGasUsed, snap.ConsecutiveFails, intentQueue.Len())
	})

	log.Println("Phoenix V3 Bot Started (Phase 6: Secured).")

	// 11. Adapters & Routers
	var adapter *univ3.Adapter
	if len(cfg.Pools) > 0 && cfg.Pools[0].PositionManager != "" {
		adapter = univ3.NewAdapter(cfg.Pools[0].PositionManager)
	}
	// Swap execution:
	// - On many testnets (e.g. Sepolia), the canonical mainnet SwapRouter address has no code.
	// - We support a per-chain SwapHelper (contracts/SwapHelper.sol) that directly calls pool.swap.
	swapHelperByChain := map[int64]*univ3.SwapHelper{}
	for _, ch := range cfg.Chains {
		if strings.TrimSpace(ch.SwapHelperAddress) == "" {
			continue
		}
		h, err := univ3.NewSwapHelper(ch.SwapHelperAddress)
		if err != nil {
			log.Printf("[SwapHelper] init failed for chain %d: %v", ch.ID, err)
			continue
		}
		swapHelperByChain[ch.ID] = h
		log.Printf("[SwapHelper] enabled chain=%d addr=%s", ch.ID, h.Address.Hex())
	}
	// Router is still used for ABI packing on chains that have it and for local minOut estimates.
	router := univ3.NewRouter("0xE592427A0AEce92De3Edee1F18E0157C05861564")

	// Optional Quoter per chain (for MinAmountOut)
	quoterByChain := map[int64]*univ3.Quoter{}
	for _, ch := range cfg.Chains {
		if ch.QuoterAddress == "" {
			continue
		}
		q, err := univ3.NewQuoter(ch.QuoterAddress)
		if err != nil {
			log.Printf("[Quoter] init failed for chain %d: %v", ch.ID, err)
			continue
		}
		quoterByChain[ch.ID] = q
	}

	priceProvider := func(token string) float64 {
		return lookupTokenPrice(&tokenPriceStore, token)
	}

	intentExecutor, err := bot.NewIntentExecutor(bot.IntentExecutorDeps{
		ConfigValue:       &cfgValue,
		Queue:             intentQueue,
		Risk:              riskMgr,
		Guard:             guard,
		SelectGateway:     selectGateway,
		Store:             store,
		Stream:            eventStream,
		Adapter:           adapter,
		Router:            router,
		SwapHelperByChain: swapHelperByChain,
		Rebalancer:        rebal,
		PriceProvider:     priceProvider,
		QuoterByChain:     quoterByChain,

		FindPoolConfig:        findPoolConfig,
		EffectiveMaxCapPct:    effectiveMaxCapPct,
		ExecuteSwap:           executeSwap,
		WaitForReceipt:        waitForReceipt,
		HasSufficientBalances: hasSufficientBalances,
		ParseMetadataFloat:    parseMetadataFloat,
		FloatFromBigInt:       floatFromBigInt,
		SetLastRebalanceAt:    setLastRebalanceAt,
		RebalanceLimiter:      &rebalanceLimiter,
	})
	if err != nil {
		log.Fatalf("[IntentExecutor] init failed: %v", err)
	}
	intentExecutor.Start(ctx)
	for _, gw := range chainGateways {
		if gw == nil {
			continue
		}
		go startReceiptWatcher(ctx, gw.Receipts(), store, riskMgr)
	}

	// Periodically recompute drawdown from realized PnL.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if store == nil {
					continue
				}
				trades, err := store.GetRecentPnLTrades(30)
				if err != nil {
					log.Printf("[Risk] drawdown fetch trades failed: %v", err)
					continue
				}
				riskMgr.UpdateDrawdownFromTrades(trades)
			}
		}
	}()

	go func() {
		for updated := range cfgManager.Updates() {
			if updated == nil {
				continue
			}
			if *dryRunOverride {
				updated.Strategy.DryRun = boolPtr(true)
			}
			cfgValue.Store(updated)
			if w, err := time.ParseDuration(updated.Strategy.Range.VolWindow); err == nil {
				volEstimator.SetWindow(w)
			}
			bot.SyncPoolStatesFromConfig(updated)
			bot.SyncMintGuardsFromConfig(updated)
			chainStates = bot.InitDexStates(updated.Chains)
			poolWatchers.Restart(ctx, chainStates, updated, eventStream)
			riskMgr.UpdateLimits(updated.Risk.MaxDailyGas, updated.Risk.ConsecutiveFails, updated.Risk.MaxDrawdown)
			// Also update swap limits
			// riskMgr.UpdateSwapLimits(updated.Risk.MaxSwapVol, updated.Risk.MaxSwapCount) // If added to config generic
			strategyMu.Lock()
			policyEngine = strategy.NewPolicyEngine(updated.Strategy.Profiles)
			strategyMap = buildStrategyMapWithPolicy(updated, policyEngine)
			strategyMu.Unlock()
		}
	}()

	queryTicker := time.NewTicker(5 * time.Second)
	defer queryTicker.Stop()

	lastStrategyLog := map[string]time.Time{}
	lastCooldownLog := map[string]time.Time{}

	for {
		select {
		case evt, ok := <-priceEvents:
			if !ok {
				log.Println("price event stream closed")
				return
			}
			ticker, ok := decodeTicker(evt.Payload)
			if !ok {
				continue
			}
			apiServer.UpdateCEXPrice(ticker)
			currentPrice = ticker.Price
			log.Printf("📊 Price updated: $%.2f", currentPrice)
			volEstimator.Add(currentPrice, ticker.Timestamp)
			updateTokenPrices(cfgValue.Load().(*config.AppConfig), &tokenPriceStore, ticker)

		case evt, ok := <-poolEvents:
			if !ok {
				continue
			}
			state, ok := decodePoolState(evt.Payload)
			if !ok {
				continue
			}
			if dexPrice, ok := bot.UpdatePoolStateFromEvent(state.PoolID, state.CurrentTick, state.Liquidity, state.SqrtPriceX96); ok {
				log.Printf("[DEX] Pool %s tick=%d liquidity=%s dexPrice=%.2f", state.PoolID, state.CurrentTick, state.Liquidity, dexPrice)
			} else {
				log.Printf("[DEX] Received state for unknown pool %s", state.PoolID)
			}

		case <-queryTicker.C:
			if *manualOnly {
				continue
			}
			if pauseFlag.Load() {
				log.Printf("[Control] paused, skipping strategy evaluation")
				continue
			}
			for _, runtime := range bot.SnapshotPoolRuntimes() {
				if guard := bot.GetMintGuard(runtime.Cfg.ID); guard.Load() {
					log.Printf("[PositionGuard] Mint in progress for pool %s, skipping strategy evaluation", runtime.Cfg.ID)
					continue
				}

				if runtime.DexPrice == 0 {
					log.Printf("[Strategy] Pool %s pending dex price, skip", runtime.Cfg.ID)
					continue
				}

				currentCfg := cfgValue.Load().(*config.AppConfig)
				if currentCfg != nil {
					minIntervalStr := strings.TrimSpace(currentCfg.Strategy.Rebalance.MinInterval)
					if minIntervalStr != "" {
						if minInterval, err := time.ParseDuration(minIntervalStr); err == nil && minInterval > 0 {
							if lastAt, ok := getLastRebalanceAt(runtime.Cfg.ID); ok && time.Since(lastAt) < minInterval {
								if last, ok := lastCooldownLog[runtime.Cfg.ID]; !ok || time.Since(last) >= 30*time.Second {
									lastCooldownLog[runtime.Cfg.ID] = time.Now()
									log.Printf("[Strategy] pool=%s cooldown active (last_rebalance=%s min_interval=%s), skip",
										runtime.Cfg.ID,
										lastAt.Format(time.RFC3339),
										minIntervalStr,
									)
								}
								continue
							}
						}
					}
				}
				mode := string(riskMgr.Snapshot().Mode)
				policyEngine := strategy.NewPolicyEngine(currentCfg.Strategy.Profiles)
				profile := policyEngine.Profile(mode)

				baseCfg := buildStrategyConfig(currentCfg, runtime.Cfg)
				tunedCfg := policyEngine.Apply(mode, baseCfg)

				strategyMu.RLock()
				strat := strategyMap[runtime.Cfg.ID]
				strategyMu.RUnlock()
				if strat == nil {
					continue
				}
				strat.UpdateConfig(tunedCfg)

				widthPct, minWidthPct, maxWidthPct := computeTargetWidthPct(currentCfg, runtime.Cfg, profile, volEstimator.SigmaDaily())
				priceToken := runtime.Cfg.CEXPriceToken
				if priceToken == "" {
					priceToken = runtime.Cfg.Token1
				}
				stableIsToken0 := strings.EqualFold(priceToken, runtime.Cfg.Token1)

				input := engine.EngineInput{
					CexPrice:       currentPrice,
					DexPrice:       runtime.DexPrice,
					Volatility:     widthPct,
					Position:       runtime.Position,
					Token0Decimals: runtime.Cfg.Token0Decimals,
					Token1Decimals: runtime.Cfg.Token1Decimals,
					StableIsToken0: stableIsToken0,
					Params:         engine.StrategyParams{RiskFactor: tunedCfg.EngineRiskFactor, MinSpreadPct: minWidthPct, MaxSpreadPct: maxWidthPct},
				}

				bot.SetPoolStrategySnapshot(runtime.Cfg.ID, volEstimator.SigmaDaily(), widthPct, currentCfg.Strategy.Range.VolWindow, mode, currentPrice)
				_ = eventStream.Publish(ctx, events.TopicStrategy, map[string]interface{}{
					"pool_id":     runtime.Cfg.ID,
					"chain_id":    runtime.Cfg.ChainID,
					"profile":     mode,
					"sigma_daily": volEstimator.SigmaDaily(),
					"width_pct":   widthPct,
					"vol_window":  currentCfg.Strategy.Range.VolWindow,
					"dex_price":   runtime.DexPrice,
					"cex_price":   currentPrice,
					"tick":        runtime.CurrentTick,
				})

				// Periodic visibility for testnet stress runs (even when no intents are produced).
				if last, ok := lastStrategyLog[runtime.Cfg.ID]; !ok || time.Since(last) >= 30*time.Second {
					lastStrategyLog[runtime.Cfg.ID] = time.Now()
					log.Printf("[Strategy] pool=%s sigma_daily=%.4f width_pct=%.4f pos=[%d,%d] tick=%d dexPrice=%.2f cex=%.2f",
						runtime.Cfg.ID,
						volEstimator.SigmaDaily(),
						widthPct,
						runtime.Position.LowerTick,
						runtime.Position.UpperTick,
						runtime.CurrentTick,
						runtime.DexPrice,
						currentPrice,
					)
				}

				intents, err := strat.Evaluate(context.Background(), input)
				if err != nil {
					log.Printf("Strategy Error (pool %s): %v", runtime.Cfg.ID, err)
					continue
				}

				for _, i := range intents {
					if i.Metadata == nil {
						i.Metadata = map[string]string{}
					}
					i.Metadata["sigma_daily"] = fmt.Sprintf("%.6f", volEstimator.SigmaDaily())
					i.Metadata["width_pct"] = fmt.Sprintf("%.6f", widthPct)
					i.Metadata["vol_window"] = currentCfg.Strategy.Range.VolWindow
					intentQueue.Enqueue(i)
				}
			}
		}
	}
}

func computeTargetWidthPct(cfg *config.AppConfig, pool config.PoolConfig, profile config.StrategyProfile, sigmaDaily float64) (widthPct float64, minWidthPct float64, maxWidthPct float64) {
	minW := 0.02
	maxW := 0.20
	k := 2.0
	if cfg != nil {
		if cfg.Strategy.Range.MinWidthPct > 0 && cfg.Strategy.Range.MinWidthPct < 1 {
			minW = cfg.Strategy.Range.MinWidthPct
		}
		if cfg.Strategy.Range.MaxWidthPct > 0 && cfg.Strategy.Range.MaxWidthPct < 1 {
			maxW = cfg.Strategy.Range.MaxWidthPct
		}
		if cfg.Strategy.Range.VolK > 0 {
			k = cfg.Strategy.Range.VolK
		}
	}
	if pool.MinWidthPct > 0 && pool.MinWidthPct < 1 {
		minW = pool.MinWidthPct
	}
	if pool.MaxWidthPct > 0 && pool.MaxWidthPct < 1 {
		maxW = pool.MaxWidthPct
	}
	if maxW < minW {
		maxW = minW
	}
	mult := profile.RangeWidthMultiplier
	if mult <= 0 {
		mult = 1.0
	}
	// If we don't have enough history yet, start from the minimum width.
	if sigmaDaily <= 0 {
		return minW, minW, maxW
	}
	width := sigmaDaily * k * mult
	if width < minW {
		width = minW
	}
	if width > maxW {
		width = maxW
	}
	return width, minW, maxW
}

func buildStrategyConfig(cfg *config.AppConfig, pool config.PoolConfig) strategy.BasicStrategyConfig {
	if cfg == nil {
		return strategy.BasicStrategyConfig{}
	}
	sCfg := strategy.BasicStrategyConfig{
		RiskMode:        "normal",
		StrategyVersion: cfg.StrategyVersion,
		MinSpreadTicks:  100,
	}
	if sCfg.StrategyVersion == "" {
		sCfg.StrategyVersion = cfg.Strategy.Name
	}
	sCfg.PoolID = pool.ID
	sCfg.ChainID = pool.ChainID
	sCfg.Token0Address = pool.Token0
	sCfg.Token1Address = pool.Token1
	sCfg.Token0Decimals = pool.Token0Decimals
	sCfg.Token1Decimals = pool.Token1Decimals
	sCfg.Fee = pool.Fee
	sCfg.PositionManager = pool.PositionManager
	sCfg.Amount0Desired = pool.Amount0
	sCfg.Amount1Desired = pool.Amount1
	sCfg.MaxCapPct = effectiveMaxCapPct(pool.MaxCapPct, cfg.Risk.MaxUtilizationPct)
	sCfg.TickSpacing = strategy.TickSpacingForFee(pool.Fee)
	sCfg.TargetNotionalPct = sCfg.MaxCapPct
	if sCfg.ChainID == 0 && len(cfg.Chains) > 0 {
		sCfg.ChainID = cfg.Chains[0].ID
	}
	return sCfg
}

func effectiveMaxCapPct(poolMax, globalMax float64) float64 {
	capPct := poolMax
	if globalMax > 0 && (capPct <= 0 || capPct > globalMax) {
		capPct = globalMax
	}
	if capPct <= 0 {
		capPct = 0.05
	}
	if capPct > 1 {
		capPct = 1
	}
	return capPct
}

func initEventStream(cfg *config.AppConfig) events.Stream {
	if cfg != nil && cfg.Events.Driver == "file" {
		stream, err := events.NewFileStream(cfg.Events.FilePath)
		if err != nil {
			log.Printf("[Events] file driver init failed: %v, fallback to memory", err)
		} else {
			log.Printf("[Events] using file stream %s", cfg.Events.FilePath)
			return stream
		}
	}
	if cfg != nil && cfg.Events.Driver == "redis" {
		retention, _ := time.ParseDuration(cfg.Events.ReplayRetention)
		stream, err := events.NewRedisStream(
			cfg.Events.RedisURL,
			cfg.Events.RedisPrefix,
			cfg.Events.RedisGroup,
			events.WithAcksRequired(cfg.Events.AcksRequired),
			events.WithReplayRetention(retention),
		)
		if err != nil {
			log.Printf("[Events] redis driver init failed: %v, fallback to memory", err)
		} else {
			log.Printf("[Events] using redis stream %s", cfg.Events.RedisURL)
			return stream
		}
	}
	log.Printf("[Events] using in-memory event stream")
	return events.NewMemoryStream(256)
}

func decodeTicker(payload interface{}) (feed.Ticker, bool) {
	switch v := payload.(type) {
	case feed.Ticker:
		return v, true
	case *feed.Ticker:
		return *v, true
	case []byte:
		var t feed.Ticker
		if err := json.Unmarshal(v, &t); err == nil {
			return t, true
		}
	case json.RawMessage:
		var t feed.Ticker
		if err := json.Unmarshal(v, &t); err == nil {
			return t, true
		}
	}
	return feed.Ticker{}, false
}

type eventsPoolState struct {
	PoolID       string `json:"pool_id"`
	ChainID      int64  `json:"chain_id"`
	PoolAddress  string `json:"pool_address"`
	CurrentTick  int64  `json:"current_tick"`
	Liquidity    string `json:"liquidity"`
	SqrtPriceX96 string `json:"sqrt_price_x96"`
}

func decodePoolState(payload interface{}) (eventsPoolState, bool) {
	switch v := payload.(type) {
	case eventsPoolState:
		return v, true
	case *eventsPoolState:
		return *v, true
	case []byte:
		var s eventsPoolState
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	case json.RawMessage:
		var s eventsPoolState
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	}
	return eventsPoolState{}, false
}

type uniV3PositionSnapshot struct {
	TokenID   *big.Int
	Liquidity *big.Int
	TickLower int64
	TickUpper int64
	Token0    common.Address
	Token1    common.Address
	Fee       uint32
}

func asBigInt(v interface{}) (*big.Int, bool) {
	switch t := v.(type) {
	case *big.Int:
		return new(big.Int).Set(t), true
	case big.Int:
		return new(big.Int).Set(&t), true
	case uint64:
		return new(big.Int).SetUint64(t), true
	case int64:
		return big.NewInt(t), true
	case int32:
		return big.NewInt(int64(t)), true
	case uint32:
		return new(big.Int).SetUint64(uint64(t)), true
	default:
		return nil, false
	}
}

func asInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case *big.Int:
		return t.Int64(), true
	case big.Int:
		return t.Int64(), true
	default:
		return 0, false
	}
}

func asUint32(v interface{}) (uint32, bool) {
	switch t := v.(type) {
	case uint32:
		return t, true
	case uint64:
		return uint32(t), true
	case int64:
		if t < 0 {
			return 0, false
		}
		return uint32(t), true
	case *big.Int:
		if t.Sign() < 0 {
			return 0, false
		}
		return uint32(t.Uint64()), true
	case big.Int:
		if t.Sign() < 0 {
			return 0, false
		}
		return uint32(t.Uint64()), true
	default:
		return 0, false
	}
}

// listMatchingPositions scans wallet's UniV3 NFT positions and returns those matching (token0,token1,fee).
func listMatchingPositions(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, poolCfg config.PoolConfig, maxScan int) ([]uniV3PositionSnapshot, error) {
	if ethGw == nil || adapter == nil {
		return nil, nil
	}
	if poolCfg.PositionManager == "" {
		return nil, nil
	}
	if maxScan <= 0 {
		maxScan = 64
	}
	pmAddr := common.HexToAddress(poolCfg.PositionManager)
	want0 := common.HexToAddress(poolCfg.Token0)
	want1 := common.HexToAddress(poolCfg.Token1)
	wantFee := uint32(poolCfg.Fee)

	out := make([]uniV3PositionSnapshot, 0, 2)
	for idx := 0; idx < maxScan; idx++ {
		data, err := adapter.ParsedABI.Pack("tokenOfOwnerByIndex", ethGw.WalletAddress(), big.NewInt(int64(idx)))
		if err != nil {
			return nil, fmt.Errorf("pack tokenOfOwnerByIndex: %w", err)
		}
		res, err := ethGw.Call(ctx, pmAddr, data)
		if err != nil {
			// No more positions (or RPC error); stop scanning to avoid churn.
			break
		}
		unpacked, err := adapter.ParsedABI.Unpack("tokenOfOwnerByIndex", res)
		if err != nil || len(unpacked) == 0 {
			break
		}
		tokenID, ok := asBigInt(unpacked[0])
		if !ok || tokenID == nil {
			continue
		}

		dataPos, err := adapter.ParsedABI.Pack("positions", tokenID)
		if err != nil {
			return nil, fmt.Errorf("pack positions: %w", err)
		}
		resPos, err := ethGw.Call(ctx, pmAddr, dataPos)
		if err != nil {
			continue
		}
		upPos, err := adapter.ParsedABI.Unpack("positions", resPos)
		if err != nil || len(upPos) < 8 {
			continue
		}

		t0, ok0 := upPos[2].(common.Address)
		t1, ok1 := upPos[3].(common.Address)
		fee, okFee := asUint32(upPos[4])
		tL, okTL := asInt64(upPos[5])
		tU, okTU := asInt64(upPos[6])
		liq, okLiq := asBigInt(upPos[7])
		if !ok0 || !ok1 || !okFee || !okTL || !okTU || !okLiq {
			continue
		}

		if (t0 == want0 && t1 == want1 && fee == wantFee) || (t0 == want1 && t1 == want0 && fee == wantFee) {
			out = append(out, uniV3PositionSnapshot{
				TokenID:   tokenID,
				Liquidity: liq,
				TickLower: tL,
				TickUpper: tU,
				Token0:    t0,
				Token1:    t1,
				Fee:       fee,
			})
		}
	}
	return out, nil
}

func closePositionsForPool(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, poolCfg config.PoolConfig) error {
	if ethGw == nil || adapter == nil {
		return nil
	}
	if poolCfg.PositionManager == "" {
		return fmt.Errorf("position_manager required")
	}
	positions, err := listMatchingPositions(ctx, ethGw, adapter, poolCfg, 64)
	if err != nil {
		return err
	}
	if len(positions) == 0 {
		return nil
	}
	if len(positions) > 5 {
		return fmt.Errorf("refuse to close %d positions for pool %s automatically; run cleanup/manual drain first", len(positions), poolCfg.ID)
	}

	pmAddr := poolCfg.PositionManager
	recipient := ethGw.Address()
	for _, p := range positions {
		if p.TokenID == nil {
			continue
		}

		// 1) DecreaseLiquidity (if needed)
		if p.Liquidity != nil && p.Liquidity.Sign() > 0 {
			intent := strategy.Intent{
				ID:      fmt.Sprintf("WITHDRAW_%s_%s", poolCfg.ID, p.TokenID.String()),
				Type:    strategy.IntentWithdraw,
				PoolID:  poolCfg.ID,
				ChainID: poolCfg.ChainID,
				Metadata: map[string]string{
					"token_id":  p.TokenID.String(),
					"liquidity": p.Liquidity.String(),
					"target":    pmAddr,
					"value":     "0",
				},
			}
			data, err := adapter.BuildDecreaseLiquidityData(intent)
			if err != nil {
				return fmt.Errorf("build decreaseLiquidity: %w", err)
			}
			intent.Metadata["calldata"] = hex.EncodeToString(data)
			res, err := ethGw.Send(ctx, intent)
			if err != nil {
				return fmt.Errorf("send decreaseLiquidity: %w", err)
			}
			_ = waitForReceipt(ctx, ethGw, res.Hash)
		}

		// 2) Collect (always attempt, fees may exist even if liquidity=0)
		collectIntent := strategy.Intent{
			ID:      fmt.Sprintf("COLLECT_%s_%s", poolCfg.ID, p.TokenID.String()),
			Type:    strategy.IntentCollectFee,
			PoolID:  poolCfg.ID,
			ChainID: poolCfg.ChainID,
			Metadata: map[string]string{
				"token_id":  p.TokenID.String(),
				"recipient": recipient,
				"target":    pmAddr,
				"value":     "0",
			},
		}
		collectData, err := adapter.BuildCollectData(collectIntent)
		if err != nil {
			return fmt.Errorf("build collect: %w", err)
		}
		collectIntent.Metadata["calldata"] = hex.EncodeToString(collectData)
		res2, err := ethGw.Send(ctx, collectIntent)
		if err != nil {
			return fmt.Errorf("send collect: %w", err)
		}
		_ = waitForReceipt(ctx, ethGw, res2.Hash)

		// 3) Burn NFT (prevents accumulation of dead positions)
		burnIntent := strategy.Intent{
			ID:      fmt.Sprintf("BURN_%s_%s", poolCfg.ID, p.TokenID.String()),
			Type:    strategy.IntentWithdraw,
			PoolID:  poolCfg.ID,
			ChainID: poolCfg.ChainID,
			Metadata: map[string]string{
				"token_id": p.TokenID.String(),
				"target":   pmAddr,
				"value":    "0",
			},
		}
		burnData, err := adapter.BuildBurnNFTData(burnIntent)
		if err != nil {
			return fmt.Errorf("build burn: %w", err)
		}
		burnIntent.Metadata["calldata"] = hex.EncodeToString(burnData)
		res3, err := ethGw.Send(ctx, burnIntent)
		if err != nil {
			return fmt.Errorf("send burn: %w", err)
		}
		_ = waitForReceipt(ctx, ethGw, res3.Hash)
	}
	return nil
}

func executeSwap(ctx context.Context, gw gateway.Gateway, router *univ3.Router, swapHelper *univ3.SwapHelper, poolCfg config.PoolConfig, action rebalancer.SwapAction, priceProvider func(string) float64, quoter *univ3.Quoter, slippagePct float64, store *storage.Store, stream events.Stream, parentIntentID string, stepIndex *int) (*gateway.TxResult, error) {
	// 1. Build Calldata
	if router == nil && swapHelper == nil {
		return nil, fmt.Errorf("swap executor not initialized (router+swapHelper both nil)")
	}

	recipient := ""
	if addrProvider, ok := gw.(interface{ Address() string }); ok {
		recipient = addrProvider.Address()
	}
	if recipient == "" || common.HexToAddress(recipient) == (common.Address{}) {
		return nil, fmt.Errorf("swap recipient required (refusing to default to zero address)")
	}

	// DEBUG: Print details before swap
	log.Printf("[DEBUG] Executing Swap: From=%s To=%s AmtIn=%s Fee=%d", action.FromToken.Hex(), action.ToToken.Hex(), action.AmountIn.String(), action.Fee)

	if slippagePct <= 0 {
		slippagePct = 0.01
	}

	// Estimate MinAmountOut using Quoter if available, otherwise fallback to local price estimate.
	if action.MinAmountOut == nil || action.MinAmountOut.Sign() == 0 {
		if quoter != nil {
			caller, ok := gw.(interface {
				Call(ctx context.Context, to common.Address, data []byte) ([]byte, error)
			})
			if ok {
				var quotedOut *big.Int
				// Multi-hop quoting is not supported yet; fall back to local estimate.
				if len(action.Path) <= 2 {
					qo, err := quoter.QuoteExactInputSingle(ctx, caller, action.FromToken, action.ToToken, action.Fee, action.AmountIn, big.NewInt(0))
					if err != nil {
						log.Printf("[Quoter] quote failed, fallback to local estimate: %v", err)
					} else {
						quotedOut = qo
					}
				} else {
					log.Printf("[Quoter] multi-hop quote unsupported, fallback to local estimate")
				}
				if quotedOut != nil {
					slipBps := int64(math.Round((1 - slippagePct) * 10000))
					minOut := new(big.Int).Mul(quotedOut, big.NewInt(slipBps))
					minOut.Div(minOut, big.NewInt(10000))
					action.MinAmountOut = minOut
					log.Printf("[Quoter] quotedOut=%s minOut=%s (slip=%.2f%%)", quotedOut, minOut, slippagePct*100)
				}
			}
		}
		if action.MinAmountOut == nil || action.MinAmountOut.Sign() == 0 {
			pFrom := priceProvider(strings.ToLower(action.FromToken.Hex()))
			pTo := priceProvider(strings.ToLower(action.ToToken.Hex()))
			// Router implements local estimate helper; keep using it even when we execute via SwapHelper.
			if router != nil {
				action.MinAmountOut = router.EstimateMinAmountOut(action.AmountIn, action.FromDecimals, action.ToDecimals, pFrom, pTo, slippagePct)
			} else {
				action.MinAmountOut = big.NewInt(0)
			}
			log.Printf("[Swap] local minOut=%s (slip=%.2f%%)", action.MinAmountOut, slippagePct*100)
		}
	}

	// Pick swap executor.
	target := common.Address{}
	var data []byte
	var err error
	if swapHelper != nil {
		target = swapHelper.Address
		pool := common.HexToAddress(poolCfg.Address)
		if pool == (common.Address{}) {
			return nil, fmt.Errorf("swaphelper requires pool address in config")
		}
		data, err = swapHelper.BuildSwapData(pool, action)
	} else {
		target = router.RouterAddress
		data, err = router.BuildSwapData(action, recipient)
	}
	if err != nil {
		return nil, err
	}

	// Ensure allowance for swap executor.
	if ethGw, ok := gw.(*gateway.EthGateway); ok {
		if err := ethGw.EnsureAllowance(ctx, action.FromToken, target, action.AmountIn); err != nil {
			return nil, fmt.Errorf("allowance failed: %w", err)
		}
	}

	intent := strategy.Intent{
		ID:   fmt.Sprintf("SWAP_%s_%s", action.FromToken.Hex(), action.ToToken.Hex()),
		Type: strategy.IntentSwap,
		Metadata: map[string]string{
			"target":   target.Hex(),
			"value":    "0",
			"calldata": hex.EncodeToString(data),
		},
	}

	res, err := gw.Send(ctx, intent)
	if err != nil {
		return nil, err
	}
	idx := 0
	if stepIndex != nil {
		idx = *stepIndex
		*stepIndex++
	}
	bot.RecordStepSent(ctx, store, stream, parentIntentID, idx, "swap", res.Hash.Hex(), map[string]interface{}{
		"from":      action.FromToken.Hex(),
		"to":        action.ToToken.Hex(),
		"amount_in": action.AmountIn.String(),
		"fee":       action.Fee,
		"min_out": func() string {
			if action.MinAmountOut == nil {
				return ""
			}
			return action.MinAmountOut.String()
		}(),
	})
	return res, nil
}

func parseMetadataFloat(meta map[string]string, key string) float64 {
	if meta == nil {
		return 0
	}
	val, ok := meta[key]
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	return f
}

func waitForReceipt(ctx context.Context, ethGw *gateway.EthGateway, hash common.Hash) *types.Receipt {
	if ethGw == nil {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			log.Printf("[ReceiptWait] timeout for %s", hash.Hex())
			return nil
		default:
		}
		rcpt, err := ethGw.TxReceipt(waitCtx, hash)
		if err == nil && rcpt != nil {
			log.Printf("[ReceiptWait] %s mined status=%d gasUsed=%d", hash.Hex(), rcpt.Status, rcpt.GasUsed)
			return rcpt
		}
		time.Sleep(3 * time.Second)
	}
}

func startReceiptWatcher(ctx context.Context, receipts <-chan gateway.ReceiptResult, store *storage.Store, riskMgr *risk.Manager) {
	if store == nil || receipts == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case receipt, ok := <-receipts:
			if !ok {
				return
			}
			gasCostNative := bot.WeiToEther(receipt.EffectiveGasPrice, receipt.GasUsed)
			// Update legacy metadata in case executor wants to use it.
			// We don't have intent object here, so only store level update.
			effPriceStr := ""
			if receipt.EffectiveGasPrice != nil {
				effPriceStr = receipt.EffectiveGasPrice.String()
			}
			from := ""
			to := ""
			if receipt.From != (common.Address{}) {
				from = receipt.From.Hex()
			}
			if receipt.To != (common.Address{}) {
				to = receipt.To.Hex()
			}
			if err := store.UpdateTradeStatusWithGasAndChainMeta(receipt.Hash.Hex(), string(receipt.Status), gasCostNative, receipt.GasUsed, effPriceStr, receipt.Nonce, from, to); err != nil {
				log.Printf("[ReceiptWatcher] update %s failed: %v", receipt.Hash.Hex(), err)
			}
			_ = store.UpsertTxReceipt(&storage.TxReceiptRecord{
				ChainID:           receipt.ChainID,
				TxHash:            receipt.Hash.Hex(),
				Nonce:             receipt.Nonce,
				FromAddr:          from,
				ToAddr:            to,
				Status:            receipt.StatusCode,
				GasUsed:           receipt.GasUsed,
				EffectiveGasPrice: effPriceStr,
				RevertReason:      receipt.RevertReason,
				MinedAt:           time.Now(),
			})
			if gasCostNative > 0 && riskMgr != nil {
				riskMgr.RecordGas(gasCostNative)
			}
		}
	}
}

func hasSufficientBalances(ctx context.Context, gw *gateway.EthGateway, meta map[string]string) bool {
	token0 := meta["token0"]
	amount0 := meta["amount0"]
	if token0 != "" && amount0 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount0, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token0))
			if err != nil || bal.Cmp(required) < 0 {
				return false
			}
		}
	}
	token1 := meta["token1"]
	amount1 := meta["amount1"]
	if token1 != "" && amount1 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount1, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token1))
			if err != nil || bal.Cmp(required) < 0 {
				return false
			}
		}
	}
	return true
}

func updateTokenPrices(cfg *config.AppConfig, store *atomic.Value, ticker feed.Ticker) {
	if cfg == nil || ticker.Price <= 0 {
		return
	}
	current := map[string]float64{}
	if existing := store.Load(); existing != nil {
		for k, v := range existing.(map[string]float64) {
			current[k] = v
		}
	}
	for _, pool := range cfg.Pools {
		// Phase-1: one token per pool is priced by the CEX feed (e.g. WETH via ETHUSDT).
		// Do NOT assume token0/token1 ordering; use pools[].cex_price_token to select.
		priceToken := pool.CEXPriceToken
		if priceToken == "" {
			priceToken = pool.Token1
		}
		current[strings.ToLower(priceToken)] = ticker.Price
		for _, stable := range pool.StableTokens {
			current[strings.ToLower(stable)] = 1.0
		}
	}
	store.Store(current)
}

func lookupTokenPrice(store *atomic.Value, token string) float64 {
	if token == "" || store == nil {
		return 0
	}
	data := store.Load()
	if data == nil {
		return 0
	}
	if price, ok := data.(map[string]float64)[strings.ToLower(token)]; ok {
		return price
	}
	if strings.Contains(strings.ToUpper(token), "USD") {
		return 1.0
	}
	return 0
}

func floatFromBigInt(amount *big.Int, decimals int) float64 {
	if amount == nil {
		return 0
	}
	if decimals <= 0 {
		decimals = 18
	}
	f, _ := new(big.Float).SetInt(amount).Float64()
	return f / math.Pow10(decimals)
}

func runCleanup(ctx context.Context, gateways map[int64]*gateway.EthGateway, cfg *config.AppConfig, store *storage.Store) {
	log.Println(">>> Starting Clean Up Mode: Closing Known Positions <<<")
	if cfg == nil || len(cfg.Pools) == 0 {
		return
	}
	for _, pool := range cfg.Pools {
		if pool.PositionManager == "" {
			continue
		}
		ethGw := gateways[pool.ChainID]
		if ethGw == nil {
			log.Printf("[Cleanup] No gateway for chain %d (pool=%s)", pool.ChainID, pool.ID)
			continue
		}
		tokenID := strings.TrimSpace(pool.PositionTokenID)
		if tokenID == "" && store != nil {
			if tid, err := store.GetPoolPositionTokenID(pool.ID, pool.ChainID); err == nil {
				tokenID = strings.TrimSpace(tid)
			}
		}
		if tokenID == "" {
			log.Printf("[Cleanup] pool %s has no position_token_id; skip", pool.ID)
			continue
		}
		tid, ok := new(big.Int).SetString(tokenID, 10)
		if !ok || tid.Sign() <= 0 {
			log.Printf("[Cleanup] invalid position_token_id for pool %s: %s", pool.ID, tokenID)
			continue
		}
		adapter := univ3.NewAdapter(pool.PositionManager)
		pmAddr := common.HexToAddress(pool.PositionManager)
		log.Printf("[Cleanup] closing pool=%s tokenId=%s", pool.ID, tokenID)
		stepIndex := 0
		if err := bot.ClosePositionTokenID(ctx, ethGw, adapter, pmAddr, tid, nil, nil, "", &stepIndex); err != nil {
			log.Printf("[Cleanup] close failed pool=%s tokenId=%s: %v", pool.ID, tokenID, err)
			continue
		}
		bot.ClearPoolRuntimePosition(pool.ID)
		if store != nil {
			_ = store.ClearPoolPosition(pool.ID, pool.ChainID)
		}
	}
	log.Println(">>> Cleanup Complete <<<")
}

func findPoolConfig(cfg *config.AppConfig, poolID string) (config.PoolConfig, bool) {
	if cfg == nil {
		return config.PoolConfig{}, false
	}
	for _, pool := range cfg.Pools {
		if pool.ID == poolID {
			return pool, true
		}
	}
	return config.PoolConfig{}, false
}

func buildStrategyMapWithPolicy(cfg *config.AppConfig, policy *strategy.PolicyEngine) map[string]*strategy.BasicStrategy {
	result := make(map[string]*strategy.BasicStrategy)
	if cfg == nil {
		return result
	}
	for _, pool := range cfg.Pools {
		stratCfg := buildStrategyConfig(cfg, pool)
		if policy != nil {
			stratCfg = policy.Apply(string(risk.ModeNormal), stratCfg)
		}
		result[pool.ID] = strategy.NewBasicStrategy(stratCfg)
	}
	return result
}

// buildStrategyMap keeps backward compatibility for existing calls.
func buildStrategyMap(cfg *config.AppConfig) map[string]*strategy.BasicStrategy {
	return buildStrategyMapWithPolicy(cfg, strategy.NewPolicyEngine(nil))
}

func snapshotPoolsForAPI() []api.PoolStatus {
	states := bot.SnapshotPoolRuntimes()
	result := make([]api.PoolStatus, 0, len(states))
	for _, rt := range states {
		if rt == nil {
			continue
		}
		sqrtPrice := ""
		if rt.SqrtPriceX96 != nil {
			sqrtPrice = rt.SqrtPriceX96.String()
		}
		result = append(result, api.PoolStatus{
			PoolID:       rt.Cfg.ID,
			ChainID:      rt.Cfg.ChainID,
			DexPrice:     rt.DexPrice,
			CurrentTick:  rt.CurrentTick,
			SqrtPriceX96: sqrtPrice,
			Liquidity:    fmt.Sprintf("%.6f", rt.Position.Liquidity),
		})
	}
	return result
}
