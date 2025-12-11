package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"math"
	"math/big"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"

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
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Load Configuration & watch for hot reload
	cfgManager, err := config.NewManager("configs/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.Current()
	log.Printf("Phoenix V3 Config Loaded. Chains: %d, Pools: %d", len(cfg.Chains), len(cfg.Pools))

	var cfgValue atomic.Value
	cfgValue.Store(cfg)

	// 2. Initialize Monitor
	monitorService := monitor.NewMonitor(cfg.Monitoring)
	go monitorService.Start()

	// Create price cache to store real prices from Binance
	var currentPrice float64 = 2005.0 // Default fallback price
	var currentDexPrice float64 = currentPrice
	var isBinanceConnected bool = false
	token0Decimals := 18
	token1Decimals := 18
	if len(cfg.Pools) > 0 {
		if cfg.Pools[0].Token0Decimals > 0 {
			token0Decimals = cfg.Pools[0].Token0Decimals
		}
		if cfg.Pools[0].Token1Decimals > 0 {
			token1Decimals = cfg.Pools[0].Token1Decimals
		}
	}

	eventStream := initEventStream(cfg)

	priceAggregator := feed.NewAggregator()
	defer priceAggregator.Close()
	go func() {
		for t := range priceAggregator.Output() {
			_ = eventStream.Publish(ctx, events.TopicTicker, t)
		}
	}()

	// 3. Start CEX Feed (Binance)
	binanceFeed := feed.NewBinanceFeed()
	binanceFeed.OnStatusUpdate(func(status feed.FeedStatus) {
		monitorService.UpdateFeedMetric(monitor.FeedMetric{
			Source:       status.Source,
			Healthy:      status.Healthy,
			DelayMs:      status.DelayMs,
			LastUpdateAt: status.LastUpdateAt,
		})
	})
	// Try to subscribe to Binance, use fallback if fails
	tickerResult, err := binanceFeed.SubscribeTicker("ETHUSDT")
	if err != nil {
		log.Printf("⚠️ Failed to subscribe to Binance: %v", err)
	} else {
		isBinanceConnected = true
		log.Println("✅ Subscribed to Binance ETHUSDT")
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
	})
	if cgChan, err := coingeckoFeed.SubscribeTicker("ETHUSDT"); err != nil {
		log.Printf("⚠️ Failed to subscribe CoinGecko: %v", err)
	} else {
		priceAggregator.AddSource("coingecko", cgChan)
	}

	priceEvents, cancelPriceSub, _ := eventStream.Subscribe(events.TopicTicker)
	defer cancelPriceSub()
	poolEvents, cancelPoolSub, _ := eventStream.Subscribe(events.TopicPoolState)
	defer cancelPoolSub()

	// 4. Start DEX State (RPC)
	var uniState *dexstate.UniV3State
	if len(cfg.Chains) > 0 {
		uniState, err = dexstate.NewUniV3State(cfg.Chains[0].RPC)
		if err != nil {
			log.Printf("⚠️ Failed to connect to RPC: %v", err)
		} else {
			log.Println("✅ Connected to ETH RPC")
		}
	}
	startPoolWatcher(ctx, uniState, cfg, eventStream)

	// 6. Initialize Strategy & Queue
	strategyCfg := buildStrategyConfig(cfg)
	strat := strategy.NewBasicStrategy(strategyCfg)
	intentQueue := strategy.NewIntentQueue()

	// 7. Initialize Storage (Phase 5)
	store, err := storage.NewStore("phoenix.db")
	if err != nil {
		log.Fatalf("Failed to init db: %v", err)
	}

	// 8. Initialize Gateway (Phase 4)
	privKey := os.Getenv("BOT_PRIVATE_KEY")
	if privKey == "" {
		log.Fatal("BOT_PRIVATE_KEY not set")
	}
	chainGateway, err := gateway.NewEthGateway(cfg.Chains[0].RPC, privKey)
	if err != nil {
		log.Printf("[Gateway] Failed to init (Check RPC): %v", err)
	}

	// 9. Initialize API Server
	apiServer := api.NewServerWithConfig(intentQueue, api.ServerConfig{
		BinanceConnected: isBinanceConnected,
		PriceSource:      map[bool]string{true: "Binance", false: "Fallback"}[isBinanceConnected],
	})

	// Initialize API with current price
	apiServer.UpdateCEXPrice(feed.Ticker{
		Symbol:    "ETHUSDT",
		Price:     currentPrice,
		Timestamp: time.Now(),
	})

	apiServer.Start("8080")

	// 10. Initialize Risk & PoolGuard (Phase 6)
	riskMgr := risk.NewManager(cfg.Risk.MaxDailyGas, cfg.Risk.ConsecutiveFails, cfg.Risk.MaxDrawdown)
	guard := poolguard.NewGuard()
	// Add a dummy blacklist for testing
	guard.AddBlacklistToken("0x000000000000000000000000000000000000dead")

	log.Println("Phoenix V3 Bot Started (Phase 6: Secured).")

	var adapter *univ3.Adapter
	if len(cfg.Pools) > 0 && cfg.Pools[0].PositionManager != "" {
		adapter = univ3.NewAdapter(cfg.Pools[0].PositionManager)
	}

	go startIntentExecutor(ctx, &cfgValue, intentQueue, riskMgr, guard, chainGateway, store, eventStream, adapter)
	if chainGateway != nil {
		go startReceiptWatcher(ctx, chainGateway.Receipts(), store)
	}

	go func() {
		for updated := range cfgManager.Updates() {
			if updated == nil {
				continue
			}
			cfgValue.Store(updated)
			riskMgr.UpdateLimits(updated.Risk.MaxDailyGas, updated.Risk.ConsecutiveFails, updated.Risk.MaxDrawdown)
			strat.UpdateConfig(buildStrategyConfig(updated))
		}
	}()

	queryTicker := time.NewTicker(5 * time.Second)
	defer queryTicker.Stop()

	// Mock position
	currentPos := engine.CurrentPosition{LowerTick: 200000, UpperTick: 202000, Liquidity: 1000}

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

		case evt, ok := <-poolEvents:
			if !ok {
				continue
			}
			state, ok := decodePoolState(evt.Payload)
			if !ok {
				continue
			}
			liq, _ := strconv.ParseFloat(state.Liquidity, 64)
			dexPrice := tickToDexPrice(state.CurrentTick, token0Decimals, token1Decimals)
			if dexPrice > 0 {
				currentDexPrice = dexPrice
			}
			log.Printf("[DEX] Pool %s tick=%d liquidity=%s dexPrice=%.2f", state.PoolAddress, state.CurrentTick, state.Liquidity, currentDexPrice)
			currentPos = engine.CurrentPosition{
				LowerTick: state.CurrentTick - 200,
				UpperTick: state.CurrentTick + 200,
				Liquidity: liq,
			}

		case <-queryTicker.C:
			// 1. Strategy Step
			input := engine.EngineInput{
				CexPrice:   currentPrice,
				DexPrice:   currentDexPrice,
				Volatility: 0.02,
				Position:   currentPos,
				Params:     engine.StrategyParams{RiskFactor: 1.0},
			}
			intents, err := strat.Evaluate(context.Background(), input)
			if err != nil {
				log.Printf("Strategy Error: %v", err)
				continue
			}

			// 2. Queue Step，由 Intent Executor 统一调度
			for _, i := range intents {
				intentQueue.Enqueue(i)
			}
		}
	}
}

func buildStrategyConfig(cfg *config.AppConfig) strategy.BasicStrategyConfig {
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
	if len(cfg.Pools) > 0 {
		sCfg.PoolID = cfg.Pools[0].ID
		sCfg.ChainID = cfg.Pools[0].ChainID
		sCfg.Token0Address = cfg.Pools[0].Token0
		sCfg.Token1Address = cfg.Pools[0].Token1
		sCfg.Fee = cfg.Pools[0].Fee
		sCfg.PositionManager = cfg.Pools[0].PositionManager
		sCfg.Amount0Desired = cfg.Pools[0].Amount0
		sCfg.Amount1Desired = cfg.Pools[0].Amount1
	}
	if sCfg.ChainID == 0 && len(cfg.Chains) > 0 {
		sCfg.ChainID = cfg.Chains[0].ID
	}
	return sCfg
}

func initEventStream(cfg *config.AppConfig) events.Stream {
	if cfg != nil && cfg.Events.Driver == "redis" {
		stream, err := events.NewRedisStream(cfg.Events.RedisURL, cfg.Events.RedisPrefix, cfg.Events.RedisGroup)
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
	ChainID     int64  `json:"chain_id"`
	PoolAddress string `json:"pool_address"`
	CurrentTick int64  `json:"current_tick"`
	Liquidity   string `json:"liquidity"`
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

func tickToDexPrice(tick int64, token0Decimals, token1Decimals int) float64 {
	price := math.Pow(1.0001, float64(tick))
	decimalFactor := math.Pow(10, float64(token0Decimals-token1Decimals))
	price *= decimalFactor
	if price <= 0 {
		return 0
	}
	return 1 / price
}

func startPoolWatcher(ctx context.Context, uniState *dexstate.UniV3State, cfg *config.AppConfig, stream events.Stream) {
	if uniState == nil || cfg == nil || len(cfg.Pools) == 0 {
		return
	}
	pool := cfg.Pools[0]
	if pool.Address == "" {
		return
	}
	addr := common.HexToAddress(pool.Address)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state, err := uniState.GetPoolState(pool.ChainID, addr)
				if err != nil {
					log.Printf("[DEX] fetch pool state failed: %v", err)
					continue
				}
				payload := eventsPoolState{
					ChainID:     state.ChainID,
					PoolAddress: state.PoolAddress.Hex(),
					CurrentTick: state.CurrentTick,
					Liquidity:   state.Liquidity.String(),
				}
				_ = stream.Publish(ctx, events.TopicPoolState, payload)
			}
		}
	}()
}

func startIntentExecutor(ctx context.Context, cfgValue *atomic.Value, queue *strategy.IntentQueue, riskMgr *risk.Manager, guard *poolguard.Guard, gw gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter) {
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

			executeIntent(ctx, cfgValue, intent, riskMgr, guard, gw, store, stream, adapter)
		}
	}()
}

func executeIntent(ctx context.Context, cfgValue *atomic.Value, intent strategy.Intent, riskMgr *risk.Manager, guard *poolguard.Guard, gw gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter) {
	if intent.Metadata == nil {
		intent.Metadata = make(map[string]string)
	}
	if err := riskMgr.CanProceed(); err != nil {
		log.Printf("[Risk] skip intent %s: %v", intent.ID, err)
		return
	}

	check := guard.CheckPool(context.Background(), intent.PoolID, "0xTokenA", "0xTokenB")
	if check.Risk == poolguard.RiskDanger {
		log.Printf("[PoolGuard] block intent %s: %s", intent.ID, check.Reason)
		return
	}

	currentCfg := cfgValue.Load().(*config.AppConfig)
	isDryRun := currentCfg.Strategy.DryRun
	log.Printf("[IntentExecutor] executing %s dry_run=%v", intent.ID, isDryRun)

	var txHash string
	var status string

	if !isDryRun && adapter != nil {
		if addrProvider, ok := gw.(interface{ Address() string }); ok {
			intent.Metadata["recipient"] = addrProvider.Address()
		}
		intent.Metadata["target"] = adapter.TargetAddress().Hex()
		if data, err := adapter.BuildMintData(intent); err == nil {
			intent.Metadata["calldata"] = hex.EncodeToString(data)
		} else {
			log.Printf("[Adapter] build calldata failed: %v", err)
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
		}
	}

	record := &storage.TradeRecord{
		Time:            time.Now(),
		IntentID:        intent.ID,
		Type:            string(intent.Type),
		PoolID:          intent.PoolID,
		ChainID:         intent.ChainID,
		TxHash:          txHash,
		Status:          status,
		PnL:             0,
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

func startReceiptWatcher(ctx context.Context, receipts <-chan gateway.ReceiptResult, store *storage.Store) {
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
			if err := store.UpdateTradeStatusByHash(receipt.Hash.Hex(), string(receipt.Status)); err != nil {
				log.Printf("[ReceiptWatcher] update %s failed: %v", receipt.Hash.Hex(), err)
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
