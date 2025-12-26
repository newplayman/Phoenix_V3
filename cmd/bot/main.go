package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"phoenix-v3/internal/api"
	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/contracts"
	"phoenix-v3/internal/control/filecontrol"
	"phoenix-v3/internal/dexstate"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/events"
	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/monitor"
	"phoenix-v3/internal/obs/v1jsonl"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
	contractv1 "shared/contracts/contract/v1"
)

const (
	contractV1MetaIntentID  = "contract_v1_intent_id"
	contractV1MetaRunID     = "contract_v1_run_id"
	contractV1MetaTsLocalMS = "contract_v1_ts_local_ms"
	contractV1MetaTTLMS     = "contract_v1_ttl_ms"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runID := fmt.Sprintf("phoenix-%d", time.Now().UnixNano())
	v1w := v1jsonl.NewWriter("var/contract_v1.jsonl")
	writeV1 := func(typ string, data any) {
		if err := v1w.WriteEvent(typ, data); err != nil {
			log.Printf("[v1jsonl] write failed: %v", err)
		}
	}
	ctrlLoader := filecontrol.NewLoader("var/control.json")
	controlState := filecontrol.Default()
	var controlStateValue atomic.Value
	controlStateValue.Store(controlState)

	// 1. Load Configuration & watch for hot reload
	configPath := os.Getenv("PHOENIX_CONFIG")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	cfgManager, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.Current()
	log.Printf("Phoenix V3 Config Loaded. Chains: %d, Pools: %d", len(cfg.Chains), len(cfg.Pools))

	var cfgValue atomic.Value
	cfgValue.Store(cfg)

	// Safety: allow forcing dry-run via env (never auto-enable real broadcasts from this patch).
	if os.Getenv("PHOENIX_FORCE_DRY_RUN") == "1" && !cfg.Strategy.DryRun {
		cfg.Strategy.DryRun = true
		cfgValue.Store(cfg)
		log.Printf("[Safety] PHOENIX_FORCE_DRY_RUN=1 -> strategy.dry_run=true")
	}
	// Safety: when auto-eval is enabled, keep dry-run unless explicitly allowed.
	if os.Getenv("PHOENIX_AUTO_EVAL") == "1" && os.Getenv("PHOENIX_ALLOW_LIVE") != "1" && !cfg.Strategy.DryRun {
		cfg.Strategy.DryRun = true
		cfgValue.Store(cfg)
		log.Printf("[Safety] PHOENIX_AUTO_EVAL=1 -> forcing dry_run=true (set PHOENIX_ALLOW_LIVE=1 to override)")
	}

	// 2. Initialize Monitor
	monitorService := monitor.NewMonitor(cfg.Monitoring)
	go monitorService.Start()

	// Create price cache to store real prices from Binance
	var currentPrice float64 = 2005.0 // Default fallback price
	var currentDexPrice float64 = currentPrice
	var dexPriceReady atomic.Bool
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

	priceCfg := feed.LoadPriceAggregatorConfigFromEnv()
	priceAgg := feed.NewPriceAggregator(priceCfg)
	priceAgg.Start(ctx)
	defer priceAgg.Close()
	go func() {
		for t := range priceAgg.Output() {
			_ = eventStream.Publish(ctx, events.TopicTicker, t)
		}
	}()

	// Optional REST fallback (default off): legacy Binance/CoinGecko polling.
	if !priceCfg.WSOnly() {
		log.Printf("[Market] PRICE_MODE=%s (WS is primary; REST is explicit fallback)", priceCfg.PriceMode)
		binanceFeed := feed.NewBinanceFeed()
		binanceFeed.OnStatusUpdate(func(status feed.FeedStatus) {
			monitorService.UpdateFeedMetric(monitor.FeedMetric{
				Source:       status.Source,
				Healthy:      status.Healthy,
				DelayMs:      status.DelayMs,
				LastUpdateAt: status.LastUpdateAt,
			})
		})
		if tickerResult, err := binanceFeed.SubscribeTicker("ETHUSDT"); err == nil {
			isBinanceConnected = true
			log.Println("✅ Subscribed to Binance REST/WS fallback ETHUSDT")
			// Legacy aggregator channel already aggregates; feed into events stream directly.
			go func() {
				for t := range tickerResult {
					_ = eventStream.Publish(ctx, events.TopicTicker, t)
				}
			}()
		}
		coingeckoFeed := feed.NewCoinGeckoFeed(5 * time.Second)
		if cgChan, err := coingeckoFeed.SubscribeTicker("ETHUSDT"); err == nil {
			go func() {
				for t := range cgChan {
					_ = eventStream.Publish(ctx, events.TopicTicker, t)
				}
			}()
		}
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
	mockStrat := strategy.NewMockRebalanceStrategyFromEnv()
	v3Strat := strategy.NewV3RebalanceStrategy()
	v3Pos := strategy.NewV3PositionResolver()
	intentQueue := strategy.NewIntentQueue()

	// 7. Initialize Storage (Phase 5)
	store, err := storage.NewStore("phoenix.db")
	if err != nil {
		log.Fatalf("Failed to init db: %v", err)
	}

	// 8. Initialize Gateway (Phase 4)
	privKey := os.Getenv("BOT_PRIVATE_KEY")
	if privKey == "" && !cfg.Strategy.DryRun {
		log.Fatal("BOT_PRIVATE_KEY not set (set strategy.dry_run=true to run without broadcasting)")
	}

	// 9. Initialize API Server
	apiServer := api.NewServerWithConfig(intentQueue, api.ServerConfig{
		BinanceConnected: isBinanceConnected,
		PriceSource:      map[bool]string{true: "Binance", false: "Fallback"}[isBinanceConnected],
	})
	apiServer.SetContractV1RunID(runID)
	apiServer.SetMarketAggregator(priceAgg)

	// Initialize API with current price
	apiServer.UpdateCEXPrice(feed.Ticker{
		Symbol:    "ETHUSDT",
		Price:     currentPrice,
		Timestamp: time.Now(),
	})

	apiPort := os.Getenv("API_PORT")
	if apiPort == "" {
		apiPort = "8081"
	}
	apiServer.Start(apiPort)

	if err := quoterRuntimePreflight(ctx, cfg); err != nil {
		if os.Getenv("PHOENIX_SWAP_REQUIRE_QUOTER") == "1" {
			log.Fatalf("[Quoter] preflight failed (PHOENIX_SWAP_REQUIRE_QUOTER=1): %v", err)
		}
		log.Printf("[Quoter] preflight warning: %v", err)
	}

	var chainGateway *gateway.EthGateway
	if privKey == "" {
		log.Printf("[Gateway] BOT_PRIVATE_KEY not set; chain gateway disabled (dry_run=true)")
	} else {
		ethGw, err := gateway.NewEthGateway(cfg.Chains[0].RPC, privKey)
		if err != nil {
			log.Printf("[Gateway] Failed to init (Check RPC): %v", err)
		} else {
			chainGateway = ethGw
		}
	}
	if chainGateway != nil {
		go startPnLAndAlertLoop(ctx, apiServer, store, chainGateway, &cfgValue, &currentPrice, &currentDexPrice, &dexPriceReady)
	}

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

	go startIntentExecutor(ctx, &cfgValue, intentQueue, riskMgr, guard, chainGateway, store, eventStream, adapter, priceAgg, apiServer, v1w, &controlStateValue)
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

	decisionIntervalSec := 5
	if v := strings.TrimSpace(os.Getenv("DECISION_INTERVAL_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			decisionIntervalSec = n
		}
	}
	queryTicker := time.NewTicker(time.Duration(decisionIntervalSec) * time.Second)
	defer queryTicker.Stop()

	lastDecisionBlocked := false
	lastDecisionReason := ""
	lastDecisionRiskMode := ""
	var lastEvalAt time.Time
	lastEvalAction := ""
	lastEvalReason := ""
	lastIntentType := ""
	lastIntentSummary := ""
	lastIntentFields := map[string]any(nil)
	positionSource := ""
	positionLower := int64(0)
	positionUpper := int64(0)
	positionTokenID := uint64(0)
	var positionUpdatedAt time.Time

	// Mock position
	currentPos := engine.CurrentPosition{LowerTick: 200000, UpperTick: 202000, Liquidity: 1000}
	var currentPoolTick int64 = (currentPos.LowerTick + currentPos.UpperTick) / 2

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
				dexPriceReady.Store(true)
			}
			log.Printf("[DEX] Pool %s tick=%d liquidity=%s dexPrice=%.2f", state.PoolAddress, state.CurrentTick, state.Liquidity, currentDexPrice)
			currentPoolTick = state.CurrentTick
			currentPos = engine.CurrentPosition{
				LowerTick: state.CurrentTick - 200,
				UpperTick: state.CurrentTick + 200,
				Liquidity: liq,
			}

		case <-queryTicker.C:
			now := time.Now()

			if next, changed, err := ctrlLoader.LoadIfChanged(now); err != nil {
				log.Printf("[Control] load failed: %v", err)
			} else if changed {
				controlStateValue.Store(next)
				controlState = next
				reasons := []string{"control_changed"}
				if next.Reason != "" {
					reasons = append(reasons, "reason="+next.Reason)
				}
				if next.DesiredState != "" {
					reasons = append(reasons, "desired_state="+next.DesiredState)
				}
				if next.RiskMode != "" {
					reasons = append(reasons, "risk_mode="+next.RiskMode)
				}
				if next.ForceDryRun {
					reasons = append(reasons, "force_dry_run=true")
				}
				level := contractv1.RiskLevelSafe
				switch strings.TrimSpace(next.RiskMode) {
				case "DENY":
					level = contractv1.RiskLevelDeny
				case "PAUSE":
					level = contractv1.RiskLevelPause
				case "SAFE_MODE":
					level = contractv1.RiskLevelSafeMode
				case "HALT":
					level = contractv1.RiskLevelHalt
				}
				if strings.TrimSpace(next.DesiredState) == "PAUSED" {
					level = contractv1.RiskLevelPause
				}
				if strings.TrimSpace(next.DesiredState) == "SAFE_MODE" {
					level = contractv1.RiskLevelSafeMode
				}
				ev := contractv1.RiskDecisionV1{
					SchemaVersion: contractv1.SchemaVersion,
					RunID:         runID,
					TsLocalMS:     now.UnixMilli(),
					Level:         level,
					Reasons:       reasons,
					Fields:        map[string]string{"source": "control.json"},
					CooldownMS:    0,
				}
				apiServer.UpdateLastRiskV1(ev)
				writeV1("RiskDecisionV1", ev)
				log.Printf("[Control] applied desired_state=%s force_dry_run=%v risk_mode=%s", next.DesiredState, next.ForceDryRun, next.RiskMode)
			} else {
				controlState = next
			}

			manualOnly := os.Getenv("PHOENIX_MANUAL_ONLY") == "1"
			autoEvalEnabled := os.Getenv("PHOENIX_AUTO_EVAL") == "1" && !manualOnly

			// Gate is driven by market risk.mode (not only stale/freeze age).
			snap := priceAgg.Snapshot()
			riskMode := strings.ToLower(strings.TrimSpace(snap.Risk.Mode))
			gateReason := strings.TrimSpace(snap.Risk.Reason)
			staleAgeMs := snap.Aggregate.StaleAgeMs

			modeV1 := contractv1.ModeLive
			currentCfg := cfgValue.Load().(*config.AppConfig)
			if currentCfg.Strategy.DryRun {
				modeV1 = contractv1.ModeDryRun
			}
			apiServer.SetContractV1Mode(modeV1)

			decisionBlocked := manualOnly || riskMode != "normal"
			blockReason := ""
			if manualOnly {
				blockReason = "manual_only"
			} else if riskMode != "normal" {
				blockReason = gateReason
			}

			riskLevelV1 := contractv1.RiskLevelSafe
			reasonsV1 := make([]string, 0, 2)
			fieldsV1 := make(map[string]string, 4)
			if manualOnly {
				riskLevelV1 = contractv1.RiskLevelPause
				reasonsV1 = append(reasonsV1, "manual_only")
				fieldsV1["block_reason"] = "manual_only"
			} else if riskMode != "normal" {
				reasonsV1 = append(reasonsV1, fmt.Sprintf("market_risk_mode=%s reason=%s stale_age_ms=%d", riskMode, gateReason, staleAgeMs))
				fieldsV1["risk_mode"] = riskMode
				fieldsV1["gate_reason"] = gateReason
				fieldsV1["stale_age_ms"] = fmt.Sprintf("%d", staleAgeMs)
				switch riskMode {
				case "degraded":
					riskLevelV1 = contractv1.RiskLevelPause
				case "frozen":
					riskLevelV1 = contractv1.RiskLevelSafeMode
				default:
					riskLevelV1 = contractv1.RiskLevelDeny
				}
			}
			riskDecisionV1 := contractv1.RiskDecisionV1{
				SchemaVersion: contractv1.SchemaVersion,
				RunID:         runID,
				TsLocalMS:     now.UnixMilli(),
				Level:         riskLevelV1,
				Reasons:       reasonsV1,
				Fields:        fieldsV1,
				CooldownMS:    0,
			}
			apiServer.UpdateLastRiskV1(riskDecisionV1)
			writeV1("RiskDecisionV1", riskDecisionV1)
			if riskDecisionV1.Level != contractv1.RiskLevelSafe || autoEvalEnabled {
				if b, err := json.Marshal(riskDecisionV1); err == nil {
					log.Printf("[ContractV1] %s", string(b))
				}
			}

			// Keep /api/status decision fields up-to-date even when auto-eval is disabled.
			apiServer.UpdateDecision(api.DecisionStatus{
				LastEvalAt:        lastEvalAt,
				LastEvalAction:    lastEvalAction,
				LastEvalReason:    lastEvalReason,
				LastIntentType:    lastIntentType,
				LastIntentSummary: lastIntentSummary,
				LastIntentFields:  lastIntentFields,
				PositionSource:    positionSource,
				PositionLower:     positionLower,
				PositionUpper:     positionUpper,
				PositionTokenID:   positionTokenID,
				PositionUpdatedAt: positionUpdatedAt,
			})

			if decisionBlocked {
				// Log on transitions or when auto-eval would have executed.
				if decisionBlocked != lastDecisionBlocked || blockReason != lastDecisionReason || riskMode != lastDecisionRiskMode || autoEvalEnabled {
					lastDecisionBlocked = decisionBlocked
					lastDecisionReason = blockReason
					lastDecisionRiskMode = riskMode
					log.Printf("[Decision] blocked reason=%s risk_mode=%s stale_age_ms=%d", blockReason, riskMode, staleAgeMs)
				}
				lastEvalAt = time.Now()
				lastEvalAction = "blocked"
				lastEvalReason = blockReason
				apiServer.UpdateDecision(api.DecisionStatus{
					LastEvalAt:        lastEvalAt,
					LastEvalAction:    lastEvalAction,
					LastEvalReason:    lastEvalReason,
					LastIntentType:    lastIntentType,
					LastIntentSummary: lastIntentSummary,
					LastIntentFields:  lastIntentFields,
					PositionSource:    positionSource,
					PositionLower:     positionLower,
					PositionUpper:     positionUpper,
					PositionTokenID:   positionTokenID,
					PositionUpdatedAt: positionUpdatedAt,
				})
				continue
			}

			// Control gate (file-based) has priority and is purely additive.
			if strings.TrimSpace(controlState.RiskMode) != "" && strings.TrimSpace(controlState.RiskMode) != "SAFE" {
				continue
			}
			if strings.TrimSpace(controlState.DesiredState) == "PAUSED" || strings.TrimSpace(controlState.DesiredState) == "SAFE_MODE" {
				continue
			}

			// If auto-eval is disabled, don't generate intents.
			if !autoEvalEnabled {
				continue
			}

			var intents []contracts.Intent
			action := "noop"
			reason := "noop"

			v3Cfg := strategy.LoadV3RebalanceConfig(currentCfg)
			if v3Cfg.Enabled {
				posState := v3Pos.Resolve(ctx, time.Now(), v3Cfg)
				positionSource = string(posState.Source)
				positionLower = posState.LowerTick
				positionUpper = posState.UpperTick
				positionTokenID = posState.TokenID
				positionUpdatedAt = posState.UpdatedAt

				res, intent := v3Strat.EvaluateAt(v3Cfg, time.Now(), strategy.V3RebalanceInput{
					ObservedAt:       time.Now(),
					PoolTick:         currentPoolTick,
					CurrentLowerTick: posState.LowerTick,
					CurrentUpperTick: posState.UpperTick,
					AggPrice:         snap.Aggregate.AggPrice,
					DivergencePct:    snap.Aggregate.DivergencePct,
					RiskMode:         snap.Risk.Mode,
					RiskReason:       snap.Risk.Reason,
					StaleAgeMs:       snap.Aggregate.StaleAgeMs,
				})
				action = res.Action
				reason = res.Reason
				if intent != nil {
					intentNow := time.Now()
					ttlMS := int64(120_000)
					lastIntentType = string(intent.Type)
					lastIntentSummary = fmt.Sprintf("%s cur=[%d,%d] tick=%d new=[%d,%d]", reason, res.CurLower, res.CurUpper, res.CurrentTick, res.NewLower, res.NewUpper)
					lastIntentFields = map[string]any{
						"type":                   string(intent.Type),
						"reason":                 reason,
						"current_tick":           res.CurrentTick,
						"current_lower":          res.CurLower,
						"current_upper":          res.CurUpper,
						"new_lower":              res.NewLower,
						"new_upper":              res.NewUpper,
						"new_center_tick":        res.NewCenter,
						"width_ticks":            res.WidthTicks,
						"edge_buffer_ticks":      res.BufferTicks,
						"cooldown_remaining_sec": res.CooldownLeft,
					}

					dryRunV1 := currentCfg.Strategy.DryRun || intent.Type == contracts.IntentRebalanceV3
					intentV1 := strategy.ToIntentV1FromRebalance(*intent, runID, intentNow, dryRunV1, ttlMS)
					if intent.Metadata == nil {
						intent.Metadata = make(map[string]string, 8)
					}
					intent.Metadata[contractV1MetaIntentID] = intentV1.IntentID
					intent.Metadata[contractV1MetaRunID] = runID
					intent.Metadata[contractV1MetaTsLocalMS] = fmt.Sprintf("%d", intentV1.TsLocalMS)
					intent.Metadata[contractV1MetaTTLMS] = fmt.Sprintf("%d", intentV1.TTLms)

					intents = append(intents, *intent)

					apiServer.UpdateLastIntentV1(intentV1)
					writeV1("IntentV1", intentV1)
					if b, err := json.Marshal(intentV1); err == nil {
						log.Printf("[ContractV1] %s", string(b))
					}
					if st := apiServer.ContractV1StatusSnapshot(); st != nil {
						writeV1("StatusV1", *st)
						if b, err := json.Marshal(st); err == nil {
							log.Printf("[ContractV1] %s", string(b))
						}
					}
				} else {
					lastIntentType = ""
					lastIntentSummary = ""
					lastIntentFields = nil
				}

				log.Printf("[StrategyV3] eval action=%s reason=%s position_source=%s current_tick=%d current_range=[%d,%d] new_range=[%d,%d]",
					action, reason, positionSource, res.CurrentTick, res.CurLower, res.CurUpper, res.NewLower, res.NewUpper)
			} else {
				positionSource = ""
				positionLower = 0
				positionUpper = 0
				positionTokenID = 0
				positionUpdatedAt = time.Time{}

				action, reason, intents = mockStrat.EvaluateMock(strategy.MockRebalanceInput{
					AggPrice:      snap.Aggregate.AggPrice,
					DivergencePct: snap.Aggregate.DivergencePct,
					RiskMode:      snap.Risk.Mode,
					RiskReason:    snap.Risk.Reason,
					StaleAgeMs:    snap.Aggregate.StaleAgeMs,
				})
				if len(intents) > 0 {
					lastIntentType = string(intents[0].Type)
					lastIntentSummary = reason
					lastIntentFields = map[string]any{"type": lastIntentType, "reason": reason}
				} else {
					lastIntentType = ""
					lastIntentSummary = ""
					lastIntentFields = nil
				}
			}

			lastEvalAt = time.Now()
			lastEvalAction = action
			lastEvalReason = reason
			apiServer.UpdateDecision(api.DecisionStatus{
				LastEvalAt:        lastEvalAt,
				LastEvalAction:    lastEvalAction,
				LastEvalReason:    lastEvalReason,
				LastIntentType:    lastIntentType,
				LastIntentSummary: lastIntentSummary,
				LastIntentFields:  lastIntentFields,
				PositionSource:    positionSource,
				PositionLower:     positionLower,
				PositionUpper:     positionUpper,
				PositionTokenID:   positionTokenID,
				PositionUpdatedAt: positionUpdatedAt,
			})

			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				writeV1("StatusV1", *st)
			}

			log.Printf("[Strategy] eval action=%s reason=%s intents=%d agg_price=%.6f div_pct=%.6f", action, reason, len(intents), snap.Aggregate.AggPrice, snap.Aggregate.DivergencePct)

			for _, i := range intents {
				intentQueue.Enqueue(i)
			}
		}
	}
}

type pnlBaselineFile struct {
	UpdatedAt string             `json:"updated_at"`
	Baselines map[string]float64 `json:"baselines"`
}

func startPnLAndAlertLoop(
	ctx context.Context,
	apiServer *api.Server,
	store *storage.Store,
	ethGw *gateway.EthGateway,
	cfgValue *atomic.Value,
	ethPricePtr *float64,
	dexPricePtr *float64,
	dexPriceReady *atomic.Bool,
) {
	if apiServer == nil || store == nil || ethGw == nil || cfgValue == nil {
		return
	}
	maxDailyGasUSD := parseEnvFloat("PHOENIX_ALERT_MAX_DAILY_GAS_USD", 5.0)
	maxDailyLossUSD := parseEnvFloat("PHOENIX_ALERT_MAX_DAILY_LOSS_USD", 5.0)
	webhookURL := strings.TrimSpace(os.Getenv("PHOENIX_ALERT_WEBHOOK_URL"))

	lastSent := map[string]time.Time{}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now().UTC()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		last24h := now.Add(-24 * time.Hour)

		trades24h, _ := store.GetTradesSince(last24h)
		tradesDay, _ := store.GetTradesSince(startOfDay)
		allRecent, _ := store.GetRecentTrades(2000)

		ethPrice := 0.0
		if ethPricePtr != nil {
			ethPrice = *ethPricePtr
		}
		dexPrice := 0.0
		if dexPricePtr != nil && dexPriceReady != nil && dexPriceReady.Load() {
			dexPrice = *dexPricePtr
		}

		totalGasUSD := sumGasUSD(allRecent, ethPrice)
		dailyGasUSD := sumGasUSD(tradesDay, ethPrice)

		currentCfg, _ := cfgValue.Load().(*config.AppConfig)
		portfolioUSD := computePortfolioUSD(ctx, ethGw, currentCfg, ethPrice, dexPrice)
		dailyBaselineUSD := getOrSetBaselineUSD(currentCfg, ethGw.Address(), now.Format("2006-01-02"), portfolioUSD)
		totalBaselineUSD := getOrSetBaselineUSD(currentCfg, ethGw.Address(), "ALL", portfolioUSD)

		dailyPnLUSD := (portfolioUSD - dailyBaselineUSD) - dailyGasUSD
		totalPnLUSD := (portfolioUSD - totalBaselineUSD) - totalGasUSD

		snapshot := api.PnLSnapshot{
			UpdatedAt:        now,
			PortfolioUSD:     portfolioUSD,
			BaselineUSD:      dailyBaselineUSD,
			TotalBaselineUSD: totalBaselineUSD,
			DailyGasUSD:      dailyGasUSD,
			TotalGasUSD:      totalGasUSD,
			DailyPnLUSD:      dailyPnLUSD,
			TotalPnLUSD:      totalPnLUSD,
			TradesCount24h:   len(trades24h),
		}
		apiServer.UpdatePnL(snapshot)

		var alerts []api.Alert
		if maxDailyGasUSD > 0 && dailyGasUSD > maxDailyGasUSD {
			alerts = append(alerts, api.Alert{
				Key:       "daily_gas_exceeded",
				Severity:  "warn",
				Message:   fmt.Sprintf("daily gas cost exceeded: %.4f USD > %.4f USD", dailyGasUSD, maxDailyGasUSD),
				CreatedAt: now,
			})
		}
		if maxDailyLossUSD > 0 && dailyPnLUSD < -maxDailyLossUSD {
			alerts = append(alerts, api.Alert{
				Key:       "daily_loss_exceeded",
				Severity:  "critical",
				Message:   fmt.Sprintf("daily PnL below limit: %.4f USD < -%.4f USD", dailyPnLUSD, maxDailyLossUSD),
				CreatedAt: now,
			})
		}
		apiServer.UpdateAlerts(alerts)

		for _, a := range alerts {
			if webhookURL == "" {
				continue
			}
			if t, ok := lastSent[a.Key]; ok && now.Sub(t) < 10*time.Minute {
				continue
			}
			if err := postWebhookAlert(webhookURL, a); err != nil {
				log.Printf("[Alert] webhook post failed: %v", err)
				continue
			}
			lastSent[a.Key] = now
		}
	}
}

func sumGasUSD(trades []storage.TradeRecord, ethPrice float64) float64 {
	if len(trades) == 0 || ethPrice <= 0 {
		return 0
	}
	total := 0.0
	for _, tr := range trades {
		if tr.MetaJSON == "" {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(tr.MetaJSON), &meta); err != nil {
			continue
		}
		cw, ok := meta["gas_cost_wei"]
		if !ok {
			continue
		}
		s, ok := cw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		wei := new(big.Int)
		if _, ok := wei.SetString(s, 10); !ok {
			continue
		}
		if wei.Sign() <= 0 {
			continue
		}
		// usd = wei / 1e18 * ethPrice
		fwei := new(big.Float).SetInt(wei)
		feth := new(big.Float).Quo(fwei, big.NewFloat(1e18))
		fusd := new(big.Float).Mul(feth, big.NewFloat(ethPrice))
		v, _ := fusd.Float64()
		total += v
	}
	return total
}

func parseEnvFloat(key string, def float64) float64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

func postWebhookAlert(url string, alert api.Alert) error {
	body, _ := json.Marshal(map[string]any{
		"type":  "phoenix_alert",
		"alert": alert,
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %s", resp.Status)
	}
	return nil
}

func computePortfolioUSD(parent context.Context, ethGw *gateway.EthGateway, cfg *config.AppConfig, ethUSD float64, dexPriceToken0PerToken1 float64) float64 {
	if ethGw == nil || cfg == nil || len(cfg.Pools) == 0 || ethUSD <= 0 {
		return 0
	}
	pool := cfg.Pools[0]
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()

	nativeWei, err := ethGw.BalanceOfNative(ctx)
	if err != nil {
		return 0
	}
	totalUSD := weiToETH(nativeWei) * ethUSD

	priceOverrides := parsePriceOverrides(os.Getenv("PHOENIX_PRICE_USD_OVERRIDES"))
	stables := parseAddressSet(os.Getenv("PHOENIX_STABLE_TOKENS"))

	// Default: if no stable list is configured and token decimals look like a stablecoin, assume $1 for token1.
	if stables == nil && pool.Token1Decimals == 6 && common.IsHexAddress(pool.Token1) {
		stables = map[string]struct{}{common.HexToAddress(pool.Token1).Hex(): {}}
	}

	token1Hex := common.HexToAddress(pool.Token1).Hex()
	token0Hex := common.HexToAddress(pool.Token0).Hex()
	token1USD := tokenUSDPrice(token1Hex, stables, priceOverrides)
	token0USD := tokenUSDPrice(token0Hex, stables, priceOverrides)
	if token0USD == 0 && token1USD > 0 && dexPriceToken0PerToken1 > 0 {
		token0USD = token1USD / dexPriceToken0PerToken1
	}

	if common.IsHexAddress(pool.Token0) && pool.Token0Decimals > 0 && token0USD > 0 {
		if b0, err := ethGw.BalanceOfERC20(ctx, common.HexToAddress(pool.Token0)); err == nil {
			totalUSD += unitsToFloat(b0, pool.Token0Decimals) * token0USD
		}
	}
	if common.IsHexAddress(pool.Token1) && pool.Token1Decimals > 0 && token1USD > 0 {
		if b1, err := ethGw.BalanceOfERC20(ctx, common.HexToAddress(pool.Token1)); err == nil {
			totalUSD += unitsToFloat(b1, pool.Token1Decimals) * token1USD
		}
	}
	return totalUSD
}

func getOrSetBaselineUSD(cfg *config.AppConfig, wallet string, label string, currentUSD float64) float64 {
	if cfg == nil || wallet == "" || currentUSD <= 0 {
		return 0
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = time.Now().UTC().Format("2006-01-02")
	}
	key := baselineKey(cfg, wallet, label)

	if os.Getenv("PHOENIX_PNL_RESET_BASELINE") == "1" {
		return persistBaselineUSD(cfg, wallet, label, key, currentUSD, true)
	}

	path := strings.TrimSpace(os.Getenv("PHOENIX_PNL_BASELINE_FILE"))
	if path == "" {
		path = filepath.Join(os.Getenv("HOME"), ".config", "phoenix", "pnl_baseline.json")
	}

	state := pnlBaselineFile{Baselines: map[string]float64{}}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &state)
		if state.Baselines == nil {
			state.Baselines = map[string]float64{}
		}
	}
	if v, ok := state.Baselines[key]; ok && v > 0 {
		return v
	}
	return persistBaselineUSD(cfg, wallet, label, key, currentUSD, false)
}

func baselineKey(cfg *config.AppConfig, wallet string, label string) string {
	chainID := int64(0)
	if cfg != nil && len(cfg.Chains) > 0 {
		chainID = cfg.Chains[0].ID
	}
	return fmt.Sprintf("%d:%s:%s", chainID, strings.ToLower(strings.TrimSpace(wallet)), label)
}

func persistBaselineUSD(cfg *config.AppConfig, wallet string, label string, key string, currentUSD float64, overwrite bool) float64 {
	path := strings.TrimSpace(os.Getenv("PHOENIX_PNL_BASELINE_FILE"))
	if path == "" {
		path = filepath.Join(os.Getenv("HOME"), ".config", "phoenix", "pnl_baseline.json")
	}

	state := pnlBaselineFile{Baselines: map[string]float64{}}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &state)
		if state.Baselines == nil {
			state.Baselines = map[string]float64{}
		}
	}
	if !overwrite {
		if v, ok := state.Baselines[key]; ok && v > 0 {
			return v
		}
	}
	state.Baselines[key] = currentUSD
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if out, err := json.MarshalIndent(state, "", "  "); err == nil {
		_ = os.WriteFile(path, out, 0o600)
	}
	return currentUSD
}

func parseAddressSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	m := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		if !common.IsHexAddress(s) {
			continue
		}
		m[common.HexToAddress(s).Hex()] = struct{}{}
	}
	return m
}

func parsePriceOverrides(raw string) map[string]float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	m := make(map[string]float64)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		addr := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if !common.IsHexAddress(addr) {
			continue
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || f <= 0 {
			continue
		}
		m[common.HexToAddress(addr).Hex()] = f
	}
	return m
}

func tokenUSDPrice(token string, stables map[string]struct{}, overrides map[string]float64) float64 {
	if overrides != nil {
		if v, ok := overrides[token]; ok && v > 0 {
			return v
		}
	}
	if stables != nil {
		if _, ok := stables[token]; ok {
			return 1.0
		}
	}
	return 0
}

func weiToETH(wei *big.Int) float64 {
	if wei == nil || wei.Sign() <= 0 {
		return 0
	}
	fwei := new(big.Float).SetInt(wei)
	feth := new(big.Float).Quo(fwei, big.NewFloat(1e18))
	v, _ := feth.Float64()
	return v
}

func unitsToFloat(units *big.Int, decimals int) float64 {
	if units == nil || units.Sign() == 0 {
		return 0
	}
	if decimals <= 0 {
		f, _ := new(big.Float).SetInt(units).Float64()
		return f
	}
	den := new(big.Float).SetFloat64(math.Pow10(decimals))
	num := new(big.Float).SetInt(units)
	out := new(big.Float).Quo(num, den)
	v, _ := out.Float64()
	return v
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

func startIntentExecutor(ctx context.Context, cfgValue *atomic.Value, queue *strategy.IntentQueue, riskMgr *risk.Manager, guard *poolguard.Guard, gw gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter, market *feed.PriceAggregator, apiServer *api.Server, v1w *v1jsonl.Writer, controlValue *atomic.Value) {
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

			executeIntent(ctx, cfgValue, intent, riskMgr, guard, gw, store, stream, adapter, market, apiServer, v1w, controlValue)
		}
	}()
}

func executeIntent(ctx context.Context, cfgValue *atomic.Value, intent strategy.Intent, riskMgr *risk.Manager, guard *poolguard.Guard, gw gateway.Gateway, store *storage.Store, stream events.Stream, adapter *univ3.Adapter, market *feed.PriceAggregator, apiServer *api.Server, v1w *v1jsonl.Writer, controlValue *atomic.Value) {
	if intent.Metadata == nil {
		intent.Metadata = make(map[string]string)
	}

	v1IntentID := strings.TrimSpace(intent.Metadata[contractV1MetaIntentID])
	if v1IntentID == "" {
		v1IntentID = intent.ID
	}
	v1RunID := strings.TrimSpace(intent.Metadata[contractV1MetaRunID])
	v1TsMS := parseMetadataInt64(intent.Metadata, contractV1MetaTsLocalMS)
	v1TTLMS := parseMetadataInt64(intent.Metadata, contractV1MetaTTLMS)
	nowMS := time.Now().UnixMilli()

	ctrl := filecontrol.Default()
	if controlValue != nil {
		if v := controlValue.Load(); v != nil {
			if cs, ok := v.(filecontrol.ControlState); ok {
				ctrl = cs
			}
		}
	}
	ctrlDesired := strings.TrimSpace(ctrl.DesiredState)
	ctrlRiskMode := strings.TrimSpace(ctrl.RiskMode)
	ctrlForceDry := ctrl.ForceDryRun

	if ctrlRiskMode != "" && ctrlRiskMode != "SAFE" {
		level := contractv1.RiskLevelDeny
		switch ctrlRiskMode {
		case "PAUSE":
			level = contractv1.RiskLevelPause
		case "SAFE_MODE":
			level = contractv1.RiskLevelSafeMode
		case "HALT":
			level = contractv1.RiskLevelHalt
		case "DENY":
			level = contractv1.RiskLevelDeny
		}
		riskV1 := contractv1.RiskDecisionV1{
			SchemaVersion: contractv1.SchemaVersion,
			RunID:         v1RunID,
			TsLocalMS:     nowMS,
			Level:         level,
			Reasons:       []string{"control_risk_mode_override", "risk_mode=" + ctrlRiskMode},
			Fields:        map[string]string{"source": "control.json", "reason": ctrl.Reason},
			CooldownMS:    0,
		}
		if apiServer != nil {
			apiServer.UpdateLastRiskV1(riskV1)
		}
		writeV1("RiskDecisionV1", riskV1)

		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  "control_risk_mode_override",
				"risk_mode":       ctrlRiskMode,
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		writeV1("ExecutorResultV1", execV1)
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				writeV1("StatusV1", *st)
			}
		}
		return
	}

	if ctrlDesired == "PAUSED" || ctrlDesired == "SAFE_MODE" {
		level := contractv1.RiskLevelPause
		skippedReason := "control_paused"
		if ctrlDesired == "SAFE_MODE" {
			level = contractv1.RiskLevelSafeMode
			skippedReason = "control_safe_mode"
		}
		riskV1 := contractv1.RiskDecisionV1{
			SchemaVersion: contractv1.SchemaVersion,
			RunID:         v1RunID,
			TsLocalMS:     nowMS,
			Level:         level,
			Reasons:       []string{"control_desired_state", "desired_state=" + ctrlDesired},
			Fields:        map[string]string{"source": "control.json", "reason": ctrl.Reason},
			CooldownMS:    0,
		}
		if apiServer != nil {
			apiServer.UpdateLastRiskV1(riskV1)
		}
		writeV1("RiskDecisionV1", riskV1)

		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  skippedReason,
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		writeV1("ExecutorResultV1", execV1)
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				writeV1("StatusV1", *st)
			}
		}
		return
	}

	if v1TTLMS > 0 && v1TsMS > 0 && nowMS-v1TsMS > v1TTLMS {
		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  "intent_expired",
				"ts_local_ms":     v1TsMS,
				"ttl_ms":          v1TTLMS,
				"now_ms":          nowMS,
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		writeV1("ExecutorResultV1", execV1)
		if b, err := json.Marshal(execV1); err == nil {
			log.Printf("[ContractV1] %s", string(b))
		}
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				writeV1("StatusV1", *st)
				if b, err := json.Marshal(st); err == nil {
					log.Printf("[ContractV1] %s", string(b))
				}
			}
		}
		_ = v1RunID
		return
	}
	if market != nil {
		snap := market.Snapshot()
		riskMode := strings.ToLower(strings.TrimSpace(snap.Risk.Mode))
		if riskMode != "normal" {
			log.Printf("[Decision] blocked reason=%s risk_mode=%s stale_age_ms=%d", snap.Risk.Reason, riskMode, snap.Aggregate.StaleAgeMs)
			level := contractv1.RiskLevelDeny
			switch riskMode {
			case "degraded":
				level = contractv1.RiskLevelPause
			case "frozen":
				level = contractv1.RiskLevelSafeMode
			}
			riskV1 := contractv1.RiskDecisionV1{
				SchemaVersion: contractv1.SchemaVersion,
				RunID:         v1RunID,
				TsLocalMS:     nowMS,
				Level:         level,
				Reasons:       []string{fmt.Sprintf("market_risk_mode=%s reason=%s", riskMode, strings.TrimSpace(snap.Risk.Reason))},
				Fields: map[string]string{
					"risk_mode":    riskMode,
					"gate_reason":  strings.TrimSpace(snap.Risk.Reason),
					"stale_age_ms": fmt.Sprintf("%d", snap.Aggregate.StaleAgeMs),
				},
				CooldownMS: 0,
			}
			if apiServer != nil {
				apiServer.UpdateLastRiskV1(riskV1)
			}
			writeV1("RiskDecisionV1", riskV1)
			if b, err := json.Marshal(riskV1); err == nil {
				log.Printf("[ContractV1] %s", string(b))
			}

			execV1 := contractv1.ExecutorResultV1{
				SchemaVersion: contractv1.SchemaVersion,
				IntentID:      v1IntentID,
				TsLocalMS:     nowMS,
				Status:        contractv1.ExecutionStatusSkipped,
				ErrorKind:     contractv1.ErrorKindNone,
				Receipt: map[string]any{
					"skipped_reason":  "risk_denied",
					"risk_mode":       riskMode,
					"gate_reason":     strings.TrimSpace(snap.Risk.Reason),
					"stale_age_ms":    snap.Aggregate.StaleAgeMs,
					"would_broadcast": false,
				},
			}
			if apiServer != nil {
				apiServer.UpdateLastExecV1(execV1)
			}
			writeV1("ExecutorResultV1", execV1)
			if b, err := json.Marshal(execV1); err == nil {
				log.Printf("[ContractV1] %s", string(b))
			}
			if apiServer != nil {
				if st := apiServer.ContractV1StatusSnapshot(); st != nil {
					writeV1("StatusV1", *st)
					if b, err := json.Marshal(st); err == nil {
						log.Printf("[ContractV1] %s", string(b))
					}
				}
			}
			return
		}
	}
	if err := riskMgr.CanProceed(); err != nil {
		log.Printf("[Risk] skip intent %s: %v", intent.ID, err)
		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  "risk_can_proceed_failed",
				"error":           err.Error(),
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		if b, err := json.Marshal(execV1); err == nil {
			log.Printf("[ContractV1] %s", string(b))
		}
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				if b, err := json.Marshal(st); err == nil {
					log.Printf("[ContractV1] %s", string(b))
				}
			}
		}
		return
	}

	check := guard.CheckPool(context.Background(), intent.PoolID, "0xTokenA", "0xTokenB")
	if check.Risk == poolguard.RiskDanger {
		log.Printf("[PoolGuard] block intent %s: %s", intent.ID, check.Reason)
		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  "poolguard_danger",
				"reason":          check.Reason,
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		if b, err := json.Marshal(execV1); err == nil {
			log.Printf("[ContractV1] %s", string(b))
		}
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				if b, err := json.Marshal(st); err == nil {
					log.Printf("[ContractV1] %s", string(b))
				}
			}
		}
		return
	}

	currentCfg := cfgValue.Load().(*config.AppConfig)
	isDryRun := currentCfg.Strategy.DryRun
	if ctrlForceDry {
		isDryRun = true
	}
	if intent.Type == contracts.IntentMockRebalance || intent.Type == contracts.IntentRebalanceV3 {
		// This intent is intentionally non-broadcasting.
		isDryRun = true
	}
	log.Printf("[IntentExecutor] executing %s dry_run=%v", intent.ID, isDryRun)

	var txHash string
	var status string

	if !isDryRun && intent.Type != contracts.IntentSwap && adapter == nil {
		log.Printf("[IntentExecutor] skip intent %s (non-swap broadcast disabled without adapter)", intent.ID)
		riskMgr.RecordFailure()
		execV1 := contractv1.ExecutorResultV1{
			SchemaVersion: contractv1.SchemaVersion,
			IntentID:      v1IntentID,
			TsLocalMS:     nowMS,
			Status:        contractv1.ExecutionStatusSkipped,
			ErrorKind:     contractv1.ErrorKindNone,
			Receipt: map[string]any{
				"skipped_reason":  "missing_adapter",
				"would_broadcast": false,
			},
		}
		if apiServer != nil {
			apiServer.UpdateLastExecV1(execV1)
		}
		if b, err := json.Marshal(execV1); err == nil {
			log.Printf("[ContractV1] %s", string(b))
		}
		if apiServer != nil {
			if st := apiServer.ContractV1StatusSnapshot(); st != nil {
				if b, err := json.Marshal(st); err == nil {
					log.Printf("[ContractV1] %s", string(b))
				}
			}
		}
		return
	}

	if err := maybePrepareSwapHelperCall(ctx, currentCfg, &intent); err != nil {
		log.Printf("[Swap] skip intent %s: %v", intent.ID, err)
		riskMgr.RecordFailure()
		return
	}

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
			if err := maybeEnsureSwapAllowance(ctx, currentCfg, ethGw, store, intent); err != nil {
				log.Printf("[Swap] allowance/approve failed for intent %s: %v", intent.ID, err)
				riskMgr.RecordFailure()
				return
			}
			if ok, reason := hasSufficientBalances(ctx, ethGw, intent.Metadata); !ok {
				log.Printf("[BalanceGuard] skip intent %s: %s", intent.ID, reason)
				riskMgr.RecordFailure()
				return
			}
		}
	}

	if isDryRun || gw == nil {
		txHash = "0xSIMULATED_" + v1IntentID
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

	execV1 := contractv1.ExecutorResultV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      v1IntentID,
		TsLocalMS:     time.Now().UnixMilli(),
		Status:        contractv1.ExecutionStatusSubmitted,
		ErrorKind:     contractv1.ErrorKindNone,
		Receipt: map[string]any{
			"tx_hash":          txHash,
			"simulated":        isDryRun || gw == nil,
			"would_broadcast":  !(isDryRun || gw == nil),
			"legacy_status":    status,
			"legacy_intent_id": intent.ID,
		},
	}
	if isDryRun || gw == nil {
		execV1.Status = contractv1.ExecutionStatusSimulated
		execV1.ErrorKind = contractv1.ErrorKindNone
	} else if status == "failed" {
		execV1.Status = contractv1.ExecutionStatusFailed
		execV1.ErrorKind = contractv1.ErrorKindUnknown
	}
	if apiServer != nil {
		apiServer.UpdateLastExecV1(execV1)
	}
	writeV1("ExecutorResultV1", execV1)
	if b, err := json.Marshal(execV1); err == nil {
		log.Printf("[ContractV1] %s", string(b))
	}
	if apiServer != nil {
		if st := apiServer.ContractV1StatusSnapshot(); st != nil {
			writeV1("StatusV1", *st)
			if b, err := json.Marshal(st); err == nil {
				log.Printf("[ContractV1] %s", string(b))
			}
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
		MetaJSON:        buildTradeMetaJSON(intent, txHash, status, isDryRun),
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

func maybeEnsureSwapAllowance(parent context.Context, cfg *config.AppConfig, ethGw *gateway.EthGateway, store *storage.Store, intent strategy.Intent) error {
	if cfg == nil || ethGw == nil {
		return nil
	}
	if intent.Type != contracts.IntentSwap && strings.ToLower(strings.TrimSpace(intent.Metadata["action"])) != "swap_exact_in" {
		return nil
	}
	tokenInAddr := strings.TrimSpace(intent.Metadata["swap_token_in"])
	amtInStr := strings.TrimSpace(intent.Metadata["swap_amount_in"])
	spender := strings.TrimSpace(intent.Metadata["swap_helper"])
	if spender == "" {
		spender = strings.TrimSpace(intent.Metadata["target"])
	}
	if tokenInAddr == "" || amtInStr == "" || spender == "" {
		return nil
	}
	if !common.IsHexAddress(tokenInAddr) || !common.IsHexAddress(spender) {
		return nil
	}
	amountIn := new(big.Int)
	if _, ok := amountIn.SetString(amtInStr, 10); !ok || amountIn.Sign() <= 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	token := common.HexToAddress(tokenInAddr)
	spenderAddr := common.HexToAddress(spender)
	allow, err := ethGw.AllowanceERC20(ctx, token, ethGw.WalletAddress(), spenderAddr)
	if err != nil {
		return err
	}
	if allow.Cmp(amountIn) >= 0 {
		return nil
	}

	maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	log.Printf("[Swap] allowance %s < amountIn %s; approving spender=%s", allow.String(), amountIn.String(), spenderAddr.Hex())

	// Some tokens require setting allowance to 0 before changing it.
	if allow.Sign() > 0 {
		tx0, err := ethGw.ApproveERC20(ctx, token, spenderAddr, big.NewInt(0))
		if err != nil {
			return err
		}
		rcpt0, err := ethGw.WaitMined(ctx, tx0.Hash)
		if err != nil {
			return err
		}
		if rcpt0.Status != 1 {
			return fmt.Errorf("approve(0) reverted: %s", tx0.Hash.Hex())
		}
		saveApproveTradeRecord(store, intent, token, spenderAddr, big.NewInt(0), tx0.Hash.Hex(), rcpt0.Status == 1)
	}

	tx, err := ethGw.ApproveERC20(ctx, token, spenderAddr, maxUint)
	if err != nil {
		return err
	}
	rcpt, err := ethGw.WaitMined(ctx, tx.Hash)
	if err != nil {
		return err
	}
	if rcpt.Status != 1 {
		return fmt.Errorf("approve(max) reverted: %s", tx.Hash.Hex())
	}
	log.Printf("[Swap] approve mined tx=%s", tx.Hash.Hex())
	saveApproveTradeRecord(store, intent, token, spenderAddr, maxUint, tx.Hash.Hex(), rcpt.Status == 1)
	return nil
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

func parseMetadataInt64(meta map[string]string, key string) int64 {
	if meta == nil {
		return 0
	}
	val, ok := meta[key]
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func buildTradeMetaJSON(intent strategy.Intent, txHash string, status string, isDryRun bool) string {
	meta := make(map[string]any, 32)
	meta["intent_id"] = intent.ID
	meta["intent_type"] = string(intent.Type)
	meta["pool_id"] = intent.PoolID
	meta["chain_id"] = intent.ChainID
	meta["tx_hash"] = txHash
	meta["status"] = status
	meta["is_simulation"] = isDryRun
	meta["captured_at"] = time.Now().UTC().Format(time.RFC3339Nano)

	// Curated, audit-useful fields only (avoid secrets; do not store BOT_PRIVATE_KEY or ADMIN_TOKEN).
	for _, k := range []string{
		"action",
		"target",
		"value",
		"recipient",
		"calldata",

		"swap_pool",
		"swap_token_in",
		"swap_token_out",
		"swap_fee",
		"swap_amount_in",
		"swap_slippage_bps",
		"swap_slippage_bps_effective",
		"swap_quote_method",
		"swap_quote_amount_out",
		"swap_amount_out_minimum",
		"swap_helper",

		"approve_tx_hash",
		"approve_amount",
		"approve_token",
		"approve_spender",
	} {
		if intent.Metadata != nil {
			if v := strings.TrimSpace(intent.Metadata[k]); v != "" {
				meta[k] = v
			}
		}
	}

	meta["manual_only"] = os.Getenv("PHOENIX_MANUAL_ONLY")
	meta["swap_require_quoter"] = os.Getenv("PHOENIX_SWAP_REQUIRE_QUOTER")
	meta["swap_force_confirm"] = os.Getenv("PHOENIX_SWAP_FORCE_CONFIRM")

	b, _ := json.Marshal(meta)
	return string(b)
}

func saveApproveTradeRecord(store *storage.Store, intent strategy.Intent, token common.Address, spender common.Address, amount *big.Int, txHash string, ok bool) {
	if store == nil || txHash == "" {
		return
	}
	status := "failed"
	if ok {
		status = "success"
	}
	meta := map[string]any{
		"action":          "approve",
		"approve_token":   token.Hex(),
		"approve_spender": spender.Hex(),
		"approve_amount": func() string {
			if amount == nil {
				return "0"
			}
			return amount.String()
		}(),
	}
	b, _ := json.Marshal(meta)
	_ = store.SaveTrade(&storage.TradeRecord{
		Time:            time.Now(),
		IntentID:        intent.ID,
		Type:            "approve",
		PoolID:          intent.PoolID,
		ChainID:         intent.ChainID,
		TxHash:          txHash,
		Status:          status,
		MetaJSON:        string(b),
		IsSimulation:    false,
		StrategyVersion: intent.StrategyVersion,
		RiskMode:        intent.RiskMode,
	})
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
			if err := store.UpdateTradeReceiptByHash(receipt.Hash.Hex(), string(receipt.Status), receipt.GasUsed, receipt.GasPriceWei); err != nil {
				log.Printf("[ReceiptWatcher] update %s failed: %v", receipt.Hash.Hex(), err)
			}
		}
	}
}

func hasSufficientBalances(ctx context.Context, gw *gateway.EthGateway, meta map[string]string) (bool, string) {
	// Swap guard: require token-in balance >= amountIn.
	if meta != nil {
		swapTokenIn := strings.TrimSpace(meta["swap_token_in"])
		swapAmountIn := strings.TrimSpace(meta["swap_amount_in"])
		if swapTokenIn != "" && swapAmountIn != "" && common.IsHexAddress(swapTokenIn) {
			required := new(big.Int)
			if _, ok := required.SetString(swapAmountIn, 10); ok && required.Sign() > 0 {
				bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(swapTokenIn))
				if err != nil {
					return false, fmt.Sprintf("balanceOf failed token=%s err=%v", common.HexToAddress(swapTokenIn).Hex(), err)
				}
				if bal.Cmp(required) < 0 {
					missing := new(big.Int).Sub(required, bal)
					return false, fmt.Sprintf(
						"insufficient swap tokenIn balance token=%s balance=%s required=%s missing=%s",
						common.HexToAddress(swapTokenIn).Hex(),
						bal.String(),
						required.String(),
						missing.String(),
					)
				}
			}
		}
	}

	token0 := meta["token0"]
	amount0 := meta["amount0"]
	if token0 != "" && amount0 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount0, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token0))
			if err != nil {
				return false, fmt.Sprintf("balanceOf failed token=%s err=%v", common.HexToAddress(token0).Hex(), err)
			}
			if bal.Cmp(required) < 0 {
				missing := new(big.Int).Sub(required, bal)
				return false, fmt.Sprintf(
					"insufficient token0 balance token=%s balance=%s required=%s missing=%s",
					common.HexToAddress(token0).Hex(),
					bal.String(),
					required.String(),
					missing.String(),
				)
			}
		}
	}
	token1 := meta["token1"]
	amount1 := meta["amount1"]
	if token1 != "" && amount1 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount1, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token1))
			if err != nil {
				return false, fmt.Sprintf("balanceOf failed token=%s err=%v", common.HexToAddress(token1).Hex(), err)
			}
			if bal.Cmp(required) < 0 {
				missing := new(big.Int).Sub(required, bal)
				return false, fmt.Sprintf(
					"insufficient token1 balance token=%s balance=%s required=%s missing=%s",
					common.HexToAddress(token1).Hex(),
					bal.String(),
					required.String(),
					missing.String(),
				)
			}
		}
	}
	return true, "ok"
}

type ethClientCaller struct {
	c *ethclient.Client
}

func (e ethClientCaller) Call(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	if e.c == nil {
		return nil, context.Canceled
	}
	msg := ethereum.CallMsg{To: &to, Data: data}
	return e.c.CallContract(ctx, msg, nil)
}

func quoterRuntimePreflight(parent context.Context, cfg *config.AppConfig) error {
	if cfg == nil || len(cfg.Chains) == 0 || len(cfg.Pools) == 0 {
		return nil
	}
	chain := cfg.Chains[0]
	pool := cfg.Pools[0]

	quoterAddr := strings.TrimSpace(chain.QuoterAddress)
	if quoterAddr == "" {
		quoterAddr = strings.TrimSpace(os.Getenv("ARBITRUM_SEPOLIA_QUOTER_ADDRESS"))
	}
	if quoterAddr == "" {
		return nil
	}
	if !common.IsHexAddress(quoterAddr) {
		return fmt.Errorf("invalid quoter address: %q", quoterAddr)
	}
	if chain.RPC == "" || !common.IsHexAddress(pool.Token0) || !common.IsHexAddress(pool.Token1) || pool.Fee <= 0 {
		return nil
	}

	amountIn := new(big.Int)
	if preflightAmt := strings.TrimSpace(os.Getenv("PHOENIX_QUOTER_PREFLIGHT_AMOUNT_IN")); preflightAmt != "" {
		amountIn.SetString(preflightAmt, 10)
	} else if pool.Amount0 != "" {
		// Prefer configured amount0; fall back to amount1.
		amountIn.SetString(pool.Amount0, 10)
	} else if pool.Amount1 != "" {
		amountIn.SetString(pool.Amount1, 10)
	}
	// If config amounts are unset/zero, use a non-trivial default to avoid rounding to zero.
	if amountIn.Sign() <= 0 {
		amountIn.SetInt64(1_000_000)
	}

	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	c, err := ethclient.DialContext(ctx, chain.RPC)
	if err != nil {
		return err
	}

	q, err := univ3.NewQuoter(quoterAddr)
	if err != nil {
		return err
	}
	code, err := c.CodeAt(ctx, q.Address, nil)
	if err != nil {
		return err
	}
	if len(code) == 0 {
		return fmt.Errorf("no contract code at quoter address: %s", q.Address.Hex())
	}

	tokenIn := common.HexToAddress(pool.Token0)
	tokenOut := common.HexToAddress(pool.Token1)
	fee := uint32(pool.Fee)
	caller := ethClientCaller{c: c}

	// Prefer single-params quote if available; otherwise fall back to path quote.
	if out, err := q.QuoteExactInputSingle(ctx, caller, tokenIn, tokenOut, fee, amountIn, big.NewInt(0)); err == nil && out != nil && out.Sign() > 0 {
		log.Printf("[Quoter] quoteExactInputSingle ok amountIn=%s amountOut=%s", amountIn.String(), out.String())
		return nil
	}
	out, err := q.QuoteExactInput(ctx, caller, tokenIn, tokenOut, fee, amountIn)
	if err != nil {
		return err
	}
	if out == nil || out.Sign() <= 0 {
		return fmt.Errorf("unexpected quote amountOut: %v", out)
	}
	log.Printf("[Quoter] quoteExactInput(path) ok amountIn=%s amountOut=%s", amountIn.String(), out.String())
	return nil
}

func maybePrepareSwapHelperCall(parent context.Context, cfg *config.AppConfig, intent *strategy.Intent) error {
	if cfg == nil || intent == nil {
		return nil
	}

	// Trigger conditions:
	// - intent.Type == swap
	// - OR intent.Metadata["action"] == "swap_exact_in"
	if intent.Type != contracts.IntentSwap && strings.ToLower(strings.TrimSpace(intent.Metadata["action"])) != "swap_exact_in" {
		return nil
	}
	if len(cfg.Chains) == 0 || len(cfg.Pools) == 0 {
		return fmt.Errorf("missing chain/pool config")
	}

	chain := cfg.Chains[0]
	poolCfg := cfg.Pools[0]

	if err := enforceSwapPolicy(cfg, intent); err != nil {
		return err
	}

	swapHelperAddr := strings.TrimSpace(chain.SwapHelperAddress)
	if swapHelperAddr == "" {
		swapHelperAddr = strings.TrimSpace(os.Getenv("PHOENIX_SWAP_HELPER_ADDRESS"))
	}
	if swapHelperAddr == "" {
		return fmt.Errorf("swap helper address not set (chains[0].swap_helper_address or PHOENIX_SWAP_HELPER_ADDRESS)")
	}
	if !common.IsHexAddress(swapHelperAddr) {
		return fmt.Errorf("invalid swap helper address: %q", swapHelperAddr)
	}

	quoterAddr := strings.TrimSpace(chain.QuoterAddress)
	if quoterAddr == "" {
		quoterAddr = strings.TrimSpace(os.Getenv("ARBITRUM_SEPOLIA_QUOTER_ADDRESS"))
	}
	requireQuoter := os.Getenv("PHOENIX_SWAP_REQUIRE_QUOTER") == "1"
	if requireQuoter && quoterAddr == "" {
		return fmt.Errorf("PHOENIX_SWAP_REQUIRE_QUOTER=1 but quoter address not set (chains[0].quoter_address or ARBITRUM_SEPOLIA_QUOTER_ADDRESS)")
	}

	swapPoolAddr := strings.TrimSpace(intent.Metadata["swap_pool"])
	if swapPoolAddr == "" {
		swapPoolAddr = strings.TrimSpace(poolCfg.Address)
	}
	if swapPoolAddr == "" || !common.IsHexAddress(swapPoolAddr) {
		return fmt.Errorf("swap_pool missing/invalid (metadata swap_pool or pools[0].address)")
	}

	tokenInAddr := strings.TrimSpace(intent.Metadata["swap_token_in"])
	tokenOutAddr := strings.TrimSpace(intent.Metadata["swap_token_out"])
	if tokenInAddr == "" || tokenOutAddr == "" || !common.IsHexAddress(tokenInAddr) || !common.IsHexAddress(tokenOutAddr) {
		return fmt.Errorf("swap_token_in/out missing/invalid")
	}

	amtInStr := strings.TrimSpace(intent.Metadata["swap_amount_in"])
	if amtInStr == "" {
		return fmt.Errorf("swap_amount_in missing")
	}
	amountIn := new(big.Int)
	if _, ok := amountIn.SetString(amtInStr, 10); !ok || amountIn.Sign() <= 0 {
		return fmt.Errorf("invalid swap_amount_in: %q", amtInStr)
	}

	fee := uint32(poolCfg.Fee)
	if fee == 0 {
		if feeStr := strings.TrimSpace(intent.Metadata["swap_fee"]); feeStr != "" {
			if v, err := strconv.ParseUint(feeStr, 10, 32); err == nil {
				fee = uint32(v)
			}
		}
	}
	if fee == 0 {
		return fmt.Errorf("swap fee missing (pools[0].fee or metadata swap_fee)")
	}

	slippageBps := uint32(100) // 1%
	if bpsStr := strings.TrimSpace(intent.Metadata["swap_slippage_bps"]); bpsStr != "" {
		if v, err := strconv.ParseUint(bpsStr, 10, 32); err == nil {
			slippageBps = uint32(v)
		}
	} else if envBps := strings.TrimSpace(os.Getenv("PHOENIX_SWAP_SLIPPAGE_BPS")); envBps != "" {
		if v, err := strconv.ParseUint(envBps, 10, 32); err == nil {
			slippageBps = uint32(v)
		}
	}

	amountOutMinimum := big.NewInt(0)
	if quoterAddr != "" && common.IsHexAddress(quoterAddr) {
		ctx, cancel := context.WithTimeout(parent, univ3.DefaultQuoterTimeout)
		defer cancel()
		c, err := ethclient.DialContext(ctx, chain.RPC)
		if err != nil {
			return fmt.Errorf("dial rpc for quoter: %w", err)
		}
		q, err := univ3.NewQuoter(quoterAddr)
		if err != nil {
			return err
		}
		out, method, err := q.QuoteExactInputWithFallback(
			ctx,
			ethClientCaller{c: c},
			common.HexToAddress(tokenInAddr),
			common.HexToAddress(tokenOutAddr),
			fee,
			amountIn,
		)
		if err != nil {
			if requireQuoter {
				return fmt.Errorf("quoter quote failed: %w", err)
			}
			log.Printf("[Swap] quoter quote failed (continuing with amountOutMinimum=0): %v", err)
		} else {
			minOut, err := univ3.ComputeMinOutBps(out, slippageBps)
			if err != nil {
				return err
			}
			amountOutMinimum = minOut
			intent.Metadata["swap_quote_method"] = method
			intent.Metadata["swap_quote_amount_out"] = out.String()
			intent.Metadata["swap_amount_out_minimum"] = minOut.String()
		}
	} else if requireQuoter {
		return fmt.Errorf("PHOENIX_SWAP_REQUIRE_QUOTER=1 but quoter address invalid: %q", quoterAddr)
	}

	swapHelper, err := univ3.NewSwapHelper(swapHelperAddr)
	if err != nil {
		return err
	}
	data, err := swapHelper.BuildSwapExactInputSingleMinOutData(
		common.HexToAddress(swapPoolAddr),
		common.HexToAddress(tokenInAddr),
		common.HexToAddress(tokenOutAddr),
		amountIn,
		amountOutMinimum,
		big.NewInt(0),
	)
	if err != nil {
		return err
	}

	intent.Metadata["target"] = swapHelper.Address.Hex()
	intent.Metadata["calldata"] = hex.EncodeToString(data)
	intent.Metadata["value"] = "0"
	intent.Metadata["swap_helper"] = swapHelper.Address.Hex()
	intent.Metadata["swap_slippage_bps_effective"] = fmt.Sprintf("%d", slippageBps)
	log.Printf(
		"[Swap] prepared swapExactInputSingleMinOut pool=%s tokenIn=%s tokenOut=%s amountIn=%s amountOutMinimum=%s helper=%s quoteMethod=%s",
		swapPoolAddr,
		tokenInAddr,
		tokenOutAddr,
		amountIn.String(),
		amountOutMinimum.String(),
		swapHelper.Address.Hex(),
		intent.Metadata["swap_quote_method"],
	)
	return nil
}

func parseAddressAllowlist(envKey string) map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return nil
	}
	m := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		s := strings.ToLower(strings.TrimSpace(part))
		if s == "" {
			continue
		}
		if !common.IsHexAddress(s) {
			continue
		}
		m[common.HexToAddress(s).Hex()] = struct{}{}
	}
	return m
}

func enforceSwapPolicy(cfg *config.AppConfig, intent *strategy.Intent) error {
	// Enforce only for swap intents.
	if intent == nil || (intent.Type != contracts.IntentSwap && strings.ToLower(strings.TrimSpace(intent.Metadata["action"])) != "swap_exact_in") {
		return nil
	}

	meta := intent.Metadata
	if meta == nil {
		meta = map[string]string{}
		intent.Metadata = meta
	}

	swapPoolAddr := strings.TrimSpace(meta["swap_pool"])
	tokenInAddr := strings.TrimSpace(meta["swap_token_in"])
	tokenOutAddr := strings.TrimSpace(meta["swap_token_out"])
	amtInStr := strings.TrimSpace(meta["swap_amount_in"])
	if swapPoolAddr == "" || tokenInAddr == "" || tokenOutAddr == "" || amtInStr == "" {
		return fmt.Errorf("swap requires swap_pool, swap_token_in, swap_token_out, swap_amount_in")
	}

	amountIn := new(big.Int)
	if _, ok := amountIn.SetString(amtInStr, 10); !ok || amountIn.Sign() <= 0 {
		return fmt.Errorf("invalid swap_amount_in: %q", amtInStr)
	}

	// Require an explicit confirmation string when broadcasting (or when forced).
	forceConfirm := os.Getenv("PHOENIX_SWAP_FORCE_CONFIRM") == "1"
	if cfg != nil && cfg.Strategy.DryRun {
		// dry-run mode: confirm optional unless forced
	} else {
		forceConfirm = true
	}
	if forceConfirm {
		want := os.Getenv("PHOENIX_SWAP_CONFIRM_STRING")
		if want == "" {
			want = "I_UNDERSTAND_TESTNET_SWAP"
		}
		if strings.TrimSpace(meta["swap_confirm"]) != want {
			return fmt.Errorf("swap_confirm required (set metadata.swap_confirm=%s)", want)
		}
	}

	if maxStr := strings.TrimSpace(os.Getenv("PHOENIX_SWAP_MAX_AMOUNT_IN")); maxStr != "" {
		maxAmt := new(big.Int)
		if _, ok := maxAmt.SetString(maxStr, 10); ok && maxAmt.Sign() > 0 {
			if amountIn.Cmp(maxAmt) > 0 {
				return fmt.Errorf("swap_amount_in %s exceeds PHOENIX_SWAP_MAX_AMOUNT_IN %s", amountIn.String(), maxAmt.String())
			}
		}
	}

	if maxSlippageStr := strings.TrimSpace(os.Getenv("PHOENIX_SWAP_MAX_SLIPPAGE_BPS")); maxSlippageStr != "" {
		if v, err := strconv.ParseUint(maxSlippageStr, 10, 32); err == nil {
			wantMax := uint32(v)
			if bpsStr := strings.TrimSpace(meta["swap_slippage_bps"]); bpsStr != "" {
				if cur, err := strconv.ParseUint(bpsStr, 10, 32); err == nil {
					if uint32(cur) > wantMax {
						return fmt.Errorf("swap_slippage_bps %d exceeds PHOENIX_SWAP_MAX_SLIPPAGE_BPS %d", cur, wantMax)
					}
				}
			}
		}
	}

	// Optional allowlists.
	if pools := parseAddressAllowlist("PHOENIX_SWAP_ALLOWLIST_POOLS"); pools != nil {
		if !common.IsHexAddress(swapPoolAddr) {
			return fmt.Errorf("invalid swap_pool: %q", swapPoolAddr)
		}
		if _, ok := pools[common.HexToAddress(swapPoolAddr).Hex()]; !ok {
			return fmt.Errorf("swap_pool not allowlisted")
		}
	}
	if toks := parseAddressAllowlist("PHOENIX_SWAP_ALLOWLIST_TOKENS"); toks != nil {
		if !common.IsHexAddress(tokenInAddr) || !common.IsHexAddress(tokenOutAddr) {
			return fmt.Errorf("invalid swap_token_in/out")
		}
		if _, ok := toks[common.HexToAddress(tokenInAddr).Hex()]; !ok {
			return fmt.Errorf("swap_token_in not allowlisted")
		}
		if _, ok := toks[common.HexToAddress(tokenOutAddr).Hex()]; !ok {
			return fmt.Errorf("swap_token_out not allowlisted")
		}
	}
	return nil
}
