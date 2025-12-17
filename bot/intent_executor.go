package bot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/events"
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

type RebalanceLimiter interface {
	Allow(poolID string, limit int) bool
}

type IntentExecutorDeps struct {
	ConfigValue       *atomic.Value
	Queue             *strategy.IntentQueue
	Risk              *risk.Manager
	Guard             *poolguard.Guard
	SelectGateway     func(chainID int64) gateway.Gateway
	Store             *storage.Store
	Stream            events.Stream
	Adapter           *univ3.Adapter
	Router            *univ3.Router
	SwapHelperByChain map[int64]*univ3.SwapHelper
	Rebalancer        rebalancer.Rebalancer
	PriceProvider     func(token string) float64
	QuoterByChain     map[int64]*univ3.Quoter

	FindPoolConfig        func(cfg *config.AppConfig, poolID string) (config.PoolConfig, bool)
	EffectiveMaxCapPct    func(poolMaxCapPct float64, riskMaxUtilPct float64) float64
	ExecuteSwap           func(ctx context.Context, gw gateway.Gateway, router *univ3.Router, swapHelper *univ3.SwapHelper, poolCfg config.PoolConfig, action rebalancer.SwapAction, priceProvider func(string) float64, quoter *univ3.Quoter, slippagePct float64, store *storage.Store, stream events.Stream, parentIntentID string, stepIndex *int) (*gateway.TxResult, error)
	WaitForReceipt        func(ctx context.Context, ethGw *gateway.EthGateway, hash common.Hash) *types.Receipt
	HasSufficientBalances func(ctx context.Context, gw *gateway.EthGateway, meta map[string]string) bool
	ParseMetadataFloat    func(meta map[string]string, key string) float64
	FloatFromBigInt       func(amount *big.Int, decimals int) float64
	SetLastRebalanceAt    func(poolID string, t time.Time)
	RebalanceLimiter      RebalanceLimiter
}

type IntentExecutor struct {
	deps IntentExecutorDeps
}

func NewIntentExecutor(deps IntentExecutorDeps) (*IntentExecutor, error) {
	switch {
	case deps.ConfigValue == nil:
		return nil, fmt.Errorf("missing ConfigValue")
	case deps.Queue == nil:
		return nil, fmt.Errorf("missing Queue")
	case deps.Risk == nil:
		return nil, fmt.Errorf("missing Risk")
	case deps.Guard == nil:
		return nil, fmt.Errorf("missing Guard")
	case deps.SelectGateway == nil:
		return nil, fmt.Errorf("missing SelectGateway")
	case deps.FindPoolConfig == nil:
		return nil, fmt.Errorf("missing FindPoolConfig")
	case deps.EffectiveMaxCapPct == nil:
		return nil, fmt.Errorf("missing EffectiveMaxCapPct")
	case deps.ExecuteSwap == nil:
		return nil, fmt.Errorf("missing ExecuteSwap")
	case deps.WaitForReceipt == nil:
		return nil, fmt.Errorf("missing WaitForReceipt")
	case deps.HasSufficientBalances == nil:
		return nil, fmt.Errorf("missing HasSufficientBalances")
	case deps.ParseMetadataFloat == nil:
		return nil, fmt.Errorf("missing ParseMetadataFloat")
	case deps.FloatFromBigInt == nil:
		return nil, fmt.Errorf("missing FloatFromBigInt")
	case deps.RebalanceLimiter == nil:
		return nil, fmt.Errorf("missing RebalanceLimiter")
	default:
	}
	return &IntentExecutor{deps: deps}, nil
}

func (e *IntentExecutor) Start(ctx context.Context) {
	go func() {
		for {
			intent := e.deps.Queue.Dequeue()
			select {
			case <-ctx.Done():
				return
			default:
			}
			if e.deps.Risk.ShouldThrottle(2 * time.Second) {
				log.Printf("[IntentExecutor] throttling intent %s due to min interval", intent.ID)
				time.Sleep(2 * time.Second)
			}
			gw := e.deps.SelectGateway(intent.ChainID)
			e.executeIntent(ctx, intent, gw)
		}
	}()
}

func (e *IntentExecutor) executeIntent(ctx context.Context, intent strategy.Intent, gw gateway.Gateway) {
	if intent.Metadata == nil {
		intent.Metadata = make(map[string]string)
	}
	if gw == nil {
		log.Printf("[IntentExecutor] no gateway available for chain %d", intent.ChainID)
		return
	}
	if err := e.deps.Risk.CanProceed(); err != nil {
		log.Printf("[Risk] skip intent %s: %v", intent.ID, err)
		return
	}

	token0Addr := intent.Metadata["token0"]
	token1Addr := intent.Metadata["token1"]
	check := e.deps.Guard.CheckPool(context.Background(), intent.PoolID, intent.ChainID, token0Addr, token1Addr)
	if check.Risk == poolguard.RiskDanger {
		log.Printf("[PoolGuard] block intent %s: %s", intent.ID, check.Reason)
		return
	}

	currentCfg := e.deps.ConfigValue.Load().(*config.AppConfig)
	isDryRun := config.SafetyFromConfig(currentCfg).EffectiveDryRun
	poolCfg, ok := e.deps.FindPoolConfig(currentCfg, intent.PoolID)
	if !ok {
		log.Printf("[IntentExecutor] Unknown pool %s", intent.PoolID)
		return
	}
	tracked := strings.TrimSpace(intent.Metadata["operation_id"]) != ""
	stepStore := e.deps.Store
	stepStream := e.deps.Stream
	if !tracked {
		stepStore = nil
		stepStream = nil
	}
	stepIndex := 0
	if tracked {
		UpsertIntentStatus(stepStore, intent, "running")
	}

	// Hard cap per-pool rebalance attempts (prevents runaway churn).
	if intent.Type == strategy.IntentRebalance && !isDryRun {
		if !e.deps.RebalanceLimiter.Allow(intent.PoolID, poolCfg.MaxDailyRebalances) {
			log.Printf("[Risk] skip intent %s: pool %s max_daily_rebalances=%d exceeded", intent.ID, intent.PoolID, poolCfg.MaxDailyRebalances)
			return
		}
	}

	// Use per-pool PositionManager (avoids cfg.Pools[0] coupling).
	localAdapter := e.deps.Adapter
	if poolCfg.PositionManager != "" {
		if localAdapter == nil || !strings.EqualFold(localAdapter.TargetAddress().Hex(), poolCfg.PositionManager) {
			localAdapter = univ3.NewAdapter(poolCfg.PositionManager)
		}
	}

	// Resolve known UniV3 position tokenId for this pool (config -> store -> runtime).
	existingTokenID := ""
	if rt, ok := GetPoolRuntimeSnapshot(intent.PoolID); ok && rt != nil {
		existingTokenID = strings.TrimSpace(rt.PositionTokenID)
	}
	if existingTokenID == "" && e.deps.Store != nil {
		if tid, err := e.deps.Store.GetPoolPositionTokenID(intent.PoolID, intent.ChainID); err == nil {
			existingTokenID = strings.TrimSpace(tid)
		}
	}
	if existingTokenID == "" {
		existingTokenID = strings.TrimSpace(poolCfg.PositionTokenID)
	}
	if existingTokenID != "" {
		intent.Metadata["position_token_id"] = existingTokenID
	}

	poolMintGuard := GetMintGuard(intent.PoolID)
	poolMintGuard.Store(true)
	defer poolMintGuard.Store(false)

	ethGw, isEthGw := gw.(*gateway.EthGateway)
	var deferredCloseTokenID *big.Int
	if intent.Type == strategy.IntentRebalance && isEthGw && !isDryRun {
		if localAdapter == nil {
			log.Printf("[IntentExecutor] missing position manager adapter for pool %s", intent.PoolID)
			e.deps.Risk.RecordFailure()
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
					deferred, err := DrainPositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID, keepPct, stepStore, stepStream, intent.ID, &stepIndex)
					if err != nil {
						log.Printf("[IntentExecutor] drain existing position failed (pool %s): %v", intent.PoolID, err)
						e.deps.Risk.RecordFailure()
						return
					}
					if deferred {
						deferredCloseTokenID = tokenID
					} else {
						if err := ClosePositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID, stepStore, stepStream, intent.ID, &stepIndex); err != nil {
							log.Printf("[IntentExecutor] close existing position failed (pool %s): %v", intent.PoolID, err)
							e.deps.Risk.RecordFailure()
							return
						}
						ClearPoolRuntimePosition(intent.PoolID)
						if e.deps.Store != nil {
							if err := e.deps.Store.ClearPoolPosition(intent.PoolID, intent.ChainID); err != nil {
								log.Printf("[Storage] clear pool position failed (pool=%s chain=%d): %v", intent.PoolID, intent.ChainID, err)
							}
						}
						existingTokenID = ""
						intent.Metadata["position_token_id"] = ""
					}
				} else {
					if err := ClosePositionTokenID(ctx, ethGw, localAdapter, pmAddr, tokenID, stepStore, stepStream, intent.ID, &stepIndex); err != nil {
						log.Printf("[IntentExecutor] close existing position failed (pool %s): %v", intent.PoolID, err)
						e.deps.Risk.RecordFailure()
						return
					}
					ClearPoolRuntimePosition(intent.PoolID)
					if e.deps.Store != nil {
						if err := e.deps.Store.ClearPoolPosition(intent.PoolID, intent.ChainID); err != nil {
							log.Printf("[Storage] clear pool position failed (pool=%s chain=%d): %v", intent.PoolID, intent.ChainID, err)
						}
					}
					existingTokenID = ""
					intent.Metadata["position_token_id"] = ""
				}
			}
		}
	}

	var preBal0, preBal1 *big.Int
	shouldComputeWalletDelta := intent.Type == strategy.IntentWithdraw || intent.Type == strategy.IntentCollectFee || intent.Type == strategy.IntentRebalance
	if shouldComputeWalletDelta {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			preBal0, _ = ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token0))
			preBal1, _ = ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token1))
		}
	}

	runtimeState, _ := GetPoolRuntimeSnapshot(intent.PoolID)
	var poolStateSnap rebalancer.PoolStateSnapshot
	if runtimeState != nil {
		poolStateSnap.CurrentTick = runtimeState.CurrentTick
		if runtimeState.SqrtPriceX96 != nil {
			poolStateSnap.SqrtPriceX96 = new(big.Int).Set(runtimeState.SqrtPriceX96)
		}
	}

	if e.deps.Rebalancer != nil && isEthGw && (intent.Type == strategy.IntentRebalance || intent.TargetNotionalPct > 0) {
		bals := make(map[string]*big.Int)
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
		for _, st := range poolCfg.StableTokens {
			fetchBal(st)
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
				strings.ToLower(token0Addr): e.deps.PriceProvider(strings.ToLower(poolCfg.Token0)),
				strings.ToLower(token1Addr): e.deps.PriceProvider(strings.ToLower(poolCfg.Token1)),
			},
			PoolConfig: rebalancer.PoolConfig{
				PoolID:         poolCfg.ID,
				Token0:         poolCfg.Token0,
				Token1:         poolCfg.Token1,
				Token0Decimals: poolCfg.Token0Decimals,
				Token1Decimals: poolCfg.Token1Decimals,
				Fee:            poolFee,
				MaxCapPct:      e.deps.EffectiveMaxCapPct(poolCfg.MaxCapPct, currentCfg.Risk.MaxUtilizationPct),
				StableTokens:   stables,
			},
			RiskLimits: rebalancer.RiskLimits{
				MinIdleCashPct:     currentCfg.Wallet.MinIdlePct,
				MaxSwapSlippagePct: currentCfg.Risk.MaxSwapSlippagePct,
			},
			State: poolStateSnap,
		}

		plan, planErr := e.deps.Rebalancer.Rebalance(ctx, input)
		if planErr != nil {
			log.Printf("[Rebalancer] Error: %v", planErr)
			e.deps.Risk.RecordFailure()
			return
		}

		if plan != nil {
			if plan.FinalLP.Amount0 != nil {
				intent.Metadata["amount0"] = plan.FinalLP.Amount0.String()
			}
			if plan.FinalLP.Amount1 != nil {
				intent.Metadata["amount1"] = plan.FinalLP.Amount1.String()
			}

			quoter := (*univ3.Quoter)(nil)
			if e.deps.QuoterByChain != nil {
				quoter = e.deps.QuoterByChain[intent.ChainID]
			}
			swapHelper := (*univ3.SwapHelper)(nil)
			if e.deps.SwapHelperByChain != nil {
				swapHelper = e.deps.SwapHelperByChain[intent.ChainID]
			}

			swapStatsList := make([]swapStats, 0, len(plan.Swaps))
			for _, s := range plan.Swaps {
				if isDryRun {
					if tracked {
						idx := stepIndex
						stepIndex++
						RecordStepSimulated(ctx, stepStore, stepStream, intent.ID, idx, "swap", SimulatedTxHash(idx, intent.ID, "SIMULATED_SWAP"), map[string]interface{}{
							"from":      s.FromToken.Hex(),
							"to":        s.ToToken.Hex(),
							"amount_in": s.AmountIn.String(),
							"fee":       s.Fee,
						})
					}
					continue
				}

				swapUSD := s.EstimatedUSD
				if swapUSD > 0 {
					if err := e.deps.Risk.CanSwap(swapUSD); err != nil {
						log.Printf("[Risk] Swap rejected (%s): %v", intent.ID, err)
						return
					}
				}

				var balBeforeOut *big.Int
				if ethGw != nil {
					balBeforeOut, _ = ethGw.BalanceOfERC20(ctx, s.ToToken)
				}
				if swapHelper != nil {
					if runtimeState == nil || runtimeState.PoolLiquidity == nil || runtimeState.PoolLiquidity.Sign() <= 0 {
						log.Printf("[Rebalancer] skip swap (pool liquidity=0) to allow mint first (from=%s to=%s)", s.FromToken.Hex(), s.ToToken.Hex())
						continue
					}
				}

				res, err := e.deps.ExecuteSwap(ctx, gw, e.deps.Router, swapHelper, poolCfg, s, e.deps.PriceProvider, quoter, s.SlippagePct, stepStore, stepStream, intent.ID, &stepIndex)
				if err != nil {
					e.deps.Risk.RecordFailure()
					if tracked && res != nil {
						RecordStepFinal(ctx, stepStore, stepStream, intent.ID, stepIndex-1, "swap", "failed", res.Hash.Hex(), map[string]interface{}{"error": err.Error()})
					}
					return
				}
				swapStepIdx := stepIndex - 1

				if ethGw != nil && res != nil {
					rcpt := e.deps.WaitForReceipt(ctx, ethGw, res.Hash)
					if rcpt == nil || rcpt.Status != 1 {
						e.deps.Risk.RecordFailure()
						if tracked {
							RecordStepFinal(ctx, stepStore, stepStream, intent.ID, swapStepIdx, "swap", "failed", res.Hash.Hex(), map[string]interface{}{"hash": res.Hash.Hex()})
						}
						return
					}
					if tracked {
						RecordStepFinal(ctx, stepStore, stepStream, intent.ID, swapStepIdx, "swap", "mined", res.Hash.Hex(), map[string]interface{}{"hash": res.Hash.Hex(), "gas_used": rcpt.GasUsed})
					}
				}
				if swapUSD > 0 {
					e.deps.Risk.RecordSwap(swapUSD)
				}

				var actualOut *big.Int
				if ethGw != nil {
					balAfterOut, _ := ethGw.BalanceOfERC20(ctx, s.ToToken)
					if balBeforeOut != nil && balAfterOut != nil {
						actualOut = new(big.Int).Sub(balAfterOut, balBeforeOut)
					}
				}
				st := swapStats{
					FromToken:   s.FromToken.Hex(),
					ToToken:     s.ToToken.Hex(),
					AmountIn:    s.AmountIn.String(),
					SlippagePct: s.SlippagePct,
					PnLUSD:      s.EstimatedUSD,
				}
				if s.MinAmountOut != nil {
					st.MinAmountOut = s.MinAmountOut.String()
				}
				if actualOut != nil {
					st.ActualOut = actualOut.String()
				}
				if res != nil {
					st.TxHash = res.Hash.Hex()
				}
				swapStatsList = append(swapStatsList, st)
			}

			if len(swapStatsList) > 0 {
				totalSwapPnL := 0.0
				for i := range swapStatsList {
					totalSwapPnL += swapStatsList[i].PnLUSD
				}
				intent.ExpectedPnL += totalSwapPnL
				intent.Metadata["swap_pnl_usd"] = fmt.Sprintf("%.6f", totalSwapPnL)
				if b, err := json.Marshal(swapStatsList); err == nil {
					intent.Metadata["swap_details"] = string(b)
				}
			}
		}
	}

	if ethGw, ok := gw.(*gateway.EthGateway); ok {
		if deferredCloseTokenID != nil && deferredCloseTokenID.Sign() > 0 {
			pmAddr := common.HexToAddress(poolCfg.PositionManager)
			if err := ClosePositionTokenID(ctx, ethGw, localAdapter, pmAddr, deferredCloseTokenID, stepStore, stepStream, intent.ID, &stepIndex); err != nil {
				e.deps.Risk.RecordFailure()
				return
			}
			ClearPoolRuntimePosition(intent.PoolID)
			if e.deps.Store != nil {
				_ = e.deps.Store.ClearPoolPosition(intent.PoolID, intent.ChainID)
			}
			intent.Metadata["position_token_id"] = ""
			deferredCloseTokenID = nil
		}

		t0 := common.HexToAddress(intent.Metadata["token0"])
		t1 := common.HexToAddress(intent.Metadata["token1"])
		if bal0, err := ethGw.BalanceOfERC20(ctx, t0); err == nil {
			if amt0, ok := new(big.Int).SetString(intent.Metadata["amount0"], 10); ok {
				if bal0.Cmp(amt0) < 0 {
					intent.Metadata["amount0"] = bal0.String()
				}
			}
		}
		if bal1, err := ethGw.BalanceOfERC20(ctx, t1); err == nil {
			if amt1, ok := new(big.Int).SetString(intent.Metadata["amount1"], 10); ok {
				if bal1.Cmp(amt1) < 0 {
					intent.Metadata["amount1"] = bal1.String()
				}
			}
		}
	}

	if !isDryRun {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			if !e.deps.HasSufficientBalances(ctx, ethGw, intent.Metadata) {
				e.deps.Risk.RecordFailure()
				return
			}
		}
	}

	txHash := ""
	status := ""
	var minedReceipt *types.Receipt
	if isDryRun || gw == nil {
		txHash = "0xSIMULATED_" + intent.ID
		status = "simulated"
		e.deps.Risk.RecordSuccess()
		if tracked && intent.Type == strategy.IntentRebalance {
			mintStepIdx := stepIndex
			stepIndex++
			RecordStepSimulated(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", txHash, map[string]interface{}{
				"lower_tick": intent.Metadata["lower_tick"],
				"upper_tick": intent.Metadata["upper_tick"],
				"amount0":    intent.Metadata["amount0"],
				"amount1":    intent.Metadata["amount1"],
				"target":     intent.Metadata["target"],
			})
		}
	} else {
		mintStepIdx := -1
		if tracked && intent.Type == strategy.IntentRebalance {
			mintStepIdx = stepIndex
			stepIndex++
			RecordStepPending(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", map[string]interface{}{
				"lower_tick": intent.Metadata["lower_tick"],
				"upper_tick": intent.Metadata["upper_tick"],
				"amount0":    intent.Metadata["amount0"],
				"amount1":    intent.Metadata["amount1"],
			})
		}

		// Build mint calldata for on-chain execution (and ensure ERC20 approvals) before sending.
		if intent.Type == strategy.IntentRebalance {
			ethGw, ok := gw.(*gateway.EthGateway)
			if !ok || ethGw == nil {
				status = "failed"
				e.deps.Risk.RecordFailure()
				if mintStepIdx >= 0 {
					RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": "missing eth gateway"})
				}
				if tracked {
					UpsertIntentStatus(stepStore, intent, "failed")
				}
				return
			}
			if localAdapter == nil {
				status = "failed"
				e.deps.Risk.RecordFailure()
				if mintStepIdx >= 0 {
					RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": "missing position manager adapter"})
				}
				if tracked {
					UpsertIntentStatus(stepStore, intent, "failed")
				}
				return
			}

			intent.Metadata["recipient"] = ethGw.WalletAddress().Hex()
			intent.Metadata["target"] = localAdapter.TargetAddress().Hex()

			// Ensure allowance for both pool tokens (best-effort).
			pmAddr := localAdapter.TargetAddress()
			parseAmt := func(key string) *big.Int {
				v := strings.TrimSpace(intent.Metadata[key])
				if v == "" {
					return big.NewInt(0)
				}
				if i, ok := new(big.Int).SetString(v, 10); ok && i.Sign() > 0 {
					return i
				}
				return big.NewInt(0)
			}
			amt0 := parseAmt("amount0")
			amt1 := parseAmt("amount1")
			t0 := common.HexToAddress(intent.Metadata["token0"])
			t1 := common.HexToAddress(intent.Metadata["token1"])
			if amt0.Sign() > 0 {
				if err := ethGw.EnsureAllowance(ctx, t0, pmAddr, amt0); err != nil {
					status = "failed"
					e.deps.Risk.RecordFailure()
					if mintStepIdx >= 0 {
						RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": "approve token0 failed", "detail": err.Error()})
					}
					if tracked {
						UpsertIntentStatus(stepStore, intent, "failed")
					}
					return
				}
			}
			if amt1.Sign() > 0 {
				if err := ethGw.EnsureAllowance(ctx, t1, pmAddr, amt1); err != nil {
					status = "failed"
					e.deps.Risk.RecordFailure()
					if mintStepIdx >= 0 {
						RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": "approve token1 failed", "detail": err.Error()})
					}
					if tracked {
						UpsertIntentStatus(stepStore, intent, "failed")
					}
					return
				}
			}

			data, err := localAdapter.BuildMintData(intent)
			if err != nil {
				status = "failed"
				e.deps.Risk.RecordFailure()
				if mintStepIdx >= 0 {
					RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": "build mint calldata failed", "detail": err.Error()})
				}
				if tracked {
					UpsertIntentStatus(stepStore, intent, "failed")
				}
				return
			}
			intent.Metadata["calldata"] = "0x" + hex.EncodeToString(data)
		}

		result, err := gw.Send(ctx, intent)
		if err != nil {
			status = "failed"
			e.deps.Risk.RecordFailure()
			if mintStepIdx >= 0 {
				RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", "", map[string]interface{}{"error": err.Error()})
			}
		} else {
			txHash = result.Hash.Hex()
			status = string(result.Status)
			e.deps.Risk.RecordSuccess()
			if mintStepIdx >= 0 {
				RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "sent", txHash, map[string]interface{}{
					"lower_tick": intent.Metadata["lower_tick"],
					"upper_tick": intent.Metadata["upper_tick"],
					"amount0":    intent.Metadata["amount0"],
					"amount1":    intent.Metadata["amount1"],
					"target":     intent.Metadata["target"],
				})
			}
			if ethGw, ok := gw.(*gateway.EthGateway); ok && result.Status == gateway.StatusPending {
				minedReceipt = e.deps.WaitForReceipt(ctx, ethGw, result.Hash)
			}
			if mintStepIdx >= 0 {
				if minedReceipt == nil || minedReceipt.Status != 1 {
					RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "failed", txHash, map[string]interface{}{"hash": txHash})
				} else {
					RecordStepFinal(ctx, stepStore, stepStream, intent.ID, mintStepIdx, "mint", "mined", txHash, map[string]interface{}{"hash": txHash, "gas_used": minedReceipt.GasUsed})
				}
			}
		}
	}

	if !isDryRun && minedReceipt != nil && intent.Type == strategy.IntentRebalance {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			pmAddr := common.HexToAddress(poolCfg.PositionManager)
			newTokenID := ParseMintedPositionTokenID(minedReceipt, pmAddr, ethGw.WalletAddress())
			if newTokenID != nil && newTokenID.Sign() > 0 {
				tokenStr := newTokenID.String()
				if e.deps.Store != nil {
					_ = e.deps.Store.UpsertPoolPosition(intent.PoolID, intent.ChainID, tokenStr)
				}
				if tL, tU, liq, ok, _ := FetchPositionByTokenID(ctx, ethGw, localAdapter, pmAddr, newTokenID); ok && liq != nil && liq.Sign() > 0 {
					liqF, _ := new(big.Float).SetInt(liq).Float64()
					SetPoolRuntimePosition(intent.PoolID, tokenStr, engine.CurrentPosition{LowerTick: tL, UpperTick: tU, Liquidity: liqF})
				} else {
					SetPoolRuntimePosition(intent.PoolID, tokenStr, engine.CurrentPosition{})
				}
				intent.Metadata["position_token_id"] = tokenStr
				if e.deps.SetLastRebalanceAt != nil {
					e.deps.SetLastRebalanceAt(intent.PoolID, time.Now())
				}
			}
		}
	}

	if shouldComputeWalletDelta {
		if ethGw, ok := gw.(*gateway.EthGateway); ok {
			postBal0, _ := ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token0))
			postBal1, _ := ethGw.BalanceOfERC20(ctx, common.HexToAddress(poolCfg.Token1))
			p0 := e.deps.PriceProvider(strings.ToLower(poolCfg.Token0))
			p1 := e.deps.PriceProvider(strings.ToLower(poolCfg.Token1))
			usdDelta0 := e.deps.FloatFromBigInt(new(big.Int).Sub(postBal0, preBal0), poolCfg.Token0Decimals) * p0
			usdDelta1 := e.deps.FloatFromBigInt(new(big.Int).Sub(postBal1, preBal1), poolCfg.Token1Decimals) * p1
			deltaUSD := usdDelta0 + usdDelta1

			if e.deps.Store != nil && intent.Type == strategy.IntentRebalance {
				if deltaUSD < 0 {
					_ = e.deps.Store.UpsertPoolCostBasis(intent.PoolID, intent.ChainID, -deltaUSD)
				}
				intent.Metadata["wallet_delta_usd"] = fmt.Sprintf("%.6f", deltaUSD)
			}

			if intent.Type == strategy.IntentWithdraw || intent.Type == strategy.IntentCollectFee {
				intent.Metadata["wallet_pnl_usd"] = fmt.Sprintf("%.6f", deltaUSD)
				if e.deps.Store != nil && intent.Type == strategy.IntentWithdraw {
					basis, _ := e.deps.Store.GetPoolCostBasis(intent.PoolID, intent.ChainID)
					if basis > 0 {
						intent.ExpectedPnL += deltaUSD - basis
						_ = e.deps.Store.ClearPoolCostBasis(intent.PoolID, intent.ChainID)
					} else {
						intent.ExpectedPnL += deltaUSD
					}
				} else {
					intent.ExpectedPnL += deltaUSD
				}
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
		TargetTo:        intent.Metadata["target"],
		Status:          status,
		Token0Amt:       intent.Metadata["amount0"],
		Token1Amt:       intent.Metadata["amount1"],
		SwapDetails:     intent.Metadata["swap_details"],
		PnL:             intent.ExpectedPnL,
		IsSimulation:    isDryRun,
		StrategyVersion: intent.StrategyVersion,
		RiskMode:        intent.RiskMode,
		NotionalUSD:     e.deps.ParseMetadataFloat(intent.Metadata, "notional_usd"),
		GasCostUSD:      e.deps.ParseMetadataFloat(intent.Metadata, "gas_usd"),
	}

	if e.deps.Store != nil {
		if err := e.deps.Store.SaveTrade(record); err != nil {
			log.Printf("[Storage] save trade failed: %v", err)
		}
	}

	if tracked {
		final := "failed"
		if isDryRun {
			final = "simulated"
		} else if intent.Type == strategy.IntentRebalance {
			if minedReceipt != nil && minedReceipt.Status == 1 {
				final = "succeeded"
			}
		} else if status != "" {
			final = status
		}
		UpsertIntentStatus(stepStore, intent, final)
	}

	if e.deps.Stream != nil {
		_ = e.deps.Stream.Publish(ctx, events.TopicIntentExec, record)
	}
}
