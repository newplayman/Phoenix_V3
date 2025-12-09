package main

import (
	"context"
	"log"
	"time"

	"phoenix-v3/internal/api"
	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/dexstate"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/monitor"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

func main() {
	// 1. Load Configuration
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Phoenix V3 Config Loaded. Chains: %d, Pools: %d", len(cfg.Chains), len(cfg.Pools))

	// 2. Initialize Monitor
	monitorService := monitor.NewMonitor(cfg.Monitoring)
	go monitorService.Start()

	// 3. Start CEX Feed (Binance)
	binanceFeed := feed.NewBinanceFeed()
	// Subscribe to ETHUSDT
	// Note: In real app, loop through cfg.Pools to find corresponding symbols.
	// For Phase 1 demo, hardcode "ETHUSDT".
	tickerChan, err := binanceFeed.SubscribeTicker("ETHUSDT")
	if err != nil {
		log.Printf("Failed to subscribe to Binance: %v", err)
	} else {
		log.Println("Subscribed to Binance ETHUSDT")
	}

	// 4. Start DEX State (RPC)
	// Use the first chain from config
	var uniState *dexstate.UniV3State
	if len(cfg.Chains) > 0 {
		uniState, err = dexstate.NewUniV3State(cfg.Chains[0].RPC)
		if err != nil {
			log.Printf("Failed to connect to RPC: %v", err)
		} else {
			log.Println("Connected to ETH RPC")
		}
	}
	_ = uniState // Keep variable for future use

	// 6. Initialize Strategy & Queue
	strat := strategy.NewBasicStrategy()
	intentQueue := strategy.NewIntentQueue()

	// 7. Initialize Storage (Phase 5)
	store, err := storage.NewStore("phoenix.db")
	if err != nil {
		log.Fatalf("Failed to init db: %v", err)
	}

	// 8. Initialize Gateway (Phase 4)
	// We use a dummy key for testing "0x..."
	// In production, this comes from ENV or secure vault.
	dummyKey := "0000000000000000000000000000000000000000000000000000000000000001"
	gateway, err := gateway.NewEthGateway(cfg.Chains[0].RPC, dummyKey)
	if err != nil {
		log.Printf("[Gateway] Failed to init (Check RPC): %v", err)
	}

	// 9. Initialize API Server
	apiServer := api.NewServer(intentQueue)
	apiServer.Start("8081")

	// 10. Initialize Risk & PoolGuard (Phase 6)
	riskMgr := risk.NewManager(cfg.Risk.MaxDailyGas, cfg.Risk.ConsecutiveFails, cfg.Risk.MaxDrawdown)
	guard := poolguard.NewGuard()
	// Add a dummy blacklist for testing
	guard.AddBlacklistToken("0x000000000000000000000000000000000000dead")

	log.Println("Phoenix V3 Bot Started (Phase 6: Secured).")

	queryTicker := time.NewTicker(5 * time.Second)
	defer queryTicker.Stop()

	// Mock position
	currentPos := engine.CurrentPosition{LowerTick: 200000, UpperTick: 202000, Liquidity: 1000}

	for {
		select {
		case t, ok := <-tickerChan:
			if !ok {
				return
			}
			apiServer.UpdateCEXPrice(t)

		case <-queryTicker.C:
			// 1. Strategy Step
			input := engine.EngineInput{
				CexPrice:   2005.0,
				DexPrice:   2000.0,
				Volatility: 0.02,
				Position:   currentPos,
				Params:     engine.StrategyParams{RiskFactor: 1.0},
			}
			intents, err := strat.Evaluate(context.Background(), input)
			if err != nil {
				log.Printf("Strategy Error: %v", err)
				continue
			}

			// 2. Queue Step
			for _, i := range intents {
				// PHASE 6: Risk Check BEFORE Enqueue/Execution
				if err := riskMgr.CanProceed(); err != nil {
					log.Printf("[Risk] Action Blocked: %v", err)
					continue
				}

				// PHASE 6: PoolGuard Check
				// In reality, we check this once at config load or periodically, not every intent.
				// For demo, we check here.
				check := guard.CheckPool(context.Background(), i.PoolID, "0xTokenA", "0xTokenB")
				if check.Risk == poolguard.RiskDanger {
					log.Printf("[PoolGuard] DANGER! Skipping intent for pool %s: %s", i.PoolID, check.Reason)
					continue
				}

				intentQueue.Push(i)
				// Phase 5: Executing from Queue immediately (simplified)

				// 3. Execution Step (Gateway)
				// Check isDryRun?
				isDryRun := cfg.Strategy.DryRun

				log.Printf("Executing Intent %s [DryRun=%v]", i.ID, isDryRun)

				var txHash string
				var status string

				if isDryRun || gateway == nil {
					// Simulate
					txHash = "0xSIMULATED_" + i.ID
					status = "simulated"
					log.Println(">>> Dry Run: Simulated Tx Execution")
					// Record Success to Risk Manager
					riskMgr.RecordSuccess()
				} else {
					// Real Tx (Commented out to avoid accidental use of dummy key on real RPC)
					// res, err := gateway.Send(context.Background(), i)
					// if err != nil {
					//    log.Printf("Tx Failed: %v", err)
					//    riskMgr.RecordFailure()
					// } else {
					//    txHash = res.Hash.Hex()
					//    riskMgr.RecordSuccess()
					// }

					txHash = "0xMOCK_REAL_" + i.ID // Safety bypass
					status = "pending"
					riskMgr.RecordSuccess()
				}

				// 4. Record to DB
				record := &storage.TradeRecord{
					Time:         time.Now(),
					IntentID:     i.ID,
					Type:         string(i.Type),
					PoolID:       i.PoolID,
					TxHash:       txHash,
					Status:       status,
					PnL:          0, // Placeholder
					IsSimulation: isDryRun,
				}
				if err := store.SaveTrade(record); err != nil {
					log.Printf("Failed to save trade: %v", err)
				}
			}
		}
	}
}
