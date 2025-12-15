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
	"github.com/ethereum/go-ethereum/crypto"

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

type swapStats struct {
	FromToken    string  `json:"from_token"`
	ToToken      string  `json:"to_token"`
	AmountIn     string  `json:"amount_in"`
	QuotedOut    string  `json:"quoted_out"`
	MinAmountOut string  `json:"min_amount_out"`
	ActualOut    string  `json:"actual_out"`
	SlippagePct  float64 `json:"slippage_pct"`
	PnLUSD       float64 `json:"pnl_usd"`
	TxHash       string  `json:"tx_hash"`
}

var abortFlag atomic.Bool
var pauseFlag atomic.Bool
var cleanupFlag atomic.Bool

type poolRuntime struct {
	cfg             config.PoolConfig
	position        engine.CurrentPosition
	positionTokenID string
	dexPrice        float64
	currentTick     int64
	sqrtPrice       *big.Int
	poolLiquidity   *big.Int
}

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
	runtimeState, hasRuntime := getPoolRuntimeSnapshot(poolID)
	if !hasRuntime || runtimeState == nil {
		return fmt.Errorf("pool runtime not available for %s", poolID)
	}
	if runtimeState.dexPrice == 0 {
		return fmt.Errorf("dex price not available for %s", poolID)
	}

	// Calculate ticks based on current DEX price with a reasonable spread
	spreadPct := 0.05 // 5% spread
	lowerPrice := runtimeState.dexPrice * (1 - spreadPct)
	upperPrice := runtimeState.dexPrice * (1 + spreadPct)

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

var (
	poolStateMu        sync.RWMutex
	poolStates         = map[string]*poolRuntime{}
	mintGuardMu        sync.RWMutex
	poolMintGuards     = map[string]*atomic.Bool{}
	poolWatcherMu      sync.Mutex
	poolWatcherCancels = map[string]context.CancelFunc{}
)

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

type perPoolDailyLimiter struct {
	mu     sync.Mutex
	dayKey string
	counts map[string]int
}

func (l *perPoolDailyLimiter) Allow(poolID string, limit int) bool {
	if poolID == "" || limit <= 0 {
		return true
	}
	today := time.Now().UTC().Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil || l.dayKey != today {
		l.dayKey = today
		l.counts = map[string]int{}
	}
	if l.counts[poolID] >= limit {
		return false
	}
	l.counts[poolID]++
	return true
}

var rebalanceLimiter perPoolDailyLimiter

var rebalanceAtMu sync.Mutex
var lastRebalanceAt = map[string]time.Time{}

func setLastRebalanceAt(poolID string, t time.Time) {
	if strings.TrimSpace(poolID) == "" {
		return
	}
	if t.IsZero() {
		t = time.Now()
	}
	rebalanceAtMu.Lock()
	defer rebalanceAtMu.Unlock()
	if lastRebalanceAt == nil {
		lastRebalanceAt = map[string]time.Time{}
	}
	lastRebalanceAt[poolID] = t
}

func getLastRebalanceAt(poolID string) (time.Time, bool) {
	rebalanceAtMu.Lock()
	defer rebalanceAtMu.Unlock()
	t, ok := lastRebalanceAt[poolID]
	return t, ok
}

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
	disableAPI := flag.Bool("no-api", false, "Disable API server (no ListenAndServe)")
	disableMonitor := flag.Bool("no-monitor", false, "Disable monitor server (no ListenAndServe)")
	flag.Parse()

	// 1. Load Configuration & watch for hot reload
	cfgManager, err := config.NewManager(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.Current()
	if *dryRunOverride {
		cfg.Strategy.DryRun = true
		log.Println("[Bot] dry-run override enabled")
	}
	dryRun := cfg.Strategy.DryRun
	log.Printf("Phoenix V3 Config Loaded. Chains: %d, Pools: %d", len(cfg.Chains), len(cfg.Pools))
	syncPoolStatesFromConfig(cfg)
	syncMintGuardsFromConfig(cfg)

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
		chainStates = initDexStates(cfg.Chains)
		restartPoolWatchers(ctx, chainStates, cfg, eventStream)
	}

	// 6. Initialize Strategy & Queue
	strategyMu := sync.RWMutex{}
	policyEngine := strategy.NewPolicyEngine(cfg.Strategy.Profiles)
	strategyMap := buildStrategyMapWithPolicy(cfg, policyEngine)
	intentQueue := strategy.NewIntentQueue()
	rebal := rebalancer.NewRebalancer()

	// 7. Initialize Storage (Phase 5)
	store, err := storage.NewStore("phoenix.db")
	if err != nil {
		log.Fatalf("Failed to init db: %v", err)
	}

	// 8. Initialize Gateways (per chain)
	privKey := os.Getenv("BOT_PRIVATE_KEY")
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
	go startPositionSync(ctx, &cfgValue, store, selectGateway)

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
	// Attach PnL store if supported
	apiServer.AttachPnLStore(store)
	apiServer.AttachPauseController(&pauseController{})
	apiServer.AttachCleanupController(&cleanupController{ctx: ctx, gateways: chainGateways, cfgValue: &cfgValue, store: store})
	apiServer.AttachRebalanceController(&rebalanceController{queue: intentQueue, cfgValue: &cfgValue})

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

	go startIntentExecutor(ctx, &cfgValue, intentQueue, riskMgr, guard, selectGateway, store, eventStream, adapter, router, swapHelperByChain, rebal, priceProvider, quoterByChain)
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
				updated.Strategy.DryRun = true
			}
			cfgValue.Store(updated)
			if w, err := time.ParseDuration(updated.Strategy.Range.VolWindow); err == nil {
				volEstimator.SetWindow(w)
			}
			syncPoolStatesFromConfig(updated)
			syncMintGuardsFromConfig(updated)
			chainStates = initDexStates(updated.Chains)
			restartPoolWatchers(ctx, chainStates, updated, eventStream)
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
			poolStateMu.Lock()
			if runtime, exists := poolStates[state.PoolID]; exists {
				liqStr := strings.TrimSpace(state.Liquidity)
				if liqStr != "" {
					if liqBI, ok := new(big.Int).SetString(liqStr, 10); ok {
						runtime.poolLiquidity = liqBI
					}
				}
				liq, _ := strconv.ParseFloat(state.Liquidity, 64)
				priceToken := runtime.cfg.CEXPriceToken
				if priceToken == "" {
					priceToken = runtime.cfg.Token1
				}
				// If the CEX-priced token is token1, then the stable side is token0; otherwise stable side is token1.
				stableIsToken0 := strings.EqualFold(priceToken, runtime.cfg.Token1)
				runtime.dexPrice = tickToDexPrice(state.CurrentTick, runtime.cfg.Token0Decimals, runtime.cfg.Token1Decimals, stableIsToken0)
				runtime.currentTick = state.CurrentTick
				_ = liq // liquidity is still logged below; position is synced separately from chain.
				if state.SqrtPriceX96 != "" {
					if sqrt, ok := new(big.Int).SetString(state.SqrtPriceX96, 10); ok {
						runtime.sqrtPrice = sqrt
					}
				}
				log.Printf("[DEX] Pool %s tick=%d liquidity=%s dexPrice=%.2f", state.PoolID, state.CurrentTick, state.Liquidity, runtime.dexPrice)
			} else {
				log.Printf("[DEX] Received state for unknown pool %s", state.PoolID)
			}
			poolStateMu.Unlock()

		case <-queryTicker.C:
			if pauseFlag.Load() {
				log.Printf("[Control] paused, skipping strategy evaluation")
				continue
			}
			for _, runtime := range snapshotPoolRuntimes() {
				if guard := getMintGuard(runtime.cfg.ID); guard.Load() {
					log.Printf("[PositionGuard] Mint in progress for pool %s, skipping strategy evaluation", runtime.cfg.ID)
					continue
				}

				if runtime.dexPrice == 0 {
					log.Printf("[Strategy] Pool %s pending dex price, skip", runtime.cfg.ID)
					continue
				}

				currentCfg := cfgValue.Load().(*config.AppConfig)
				if currentCfg != nil {
					minIntervalStr := strings.TrimSpace(currentCfg.Strategy.Rebalance.MinInterval)
					if minIntervalStr != "" {
						if minInterval, err := time.ParseDuration(minIntervalStr); err == nil && minInterval > 0 {
							if lastAt, ok := getLastRebalanceAt(runtime.cfg.ID); ok && time.Since(lastAt) < minInterval {
								if last, ok := lastCooldownLog[runtime.cfg.ID]; !ok || time.Since(last) >= 30*time.Second {
									lastCooldownLog[runtime.cfg.ID] = time.Now()
									log.Printf("[Strategy] pool=%s cooldown active (last_rebalance=%s min_interval=%s), skip",
										runtime.cfg.ID,
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

				baseCfg := buildStrategyConfig(currentCfg, runtime.cfg)
				tunedCfg := policyEngine.Apply(mode, baseCfg)

				strategyMu.RLock()
				strat := strategyMap[runtime.cfg.ID]
				strategyMu.RUnlock()
				if strat == nil {
					continue
				}
				strat.UpdateConfig(tunedCfg)

				widthPct, minWidthPct, maxWidthPct := computeTargetWidthPct(currentCfg, runtime.cfg, profile, volEstimator.SigmaDaily())
				priceToken := runtime.cfg.CEXPriceToken
				if priceToken == "" {
					priceToken = runtime.cfg.Token1
				}
				stableIsToken0 := strings.EqualFold(priceToken, runtime.cfg.Token1)

				input := engine.EngineInput{
					CexPrice:       currentPrice,
					DexPrice:       runtime.dexPrice,
					Volatility:     widthPct,
					Position:       runtime.position,
					Token0Decimals: runtime.cfg.Token0Decimals,
					Token1Decimals: runtime.cfg.Token1Decimals,
					StableIsToken0: stableIsToken0,
					Params:         engine.StrategyParams{RiskFactor: tunedCfg.EngineRiskFactor, MinSpreadPct: minWidthPct, MaxSpreadPct: maxWidthPct},
				}

				// Periodic visibility for testnet stress runs (even when no intents are produced).
				if last, ok := lastStrategyLog[runtime.cfg.ID]; !ok || time.Since(last) >= 30*time.Second {
					lastStrategyLog[runtime.cfg.ID] = time.Now()
					log.Printf("[Strategy] pool=%s sigma_daily=%.4f width_pct=%.4f pos=[%d,%d] tick=%d dexPrice=%.2f cex=%.2f",
						runtime.cfg.ID,
						volEstimator.SigmaDaily(),
						widthPct,
						runtime.position.LowerTick,
						runtime.position.UpperTick,
						runtime.currentTick,
						runtime.dexPrice,
						currentPrice,
					)
				}

				intents, err := strat.Evaluate(context.Background(), input)
				if err != nil {
					log.Printf("Strategy Error (pool %s): %v", runtime.cfg.ID, err)
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

func tickToDexPrice(tick int64, token0Decimals, token1Decimals int, stableIsToken0 bool) float64 {
	// Uniswap V3 tick encodes the raw price:
	//   rawPrice = 1.0001^tick = (token1Raw / token0Raw)
	//
	// Phoenix Phase-1 expects a human-readable "stable per priced-token" value comparable
	// to the CEX feed (e.g. USD per ETH). Because token0/token1 ordering is address-sorted,
	// the stable side can be either token0 or token1 (configured via stable_tokens + cex_price_token).
	rawPrice := math.Pow(1.0001, float64(tick))
	if rawPrice <= 0 {
		return 0
	}
	if stableIsToken0 {
		// stable(token0) per priced(token1)
		return (1.0 / rawPrice) * math.Pow10(token1Decimals-token0Decimals)
	}
	// stable(token1) per priced(token0)
	return rawPrice * math.Pow10(token0Decimals-token1Decimals)
}

func restartPoolWatchers(ctx context.Context, stateMap map[int64]*dexstate.UniV3State, cfg *config.AppConfig, stream events.Stream) {
	poolWatcherMu.Lock()
	for id, cancel := range poolWatcherCancels {
		log.Printf("[DEX] stopping watcher for pool %s", id)
		cancel()
	}
	poolWatcherCancels = make(map[string]context.CancelFunc)
	poolWatcherMu.Unlock()

	if cfg == nil {
		return
	}

	for _, pool := range cfg.Pools {
		client := stateMap[pool.ChainID]
		if client == nil || pool.Address == "" {
			log.Printf("[DEX] missing rpc or address for pool %s", pool.ID)
			continue
		}
		addr := common.HexToAddress(pool.Address)
		watchCtx, cancel := context.WithCancel(ctx)
		poolID := pool.ID
		go func(chainID int64, c *dexstate.UniV3State, watchAddr common.Address, pid string, localCtx context.Context) {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-localCtx.Done():
					return
				case <-ticker.C:
					state, err := c.GetPoolState(chainID, watchAddr)
					if err != nil {
						log.Printf("[DEX] fetch pool state failed (%s): %v", pid, err)
						continue
					}
					payload := eventsPoolState{
						PoolID:       pid,
						ChainID:      state.ChainID,
						PoolAddress:  state.PoolAddress.Hex(),
						CurrentTick:  state.CurrentTick,
						Liquidity:    state.Liquidity.String(),
						SqrtPriceX96: state.SqrtPriceX96.String(),
					}
					_ = stream.Publish(localCtx, events.TopicPoolState, payload)
				}
			}
		}(pool.ChainID, client, addr, poolID, watchCtx)

		poolWatcherMu.Lock()
		poolWatcherCancels[poolID] = cancel
		poolWatcherMu.Unlock()
	}
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

func parseMintedPositionTokenID(rcpt *types.Receipt, positionManager common.Address, recipient common.Address) *big.Int {
	if rcpt == nil {
		return nil
	}
	transferSig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	for _, lg := range rcpt.Logs {
		if lg == nil {
			continue
		}
		if lg.Address != positionManager {
			continue
		}
		if len(lg.Topics) < 4 {
			continue
		}
		if lg.Topics[0] != transferSig {
			continue
		}
		fromAddr := common.BytesToAddress(lg.Topics[1].Bytes()[12:])
		toAddr := common.BytesToAddress(lg.Topics[2].Bytes()[12:])
		if fromAddr != (common.Address{}) {
			continue
		}
		if toAddr != recipient {
			continue
		}
		return new(big.Int).SetBytes(lg.Topics[3].Bytes())
	}
	return nil
}

func fetchPositionByTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int) (int64, int64, *big.Int, bool, error) {
	if ethGw == nil || adapter == nil || tokenID == nil || tokenID.Sign() <= 0 {
		return 0, 0, nil, false, fmt.Errorf("invalid inputs")
	}
	dataPos, err := adapter.ParsedABI.Pack("positions", tokenID)
	if err != nil {
		return 0, 0, nil, false, err
	}
	resPos, err := ethGw.Call(ctx, pmAddr, dataPos)
	if err != nil {
		return 0, 0, nil, false, err
	}
	upPos, err := adapter.ParsedABI.Unpack("positions", resPos)
	if err != nil || len(upPos) < 8 {
		if err == nil {
			err = fmt.Errorf("unexpected positions unpack len=%d", len(upPos))
		}
		return 0, 0, nil, false, err
	}
	tL, okTL := asInt64(upPos[5])
	tU, okTU := asInt64(upPos[6])
	liq, okLiq := asBigInt(upPos[7])
	if !okTL || !okTU || !okLiq {
		return 0, 0, nil, false, fmt.Errorf("unexpected positions types")
	}
	return tL, tU, liq, true, nil
}

func setPoolRuntimePosition(poolID string, tokenID string, pos engine.CurrentPosition) {
	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	if rt, ok := poolStates[poolID]; ok && rt != nil {
		rt.position = pos
		rt.positionTokenID = strings.TrimSpace(tokenID)
	}
}

func clearPoolRuntimePosition(poolID string) {
	setPoolRuntimePosition(poolID, "", engine.CurrentPosition{})
}

func closePositionTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int) error {
	if ethGw == nil || adapter == nil || tokenID == nil || tokenID.Sign() <= 0 {
		return nil
	}
	tL, tU, liq, ok, posErr := fetchPositionByTokenID(ctx, ethGw, adapter, pmAddr, tokenID)
	if !ok {
		// If the tokenId no longer exists (already burned / transferred), "positions" may revert.
		// Treat this as already-closed so the bot can recover by clearing its stored tokenId.
		if posErr != nil {
			msg := posErr.Error()
			if strings.Contains(msg, "execution reverted") || strings.Contains(msg, "not found") {
				log.Printf("[Rebalance] position tokenId=%s not found (already closed?): %v", tokenID.String(), posErr)
				return nil
			}
			return fmt.Errorf("failed to fetch position tokenId=%s: %w", tokenID.String(), posErr)
		}
		return fmt.Errorf("failed to fetch position tokenId=%s", tokenID.String())
	}

	// 1) DecreaseLiquidity (if needed)
	if liq != nil && liq.Sign() > 0 {
		intent := strategy.Intent{
			ID:      fmt.Sprintf("WITHDRAW_%s", tokenID.String()),
			Type:    strategy.IntentWithdraw,
			PoolID:  "",
			ChainID: ethGw.ChainID().Int64(),
			Metadata: map[string]string{
				"token_id":  tokenID.String(),
				"liquidity": liq.String(),
				"target":    pmAddr.Hex(),
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

	// 2) Collect (always attempt)
	collectIntent := strategy.Intent{
		ID:      fmt.Sprintf("COLLECT_%s", tokenID.String()),
		Type:    strategy.IntentCollectFee,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"recipient": ethGw.Address(),
			"target":    pmAddr.Hex(),
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

	// 3) Burn NFT (safe even if already at 0 liquidity)
	burnIntent := strategy.Intent{
		ID:      fmt.Sprintf("BURN_%s", tokenID.String()),
		Type:    strategy.IntentWithdraw,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id": tokenID.String(),
			"target":   pmAddr.Hex(),
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

	// Best-effort: clear local runtime position after close.
	_ = tL
	_ = tU
	return nil
}

// drainPositionTokenID reduces most liquidity and collects, but keeps a small residual liquidity.
// This is useful when the pool has no other liquidity and we still want to execute swaps
// against the pool before fully burning the position.
func drainPositionTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int, keepPct float64) (bool, error) {
	if ethGw == nil || adapter == nil || tokenID == nil || tokenID.Sign() <= 0 {
		return false, nil
	}
	if keepPct <= 0 {
		return false, nil
	}
	if keepPct >= 1 {
		return true, nil
	}

	_, _, liq, ok, posErr := fetchPositionByTokenID(ctx, ethGw, adapter, pmAddr, tokenID)
	if !ok {
		if posErr != nil {
			msg := posErr.Error()
			if strings.Contains(msg, "execution reverted") || strings.Contains(msg, "not found") {
				log.Printf("[Rebalance] position tokenId=%s not found (already closed?): %v", tokenID.String(), posErr)
				return false, nil
			}
			return false, fmt.Errorf("fetch position tokenId=%s: %w", tokenID.String(), posErr)
		}
		return false, fmt.Errorf("fetch position tokenId=%s failed", tokenID.String())
	}
	if liq == nil || liq.Sign() <= 0 {
		return false, nil
	}

	keep := new(big.Int)
	// floor(liq * keepPct)
	fKeep := new(big.Float).Mul(new(big.Float).SetInt(liq), big.NewFloat(keepPct))
	fKeep.Int(keep)
	if keep.Sign() < 0 {
		keep.SetInt64(0)
	}
	// Ensure we withdraw at least 1 unit if possible.
	if keep.Cmp(liq) >= 0 {
		keep.Sub(liq, big.NewInt(1))
	}
	if keep.Sign() < 0 {
		keep.SetInt64(0)
	}
	withdraw := new(big.Int).Sub(liq, keep)
	if withdraw.Sign() <= 0 {
		// Keep everything.
		return true, nil
	}

	log.Printf("[Rebalance] draining position tokenId=%s keepPct=%.3f withdrawLiq=%s keepLiq=%s", tokenID.String(), keepPct, withdraw.String(), keep.String())

	// 1) DecreaseLiquidity (partial)
	intent := strategy.Intent{
		ID:      fmt.Sprintf("DRAIN_%s", tokenID.String()),
		Type:    strategy.IntentWithdraw,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"liquidity": withdraw.String(),
			"target":    pmAddr.Hex(),
			"value":     "0",
		},
	}
	data, err := adapter.BuildDecreaseLiquidityData(intent)
	if err != nil {
		return false, fmt.Errorf("build decreaseLiquidity: %w", err)
	}
	intent.Metadata["calldata"] = hex.EncodeToString(data)
	res, err := ethGw.Send(ctx, intent)
	if err != nil {
		return false, fmt.Errorf("send decreaseLiquidity: %w", err)
	}
	_ = waitForReceipt(ctx, ethGw, res.Hash)

	// 2) Collect (best-effort)
	collectIntent := strategy.Intent{
		ID:      fmt.Sprintf("COLLECT_DRAIN_%s", tokenID.String()),
		Type:    strategy.IntentCollectFee,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"recipient": ethGw.Address(),
			"target":    pmAddr.Hex(),
			"value":     "0",
		},
	}
	collectData, err := adapter.BuildCollectData(collectIntent)
	if err != nil {
		return false, fmt.Errorf("build collect: %w", err)
	}
	collectIntent.Metadata["calldata"] = hex.EncodeToString(collectData)
	res2, err := ethGw.Send(ctx, collectIntent)
	if err != nil {
		return false, fmt.Errorf("send collect: %w", err)
	}
	_ = waitForReceipt(ctx, ethGw, res2.Hash)
	return true, nil
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

// startPositionSync periodically updates poolStates[].position from on-chain UniV3 positions.
// This prevents Strategy from minting multiple positions for the same pool.
//
// If we don't yet know the pool's tokenId (config + DB empty), we fall back to scanning
// wallet positions via tokenOfOwnerByIndex and adopt the best matching position.
func startPositionSync(ctx context.Context, cfgValue *atomic.Value, store *storage.Store, gwSelector func(int64) gateway.Gateway) {
	syncOnce := func() {
		cfg, _ := cfgValue.Load().(*config.AppConfig)
		if cfg == nil {
			return
		}
		for _, pool := range cfg.Pools {
			gw := gwSelector(pool.ChainID)
			ethGw, ok := gw.(*gateway.EthGateway)
			if !ok || ethGw == nil || pool.PositionManager == "" {
				continue
			}

			adapter := univ3.NewAdapter(pool.PositionManager)

			// Prefer learned tokenId from storage over config, so rebalances that mint a new NFT
			// keep working even if config.yaml wasn't manually updated yet.
			tokenID := ""
			if store != nil {
				if tid, err := store.GetPoolPositionTokenID(pool.ID, pool.ChainID); err == nil {
					tokenID = strings.TrimSpace(tid)
				}
			}
			if tokenID == "" {
				tokenID = strings.TrimSpace(pool.PositionTokenID)
			}
			// If we still don't know the tokenId, scan wallet positions and adopt a matching one.
			// This prevents "same pair minted many LPs" after restarts when config wasn't updated.
			if tokenID == "" {
				pos, err := listMatchingPositions(ctx, ethGw, adapter, pool, 64)
				if err != nil {
					continue
				}
				if len(pos) == 0 {
					continue
				}

				var best *uniV3PositionSnapshot
				for i := range pos {
					if pos[i].TokenID == nil {
						continue
					}
					if best == nil {
						best = &pos[i]
						continue
					}
					// Prefer higher liquidity, then higher tokenId as a stable tie-break.
					liqA := pos[i].Liquidity
					liqB := best.Liquidity
					if liqA != nil && liqB != nil {
						if liqA.Cmp(liqB) > 0 {
							best = &pos[i]
							continue
						}
						if liqA.Cmp(liqB) == 0 && pos[i].TokenID.Cmp(best.TokenID) > 0 {
							best = &pos[i]
							continue
						}
					} else if liqA != nil && (liqB == nil || liqB.Sign() <= 0) {
						best = &pos[i]
						continue
					}
				}
				if best == nil || best.TokenID == nil {
					continue
				}
				tokenID = best.TokenID.String()
				if store != nil {
					_ = store.UpsertPoolPosition(pool.ID, pool.ChainID, tokenID)
				}
			}

			pmAddr := common.HexToAddress(pool.PositionManager)
			tid, ok := new(big.Int).SetString(tokenID, 10)
			if !ok || tid.Sign() <= 0 {
				continue
			}
			dataPos, err := adapter.ParsedABI.Pack("positions", tid)
			if err != nil {
				continue
			}
			resPos, err := ethGw.Call(ctx, pmAddr, dataPos)
			if err != nil {
				continue
			}
			upPos, err := adapter.ParsedABI.Unpack("positions", resPos)
			if err != nil || len(upPos) < 8 {
				continue
			}
			tL, okTL := asInt64(upPos[5])
			tU, okTU := asInt64(upPos[6])
			liq, okLiq := asBigInt(upPos[7])
			if !okTL || !okTU || !okLiq {
				continue
			}

			poolStateMu.Lock()
			if rt, exists := poolStates[pool.ID]; exists && rt != nil {
				rt.positionTokenID = tokenID
				if liq == nil || liq.Sign() <= 0 {
					rt.position = engine.CurrentPosition{}
				} else {
					liqF, _ := new(big.Float).SetInt(liq).Float64()
					rt.position = engine.CurrentPosition{LowerTick: tL, UpperTick: tU, Liquidity: liqF}
				}
			}
			poolStateMu.Unlock()
		}
	}

	syncOnce()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			syncOnce()
		}
	}
}

func startIntentExecutor(ctx context.Context, cfgValue *atomic.Value, queue *strategy.IntentQueue, riskMgr *risk.Manager, guard *poolguard.Guard, gwSelector func(int64) gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter, router *univ3.Router, swapHelperByChain map[int64]*univ3.SwapHelper, rebal rebalancer.Rebalancer, priceProvider func(string) float64, quoterByChain map[int64]*univ3.Quoter) {
	go func() {
		for {
			intent := queue.Dequeue()

			select {
			case <-ctx.Done():
				return
			default:
			}

			if riskMgr.ShouldThrottle(2 * time.Second) {
				log.Printf("[IntentExecutor] throttling intent %s due to min interval", intent.ID)
				time.Sleep(2 * time.Second)
				continue
			}

			gw := gwSelector(intent.ChainID)
			executeIntent(ctx, cfgValue, intent, riskMgr, guard, gw, store, stream, adapter, router, swapHelperByChain, rebal, priceProvider, quoterByChain)
		}
	}()
}

func executeIntent(ctx context.Context, cfgValue *atomic.Value, intent strategy.Intent, riskMgr *risk.Manager, guard *poolguard.Guard, gw gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter, router *univ3.Router, swapHelperByChain map[int64]*univ3.SwapHelper, rebal rebalancer.Rebalancer, priceProvider func(string) float64, quoterByChain map[int64]*univ3.Quoter) {
	if intent.Metadata == nil {
		intent.Metadata = make(map[string]string)
	}
	if gw == nil {
		log.Printf("[IntentExecutor] no gateway available for chain %d", intent.ChainID)
		return
	}
	if err := riskMgr.CanProceed(); err != nil {
		log.Printf("[Risk] skip intent %s: %v", intent.ID, err)
		// return // Dont return here to allow seeing logs? No, should return.
		return
	}

	token0Addr := intent.Metadata["token0"]
	token1Addr := intent.Metadata["token1"]
	check := guard.CheckPool(context.Background(), intent.PoolID, intent.ChainID, token0Addr, token1Addr)
	if check.Risk == poolguard.RiskDanger {
		log.Printf("[PoolGuard] block intent %s: %s", intent.ID, check.Reason)
		return
	}

	currentCfg := cfgValue.Load().(*config.AppConfig)
	isDryRun := currentCfg != nil && currentCfg.Strategy.DryRun
	poolCfg, ok := findPoolConfig(currentCfg, intent.PoolID)
	if !ok {
		log.Printf("[IntentExecutor] Unknown pool %s", intent.PoolID)
		return
	}

	// Hard cap per-pool rebalance attempts (prevents runaway churn).
	if intent.Type == strategy.IntentRebalance && !isDryRun {
		if !rebalanceLimiter.Allow(intent.PoolID, poolCfg.MaxDailyRebalances) {
			log.Printf("[Risk] skip intent %s: pool %s max_daily_rebalances=%d exceeded", intent.ID, intent.PoolID, poolCfg.MaxDailyRebalances)
			return
		}
	}

	// Use per-pool PositionManager (avoids cfg.Pools[0] coupling).
	localAdapter := adapter
	if poolCfg.PositionManager != "" {
		if localAdapter == nil || !strings.EqualFold(localAdapter.TargetAddress().Hex(), poolCfg.PositionManager) {
			localAdapter = univ3.NewAdapter(poolCfg.PositionManager)
		}
	}

	// Resolve known UniV3 position tokenId for this pool (config -> store -> runtime).
	existingTokenID := ""
	if rt, ok := getPoolRuntimeSnapshot(intent.PoolID); ok && rt != nil {
		existingTokenID = strings.TrimSpace(rt.positionTokenID)
	}
	if existingTokenID == "" && store != nil {
		if tid, err := store.GetPoolPositionTokenID(intent.PoolID, intent.ChainID); err == nil {
			existingTokenID = strings.TrimSpace(tid)
		}
	}
	if existingTokenID == "" {
		existingTokenID = strings.TrimSpace(poolCfg.PositionTokenID)
	}
	if existingTokenID != "" {
		intent.Metadata["position_token_id"] = existingTokenID
	}

	// Guard the whole execution (including close-existing-position) to avoid generating/queuing
	// overlapping intents while we temporarily have no LP on-chain.
	poolMintGuard := getMintGuard(intent.PoolID)
	poolMintGuard.Store(true)
	defer poolMintGuard.Store(false)

	// If we're rebalancing an active LP, close existing matching positions first so
	// subsequent planning uses "wallet balance + recovered LP funds" as the baseline.
	//
	// NOTE: On testnets where we are the only LP, fully removing liquidity makes swaps against
	// the pool revert. When configured, we keep a small residual liquidity until swaps are done.
	ethGw, isEthGw := gw.(*gateway.EthGateway)
	var deferredCloseTokenID *big.Int
	if intent.Type == strategy.IntentRebalance && isEthGw && !isDryRun {
		if localAdapter == nil {
			log.Printf("[IntentExecutor] missing position manager adapter for pool %s", intent.PoolID)
			riskMgr.RecordFailure()
			return
		}
		if existingTokenID != "" {
			if tokenID, ok := new(big.Int).SetString(existingTokenID, 10); ok && tokenID.Sign() > 0 {
				log.Printf("[Rebalance] closing existing position tokenId=%s before planning", existingTokenID)
				pmAddr := common.HexToAddress(poolCfg.PositionManager)
				keepPct := 0.0
				if currentCfg != nil {
					keepPct = currentCfg.Strategy.Rebalance.KeepLiquidityPctForSwaps
				}
				if keepPct > 0 {
					// Drain most liquidity but keep a small residual until swaps complete.
					deferred, err := drainPositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID, keepPct)
					if err != nil {
						log.Printf("[IntentExecutor] drain existing position failed (pool %s): %v", intent.PoolID, err)
						riskMgr.RecordFailure()
						return
					}
					if deferred {
						deferredCloseTokenID = tokenID
					} else {
						// Nothing to defer; proceed with full close.
						if err := closePositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID); err != nil {
							log.Printf("[IntentExecutor] close existing position failed (pool %s): %v", intent.PoolID, err)
							riskMgr.RecordFailure()
							return
						}
						clearPoolRuntimePosition(intent.PoolID)
						if store != nil {
							if err := store.ClearPoolPosition(intent.PoolID, intent.ChainID); err != nil {
								log.Printf("[Storage] clear pool position failed (pool=%s chain=%d): %v", intent.PoolID, intent.ChainID, err)
							}
						}
						existingTokenID = ""
						intent.Metadata["position_token_id"] = ""
					}
				} else {
					// Default behavior: fully close+burn before planning.
					if err := closePositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID); err != nil {
						log.Printf("[IntentExecutor] close existing position failed (pool %s): %v", intent.PoolID, err)
						riskMgr.RecordFailure()
						return
					}
					clearPoolRuntimePosition(intent.PoolID)
					if store != nil {
						if err := store.ClearPoolPosition(intent.PoolID, intent.ChainID); err != nil {
							log.Printf("[Storage] clear pool position failed (pool=%s chain=%d): %v", intent.PoolID, intent.ChainID, err)
						}
					}
					existingTokenID = ""
					intent.Metadata["position_token_id"] = ""
				}
			}
		}
	}

	// Capture pre-action wallet balances for PnL/cost-basis calculations.
	var preBal0, preBal1 *big.Int
	shouldComputeWalletDelta := intent.Type == strategy.IntentWithdraw || intent.Type == strategy.IntentCollectFee || intent.Type == strategy.IntentRebalance
	if shouldComputeWalletDelta {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			preBal0, _ = ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token0))
			preBal1, _ = ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token1))
		}
	}
	runtimeState, hasRuntime := getPoolRuntimeSnapshot(intent.PoolID)
	if !hasRuntime {
		log.Printf("[IntentExecutor] Pool runtime snapshot missing for %s", intent.PoolID)
	}
	var poolStateSnap rebalancer.PoolStateSnapshot
	if runtimeState != nil {
		poolStateSnap.CurrentTick = runtimeState.currentTick
		if runtimeState.sqrtPrice != nil {
			poolStateSnap.SqrtPriceX96 = new(big.Int).Set(runtimeState.sqrtPrice)
		}
	}

	// --- Rebalancer Logic ---
	// Only run if not dry_run (or run in dry_run to simulate logs)
	// And if gw available for balance check
	if rebal != nil && isEthGw && (intent.Type == strategy.IntentRebalance || intent.TargetNotionalPct > 0) {
		log.Printf("[Rebalancer] analyzing intent %s...", intent.ID)
		log.Printf("[Debug] Intent Metadata: %+v", intent.Metadata)

		// 1. Fetch Balances
		bals := make(map[string]*big.Int)
		// Helper to fetch
		fetchBal := func(addr string) {
			if addr == "" {
				return
			}
			if b, err := ethGw.BalanceOfERC20(ctx, common.HexToAddress(addr)); err == nil {
				bals[strings.ToLower(addr)] = b
			}
		}
		fetchBal(token0Addr)
		fetchBal(token1Addr)
		// Include pool-configured stable tokens to improve budget estimation (e.g., USDC).
		for _, st := range poolCfg.StableTokens {
			fetchBal(st)
		}
		// Assume configured stablecoin?
		// For Phase 1, we might just scan a known stable if in config, but let's stick to T0/T1 if undefined.

		token0 := strings.ToLower(token0Addr)
		token1 := strings.ToLower(token1Addr)

		d0 := poolCfg.Token0Decimals
		d1 := poolCfg.Token1Decimals
		if d0 == 0 {
			d0 = 18
		}
		if d1 == 0 {
			d1 = 18
		}
		poolFee := poolCfg.Fee
		if poolFee == 0 {
			poolFee = 3000
		}
		intent.Metadata["fee"] = strconv.Itoa(poolFee)

		stables := make([]string, 0, len(poolCfg.StableTokens))
		for _, st := range poolCfg.StableTokens {
			stables = append(stables, strings.ToLower(st))
		}

		input := rebalancer.RebalanceInput{
			Intent:        intent,
			WalletBalance: bals,
			Prices: map[string]float64{
				token0: priceProvider(token0),
				token1: priceProvider(token1),
			},
			PoolConfig: rebalancer.PoolConfig{
				PoolID:         intent.PoolID,
				Token0:         token0,
				Token1:         token1,
				Token0Decimals: d0,
				Token1Decimals: d1,
				Fee:            poolFee,
				MaxCapPct:      poolCfg.MaxCapPct,
				StableTokens:   stables,
			},
			RiskLimits: rebalancer.RiskLimits{
				MinIdleCashPct:     currentCfg.Wallet.MinIdlePct,
				MaxSwapSlippagePct: currentCfg.Risk.MaxSwapSlippagePct,
			},
			State: poolStateSnap,
		}

		// 3. Plan
		if plan, err := rebal.Rebalance(ctx, input); err == nil && plan != nil {
			log.Printf("[Rebalancer] Plan generated: %d swaps", len(plan.Swaps))

			// 4. Update Intent Amounts (Target)
			if plan.FinalLP.Amount0 != nil {
				intent.Metadata["amount0"] = plan.FinalLP.Amount0.String()
			}
			if plan.FinalLP.Amount1 != nil {
				intent.Metadata["amount1"] = plan.FinalLP.Amount1.String()
			}

			// 5. Execute Swaps
			quoter := quoterByChain[intent.ChainID]
			swapHelper := (*univ3.SwapHelper)(nil)
			if swapHelperByChain != nil {
				swapHelper = swapHelperByChain[intent.ChainID]
			}
			swapStatsList := make([]swapStats, 0, len(plan.Swaps))
			for _, s := range plan.Swaps {
				if currentCfg != nil && currentCfg.Strategy.DryRun {
					log.Printf("[Rebalancer] dry-run enabled; skip executing swap %s->%s", s.FromToken.Hex(), s.ToToken.Hex())
					continue
				}
				swapUSD := s.EstimatedUSD
				if swapUSD > 0 {
					if err := riskMgr.CanSwap(swapUSD); err != nil {
						log.Printf("[Risk] Swap rejected (%s): %v", intent.ID, err)
						return
					}
				}

				// 2. Execute Swap
				var balBeforeOut *big.Int
				if ethGw != nil {
					balBeforeOut, _ = ethGw.BalanceOfERC20(ctx, s.ToToken)
				}
				// If we are executing swaps via SwapHelper, the underlying pool must have active liquidity.
				// On testnets, if the bot is the only LP and liquidity is temporarily 0, skip swaps so we can mint first.
				if swapHelper != nil {
					if runtimeState == nil || runtimeState.poolLiquidity == nil || runtimeState.poolLiquidity.Sign() <= 0 {
						log.Printf("[Rebalancer] skip swap (pool liquidity=0) to allow mint first (from=%s to=%s)", s.FromToken.Hex(), s.ToToken.Hex())
						continue
					}
				}
				res, err := executeSwap(ctx, gw, router, swapHelper, poolCfg, s, priceProvider, quoter, s.SlippagePct)
				if err != nil {
					log.Printf("[Rebalancer] Swap failed: %v. Aborting intent.", err)
					riskMgr.RecordFailure()
					return
				}

				// 3. Wait for receipt instead of sleeping
				if ethGw != nil && res != nil {
					rcpt := waitForReceipt(ctx, ethGw, res.Hash)
					if rcpt == nil || rcpt.Status != 1 {
						log.Printf("[Rebalancer] Swap tx reverted (hash=%s)", res.Hash.Hex())
						riskMgr.RecordFailure()
						return
					}
				}
				if swapUSD > 0 {
					riskMgr.RecordSwap(swapUSD)
				}

				// 4. Capture actual out and slippage
				var actualOut *big.Int
				if ethGw != nil {
					balAfterOut, _ := ethGw.BalanceOfERC20(ctx, s.ToToken)
					if balBeforeOut != nil && balAfterOut != nil {
						actualOut = new(big.Int).Sub(balAfterOut, balBeforeOut)
					}
				}
				st := swapStats{
					FromToken:    s.FromToken.Hex(),
					ToToken:      s.ToToken.Hex(),
					AmountIn:     s.AmountIn.String(),
					MinAmountOut: s.MinAmountOut.String(),
					TxHash:       res.Hash.Hex(),
				}
				// Estimate swap PnL as actual USD out minus USD in using current price snapshot.
				pFrom := priceProvider(strings.ToLower(s.FromToken.Hex()))
				pTo := priceProvider(strings.ToLower(s.ToToken.Hex()))
				usdIn := floatFromBigInt(s.AmountIn, s.FromDecimals) * pFrom
				usdOut := 0.0
				if actualOut != nil {
					st.ActualOut = actualOut.String()
					usdOut = floatFromBigInt(actualOut, s.ToDecimals) * pTo
					if s.MinAmountOut != nil && s.MinAmountOut.Sign() > 0 && actualOut.Cmp(s.MinAmountOut) < 0 {
						log.Printf("[Rebalancer] Swap output below minOut: actual=%s min=%s (hash=%s)", actualOut.String(), s.MinAmountOut.String(), res.Hash.Hex())
						riskMgr.RecordFailure()
						return
					}
					if s.MinAmountOut != nil && s.MinAmountOut.Sign() > 0 {
						fActual, _ := new(big.Float).SetInt(actualOut).Float64()
						fMin, _ := new(big.Float).SetInt(s.MinAmountOut).Float64()
						if fMin > 0 {
							st.SlippagePct = (fMin - fActual) / fMin
						}
					}
				}
				if usdIn > 0 || usdOut > 0 {
					st.PnLUSD = usdOut - usdIn
				}
				swapStatsList = append(swapStatsList, st)

			}

			if len(swapStatsList) > 0 {
				totalSwapPnL := 0.0
				for _, st := range swapStatsList {
					totalSwapPnL += st.PnLUSD
				}
				intent.ExpectedPnL += totalSwapPnL
				intent.Metadata["swap_pnl_usd"] = fmt.Sprintf("%.6f", totalSwapPnL)
				if b, err := json.Marshal(swapStatsList); err == nil {
					intent.Metadata["swap_details"] = string(b)
				}
			}

		} else if err != nil {
			log.Printf("[Rebalancer] Error: %v", err)
			log.Printf("[IntentExecutor] Aborting intent %s due to rebalancer error", intent.ID)
			riskMgr.RecordFailure()
			return // ⚠️ CRITICAL: Stop execution if rebalancer fails
		}
	}

	// --- Refresh & Clamp Amounts for Minting ---
	// Since swaps executed, actual balance might be slightly less than target due to fees.
	// We clamp intent amounts to actual balance to ensure Mint succeeds.
	if ethGw, ok := gw.(*gateway.EthGateway); ok {
		// If we kept residual liquidity for swaps, finalize full close+burn before minting the new position.
		if deferredCloseTokenID != nil && deferredCloseTokenID.Sign() > 0 {
			pmAddr := common.HexToAddress(poolCfg.PositionManager)
			log.Printf("[Rebalance] finalizing close of tokenId=%s after swaps", deferredCloseTokenID.String())
			if err := closePositionTokenID(ctx, ethGw, localAdapter, pmAddr, deferredCloseTokenID); err != nil {
				log.Printf("[IntentExecutor] finalize close failed (pool %s): %v", intent.PoolID, err)
				riskMgr.RecordFailure()
				return
			}
			clearPoolRuntimePosition(intent.PoolID)
			if store != nil {
				if err := store.ClearPoolPosition(intent.PoolID, intent.ChainID); err != nil {
					log.Printf("[Storage] clear pool position failed (pool=%s chain=%d): %v", intent.PoolID, intent.ChainID, err)
					// Continue; this is persistence-only.
				}
			}
			intent.Metadata["position_token_id"] = ""
			deferredCloseTokenID = nil
		}

		t0 := common.HexToAddress(intent.Metadata["token0"])
		t1 := common.HexToAddress(intent.Metadata["token1"])

		if bal0, err := ethGw.BalanceOfERC20(ctx, t0); err == nil {
			if amt0, ok := new(big.Int).SetString(intent.Metadata["amount0"], 10); ok {
				if bal0.Cmp(amt0) < 0 {
					log.Printf("[MintGuard] Clamping Amount0: Target %s -> Balance %s", amt0, bal0)
					intent.Metadata["amount0"] = bal0.String()
				}
			}
		}
		if bal1, err := ethGw.BalanceOfERC20(ctx, t1); err == nil {
			if amt1, ok := new(big.Int).SetString(intent.Metadata["amount1"], 10); ok {
				if bal1.Cmp(amt1) < 0 {
					log.Printf("[MintGuard] Clamping Amount1: Target %s -> Balance %s", amt1, bal1)
					intent.Metadata["amount1"] = bal1.String()
				}
			}
		}
	}
	// ------------------------

	intent.Metadata["dry_run"] = "false" // overwritten below
	log.Printf("[IntentExecutor] executing %s", intent.ID)

	var txHash string
	var status string

	// Use config dry-run.
	intent.Metadata["dry_run"] = fmt.Sprintf("%v", isDryRun)
	log.Printf("[IntentExecutor] dry_run=%v", isDryRun)

	log.Println("[IntentExecutor] Mint phase start (no fixed sleep)")
	if !isDryRun && localAdapter != nil {
		if intent.Type != strategy.IntentRebalance {
			log.Printf("[IntentExecutor] skip mint phase for non-rebalance intent type=%s", intent.Type)
			return
		}

		if addrProvider, ok := gw.(interface{ Address() string }); ok {
			intent.Metadata["recipient"] = addrProvider.Address()
		} else {
			log.Printf("[IntentExecutor] recipient unavailable (gateway has no Address())")
			riskMgr.RecordFailure()
			return
		}
		intent.Metadata["target"] = localAdapter.TargetAddress().Hex()

		// Ensure Allowance for PositionManager
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			// Find Token0/Token1 and Amounts
			// We parse from Metadata because Plan might not be fully carried here if Rebalance skipped?
			// Actually RebalancePlan updates intent.Metadata["amount0"] etc.
			// Let's parse

			// Note: We need to know token addresses. They are in Metadata "token0", "token1" if Strategy set them.
			t0Addr := common.HexToAddress(intent.Metadata["token0"])
			t1Addr := common.HexToAddress(intent.Metadata["token1"])

			amt0, _ := new(big.Int).SetString(intent.Metadata["amount0"], 10)
			amt1, _ := new(big.Int).SetString(intent.Metadata["amount1"], 10)

			if amt0 != nil && amt0.Sign() > 0 {
				if err := ethGw.EnsureAllowance(ctx, t0Addr, localAdapter.TargetAddress(), amt0); err != nil {
					log.Printf("[IntentExecutor] Approve Token0 failed: %v", err)
					return // or continue? better return to avoid revert.
				}
			}
			if amt1 != nil && amt1.Sign() > 0 {
				if err := ethGw.EnsureAllowance(ctx, t1Addr, localAdapter.TargetAddress(), amt1); err != nil {
					log.Printf("[IntentExecutor] Approve Token1 failed: %v", err)
					return
				}
			}
		}

		if data, err := localAdapter.BuildMintData(intent); err == nil {
			intent.Metadata["calldata"] = hex.EncodeToString(data)
		} else {
			log.Printf("[Adapter] build calldata failed: %v", err)
			riskMgr.RecordFailure()
			return
		}
	}

	if !isDryRun {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			if !hasSufficientBalances(ctx, ethGw, intent.Metadata) {
				log.Printf("[BalanceGuard] skip intent %s due to insufficient token balance", intent.ID)
				riskMgr.RecordFailure()
				return
			}
		}
	}

	var minedReceipt *types.Receipt
	if isDryRun || gw == nil {
		txHash = "0xSIMULATED_" + intent.ID
		status = "simulated"
		log.Println(">>> Dry Run: Simulated Tx Execution")
		riskMgr.RecordSuccess()
	} else {
		result, err := gw.Send(ctx, intent)
		if err != nil {
			log.Printf("[Gateway] send intent %s failed: %v", intent.ID, err)
			status = "failed"
			riskMgr.RecordFailure()
		} else {
			txHash = result.Hash.Hex()
			status = string(result.Status)
			riskMgr.RecordSuccess()
			// For mint/withdraw intents, wait for receipt to avoid sleeping.
			if ethGw, ok := gw.(*gateway.EthGateway); ok && result.Status == gateway.StatusPending {
				minedReceipt = waitForReceipt(ctx, ethGw, result.Hash)
			}
		}
	}

	// If we just minted a new position, extract tokenId from receipt and persist it.
	if !isDryRun && minedReceipt != nil && intent.Type == strategy.IntentRebalance {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			pmAddr := common.HexToAddress(poolCfg.PositionManager)
			newTokenID := parseMintedPositionTokenID(minedReceipt, pmAddr, ethGw.WalletAddress())
			if newTokenID != nil && newTokenID.Sign() > 0 {
				tokenStr := newTokenID.String()
				if store != nil {
					if err := store.UpsertPoolPosition(intent.PoolID, intent.ChainID, tokenStr); err != nil {
						log.Printf("[Storage] upsert pool position failed (pool=%s chain=%d tokenId=%s): %v", intent.PoolID, intent.ChainID, tokenStr, err)
					}
				}
				// Refresh runtime position snapshot from chain (ticks/liquidity).
				if tL, tU, liq, ok, _ := fetchPositionByTokenID(ctx, ethGw, localAdapter, pmAddr, newTokenID); ok && liq != nil && liq.Sign() > 0 {
					liqF, _ := new(big.Float).SetInt(liq).Float64()
					setPoolRuntimePosition(intent.PoolID, tokenStr, engine.CurrentPosition{LowerTick: tL, UpperTick: tU, Liquidity: liqF})
				} else {
					setPoolRuntimePosition(intent.PoolID, tokenStr, engine.CurrentPosition{})
				}
				intent.Metadata["position_token_id"] = tokenStr
				log.Printf("[Rebalance] minted new position tokenId=%s", tokenStr)
				setLastRebalanceAt(intent.PoolID, time.Now())
			} else {
				log.Printf("[Rebalance] warning: could not parse minted tokenId from receipt %s", txHash)
			}
		}
	}

	// Compute wallet delta for mint/rebalance/collect/withdraw and update cost basis / PnL.
	if shouldComputeWalletDelta {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			postBal0, _ := ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token0))
			postBal1, _ := ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token1))
			p0 := priceProvider(strings.ToLower(poolCfg.Token0))
			p1 := priceProvider(strings.ToLower(poolCfg.Token1))
			usdDelta0 := floatFromBigInt(new(big.Int).Sub(postBal0, preBal0), poolCfg.Token0Decimals) * p0
			usdDelta1 := floatFromBigInt(new(big.Int).Sub(postBal1, preBal1), poolCfg.Token1Decimals) * p1
			deltaUSD := usdDelta0 + usdDelta1

			// Mint/rebalance: negative delta is capital deployed; use as cost basis.
			if store != nil && intent.Type == strategy.IntentRebalance {
				if deltaUSD < 0 {
					_ = store.UpsertPoolCostBasis(intent.PoolID, intent.ChainID, -deltaUSD)
				}
				intent.Metadata["wallet_delta_usd"] = fmt.Sprintf("%.6f", deltaUSD)
			}

			// Collect/withdraw: positive delta is realized return.
			if intent.Type == strategy.IntentWithdraw || intent.Type == strategy.IntentCollectFee {
				intent.Metadata["wallet_pnl_usd"] = fmt.Sprintf("%.6f", deltaUSD)
				if store != nil && intent.Type == strategy.IntentWithdraw {
					basis, _ := store.GetPoolCostBasis(intent.PoolID, intent.ChainID)
					if basis > 0 {
						intent.ExpectedPnL += deltaUSD - basis
						_ = store.ClearPoolCostBasis(intent.PoolID, intent.ChainID)
					} else {
						intent.ExpectedPnL += deltaUSD
					}
				} else {
					intent.ExpectedPnL += deltaUSD
				}
			}
		}
	}

	// Estimate total PnL from swaps only. Mint/collect PnL still TODO.
	totalPnL := intent.ExpectedPnL

	record := &storage.TradeRecord{
		Time:            time.Now(),
		IntentID:        intent.ID,
		Type:            string(intent.Type),
		PoolID:          intent.PoolID,
		ChainID:         intent.ChainID,
		TxHash:          txHash,
		TargetTo:        intent.Metadata["target"],
		Status:          status,
		Token0Amt:       intent.Metadata["amount0"],
		Token1Amt:       intent.Metadata["amount1"],
		SwapDetails:     intent.Metadata["swap_details"],
		PnL:             totalPnL,
		IsSimulation:    isDryRun,
		StrategyVersion: intent.StrategyVersion,
		RiskMode:        intent.RiskMode,
		NotionalUSD:     parseMetadataFloat(intent.Metadata, "notional_usd"),
		GasCostUSD:      parseMetadataFloat(intent.Metadata, "gas_usd"),
	}
	if err := store.SaveTrade(record); err != nil {
		log.Printf("Failed to save trade: %v", err)
	}

	_ = stream.Publish(ctx, events.TopicIntentExec, record)
}

func executeSwap(ctx context.Context, gw gateway.Gateway, router *univ3.Router, swapHelper *univ3.SwapHelper, poolCfg config.PoolConfig, action rebalancer.SwapAction, priceProvider func(string) float64, quoter *univ3.Quoter, slippagePct float64) (*gateway.TxResult, error) {
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
			gasCostNative := weiToEther(receipt.EffectiveGasPrice, receipt.GasUsed)
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
		if err := closePositionTokenID(ctx, ethGw, adapter, pmAddr, tid); err != nil {
			log.Printf("[Cleanup] close failed pool=%s tokenId=%s: %v", pool.ID, tokenID, err)
			continue
		}
		clearPoolRuntimePosition(pool.ID)
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

func syncPoolStatesFromConfig(cfg *config.AppConfig) {
	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	newStates := make(map[string]*poolRuntime, len(cfg.Pools))
	for _, pool := range cfg.Pools {
		if runtime, ok := poolStates[pool.ID]; ok {
			runtime.cfg = pool
			newStates[pool.ID] = runtime
		} else {
			newStates[pool.ID] = &poolRuntime{
				cfg:      pool,
				position: engine.CurrentPosition{},
			}
		}
	}
	poolStates = newStates
}

func syncMintGuardsFromConfig(cfg *config.AppConfig) {
	mintGuardMu.Lock()
	defer mintGuardMu.Unlock()
	if poolMintGuards == nil {
		poolMintGuards = map[string]*atomic.Bool{}
	}
	for _, pool := range cfg.Pools {
		if _, ok := poolMintGuards[pool.ID]; !ok {
			poolMintGuards[pool.ID] = &atomic.Bool{}
		}
	}
	for id := range poolMintGuards {
		exists := false
		for _, pool := range cfg.Pools {
			if pool.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			delete(poolMintGuards, id)
		}
	}
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

func getMintGuard(poolID string) *atomic.Bool {
	mintGuardMu.RLock()
	defer mintGuardMu.RUnlock()
	if guard, ok := poolMintGuards[poolID]; ok {
		return guard
	}
	return &atomic.Bool{}
}

func initDexStates(chains []config.ChainConfig) map[int64]*dexstate.UniV3State {
	states := make(map[int64]*dexstate.UniV3State)
	for _, ch := range chains {
		state, err := dexstate.NewUniV3State(ch.RPC)
		if err != nil {
			log.Printf("⚠️ Failed to connect RPC for chain %s: %v", ch.Name, err)
			continue
		}
		states[ch.ID] = state
		log.Printf("✅ Connected to RPC %s (chain %d)", ch.Name, ch.ID)
	}
	return states
}

func snapshotPoolRuntimes() []*poolRuntime {
	poolStateMu.RLock()
	defer poolStateMu.RUnlock()
	result := make([]*poolRuntime, 0, len(poolStates))
	for _, rt := range poolStates {
		if clone := clonePoolRuntime(rt); clone != nil {
			result = append(result, clone)
		}
	}
	return result
}

func snapshotPoolsForAPI() []api.PoolStatus {
	states := snapshotPoolRuntimes()
	result := make([]api.PoolStatus, 0, len(states))
	for _, rt := range states {
		if rt == nil {
			continue
		}
		sqrtPrice := ""
		if rt.sqrtPrice != nil {
			sqrtPrice = rt.sqrtPrice.String()
		}
		result = append(result, api.PoolStatus{
			PoolID:       rt.cfg.ID,
			ChainID:      rt.cfg.ChainID,
			DexPrice:     rt.dexPrice,
			CurrentTick:  rt.currentTick,
			SqrtPriceX96: sqrtPrice,
			Liquidity:    fmt.Sprintf("%.6f", rt.position.Liquidity),
		})
	}
	return result
}

func getPoolRuntimeSnapshot(poolID string) (*poolRuntime, bool) {
	poolStateMu.RLock()
	defer poolStateMu.RUnlock()
	rt, ok := poolStates[poolID]
	if !ok {
		return nil, false
	}
	return clonePoolRuntime(rt), true
}

func clonePoolRuntime(rt *poolRuntime) *poolRuntime {
	if rt == nil {
		return nil
	}
	clone := *rt
	if rt.sqrtPrice != nil {
		clone.sqrtPrice = new(big.Int).Set(rt.sqrtPrice)
	}
	return &clone
}

func weiToEther(gasPrice *big.Int, gasUsed uint64) float64 {
	if gasPrice == nil || gasUsed == 0 {
		return 0
	}
	wei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasUsed))
	fWei, _ := new(big.Float).SetInt(wei).Float64()
	return fWei / 1e18
}
