package rebalancer

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/engine"
)

type Rebalancer interface {
	Rebalance(ctx context.Context, input RebalanceInput) (*RebalancePlan, error)
}

type DefaultRebalancer struct{}

func NewRebalancer() *DefaultRebalancer {
	return &DefaultRebalancer{}
}

func (r *DefaultRebalancer) Rebalance(ctx context.Context, input RebalanceInput) (*RebalancePlan, error) {
	priceOf := func(token string) float64 {
		if token == "" {
			return 0
		}
		if p, ok := input.Prices[token]; ok {
			return p
		}
		if p, ok := input.Prices[strings.ToLower(token)]; ok {
			return p
		}
		return 0
	}

	// 1. Calculate Total Equity in USD
	totalEquityUSD := 0.0
	for token, bal := range input.WalletBalance {
		price := priceOf(token)
		if price > 0 && bal.Sign() > 0 {
			// Convert balance to float USD
			// Need decimals. For now assuming we can look up decimals or standard map
			// To simplify, we rely on input.Prices being "Price per Raw Unit" ??
			// No, Price is usually per 1.0 Token.
			// So we need decimals.
			// In Phase 1 simplification: we might only have decimals for Pool Tokens.
			// For others (like unknown alts), we might skip or assume 18.
			// Let's iterate and try to find decimals from config or known map.

			decimals := 18
			if strings.EqualFold(token, input.PoolConfig.Token0) {
				decimals = input.PoolConfig.Token0Decimals
			} else if strings.EqualFold(token, input.PoolConfig.Token1) {
				decimals = input.PoolConfig.Token1Decimals
			} else if isStable(token) {
				decimals = 6 // Assume USDC/USDT is 6 mostly
			}

			rawVal, _ := new(big.Float).SetInt(bal).Float64()
			usdVal := rawVal / math.Pow(10, float64(decimals)) * price
			totalEquityUSD += usdVal
		}
	}

	// Avoid division by zero
	if totalEquityUSD <= 0 {
		return nil, fmt.Errorf("total equity is zero")
	}

	// 2. Determine Target Notional USD
	targetPct := input.Intent.TargetNotionalPct
	if targetPct <= 0 {
		targetPct = 0.05 // Default 5%
	}
	if input.PoolConfig.MaxCapPct > 0 && targetPct > input.PoolConfig.MaxCapPct {
		targetPct = input.PoolConfig.MaxCapPct
	}

	targetUSD := totalEquityUSD * targetPct

	// Check Min Idle Cash Constraint
	minIdleUSD := totalEquityUSD * input.RiskLimits.MinIdleCashPct

	// If after spending targetUSD, we dip below minIdle?
	// Note: We don't know exactly how much "Stable" we spend yet,
	// but strictly speaking, we shouldn't use more than (Liquidity - MinReserved).
	// Simplification: Max Budget = Total Equity - Min Idle.
	maxBudget := totalEquityUSD - minIdleUSD
	if targetUSD > maxBudget {
		targetUSD = maxBudget
	}

	if targetUSD <= 0 {
		return nil, fmt.Errorf("target budget is <= 0 after risk constraints")
	}

	// 3. Calculate Required Token0/Token1 for this TargetUSD
	// We need theoretical Amounts for the target Range

	// Let's assume Metadata has "lower_tick", "upper_tick"
	tL, _ := parseMetaInt(input.Intent.Metadata, "lower_tick")
	tU, _ := parseMetaInt(input.Intent.Metadata, "upper_tick")
	if tL >= tU {
		// Fallback or error
		return nil, fmt.Errorf("invalid ticks in intent")
	}

	p0 := priceOf(input.PoolConfig.Token0)
	p1 := priceOf(input.PoolConfig.Token1)
	if p0 == 0 {
		// Allow stable addresses to be treated as $1 if caller forgot to include them in the price map.
		for _, st := range input.PoolConfig.StableTokens {
			if strings.EqualFold(st, input.PoolConfig.Token0) {
				p0 = 1.0
				break
			}
		}
	}
	if p1 == 0 {
		for _, st := range input.PoolConfig.StableTokens {
			if strings.EqualFold(st, input.PoolConfig.Token1) {
				p1 = 1.0
				break
			}
		}
	}
	if p1 == 0 {
		p1 = 1.0
	}

	var sqrtP *big.Int
	switch {
	case input.State.SqrtPriceX96 != nil:
		sqrtP = new(big.Int).Set(input.State.SqrtPriceX96)
	case input.State.CurrentTick != 0:
		sqrtP = TickToSqrtPriceX96(input.State.CurrentTick)
	default:
		dexPrice := p0 / p1
		if dexPrice <= 0 {
			return nil, fmt.Errorf("unable to derive dex price")
		}
		shift := float64(input.PoolConfig.Token1Decimals - input.PoolConfig.Token0Decimals)
		dexPriceRaw := dexPrice * math.Pow(10, shift)
		sqrtP = TickToSqrtPriceX96(engine.PriceToTickRaw(dexPriceRaw))
	}
	if sqrtP == nil || sqrtP.Sign() == 0 {
		return nil, fmt.Errorf("invalid sqrt price snapshot")
	}

	sqrtPA := TickToSqrtPriceX96(tL)
	sqrtPB := TickToSqrtPriceX96(tU)

	// Solve for Liquidity L that matches TargetUSD
	// Value = A0 * P0 + A1 * P1
	// A0 = getAmount0(L) = L * C0
	// A1 = getAmount1(L) = L * C1
	// We calculate C0, C1 for L=1e18 (standard unit)
	testL := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e18))
	testA0, testA1 := GetAmountsForLiquidity(sqrtP, sqrtPA, sqrtPB, testL)

	fA0, _ := new(big.Float).SetInt(testA0).Float64()
	fA1, _ := new(big.Float).SetInt(testA1).Float64()

	// Normalize decimals
	fA0 = fA0 / math.Pow(10, float64(input.PoolConfig.Token0Decimals))
	fA1 = fA1 / math.Pow(10, float64(input.PoolConfig.Token1Decimals))

	valPerUnitL := fA0*p0 + fA1*p1

	if valPerUnitL <= 0 {
		return nil, fmt.Errorf("calculated value per liquidity is 0")
	}

	// Required Liquidity
	// targetUSD / valPerUnitL = scale factor for testL
	scale := targetUSD / valPerUnitL

	// Final Required Amounts
	reqA0Float := fA0 * scale
	reqA1Float := fA1 * scale

	reqA0 := new(big.Int)
	new(big.Float).SetFloat64(reqA0Float * math.Pow(10, float64(input.PoolConfig.Token0Decimals))).Int(reqA0)

	reqA1 := new(big.Int)
	new(big.Float).SetFloat64(reqA1Float * math.Pow(10, float64(input.PoolConfig.Token1Decimals))).Int(reqA1)

	// 4. Calculate Deltas
	have0 := balanceOf(input.WalletBalance, input.PoolConfig.Token0)
	have1 := balanceOf(input.WalletBalance, input.PoolConfig.Token1)
	if have0 == nil {
		have0 = big.NewInt(0)
	}
	if have1 == nil {
		have1 = big.NewInt(0)
	}

	delta0 := new(big.Int).Sub(reqA0, have0)
	delta1 := new(big.Int).Sub(reqA1, have1)

	plan := &RebalancePlan{
		Swaps: []SwapAction{},
		FinalLP: LPAction{
			PoolID:      input.PoolConfig.PoolID,
			LowerTick:   tL,
			UpperTick:   tU,
			IsMint:      true,
			SlippagePct: input.RiskLimits.MaxSwapSlippagePct, // Use same slippage for LP
		},
	}

	// 5. Generate Swaps
	// Strategy:
	// A. If we have surplus of one token, can we sell it to buy the other?
	// B. If we have stablecoin, buy the deficit.

	// Let's implement a simple "Use Stablecoin" first as per Step 4.
	// Actually Step 4 says: 1. Idle Stable 2. Other side.

	addSwap := func(from, to string, amount *big.Int, path []string) {
		if amount.Sign() <= 0 {
			return
		}

		pt := priceOf(to)
		if pt == 0 {
			return
		}

		action := SwapAction{
			FromToken:    common.HexToAddress(from),
			ToToken:      common.HexToAddress(to),
			AmountIn:     amount,
			Fee:          uint32(input.PoolConfig.Fee),
			Route:        "router_v3",
			SlippagePct:  input.RiskLimits.MaxSwapSlippagePct,
			FromDecimals: decimalsForToken(input.PoolConfig, from),
			ToDecimals:   decimalsForToken(input.PoolConfig, to),
			EstimatedUSD: estimateUSD(amount, priceOf(from), decimalsForToken(input.PoolConfig, from)),
		}
		if len(path) > 2 {
			addrPath := make([]common.Address, 0, len(path))
			fees := make([]uint32, 0, len(path)-1)
			for _, p := range path {
				addrPath = append(addrPath, common.HexToAddress(p))
			}
			for i := 0; i < len(path)-1; i++ {
				fees = append(fees, uint32(input.PoolConfig.Fee))
			}
			action.Path = addrPath
			action.Fees = fees
			action.Route = "router_v3_multihop"
		}
		plan.Swaps = append(plan.Swaps, action)
	}

	// Handle Token0 Deficit
	if delta0.Sign() > 0 {
		// Need `delta0` amount of Token0.
		// Try to buy with Token1 Surplus first?
		if delta1.Sign() < 0 {
			// We have surplus Token1 (amount = abs(delta1))
			surplus1 := new(big.Int).Abs(delta1)

			// How much Token0 can we buy with this surplus1?
			// Value1 = surplus1 * P1
			// Amount0 = Value1 / P0
			// (ignoring swap fee/slippage for estimation)

			// We need delta0.
			// Compare value.

			// Convert surplus1 to ~Amount0
			// amt0 = surplus1 * (P1/P0) * (10^(d0-d1))
			valSurplus1 := floatFromBig(surplus1, input.PoolConfig.Token1Decimals) * p1
			valNeed0 := floatFromBig(delta0, input.PoolConfig.Token0Decimals) * p0

			if valSurplus1 >= valNeed0 {
				// We can cover entire deficit with surplus
				// AmountToSell = delta0 * (P0/P1) ...
				// Better: AmountIn = need0_value / P1 * 1.01 (buffer)
				amtInFloat := (valNeed0 / p1) * 1.01
				amtIn := bigFromFloat(amtInFloat, input.PoolConfig.Token1Decimals)
				addSwap(input.PoolConfig.Token1, input.PoolConfig.Token0, amtIn, nil)

				// Update balance virtual state
				delta0 = big.NewInt(0)
				// delta1 is reduced (still negative or zero)
			} else {
				// Sell all surplus
				addSwap(input.PoolConfig.Token1, input.PoolConfig.Token0, surplus1, nil)
				// Remaining delta0
				restVal0 := valNeed0 - valSurplus1
				// ... update delta0 ...
				delta0 = bigFromFloat(restVal0/p0, input.PoolConfig.Token0Decimals)
			}
		}

		// If still need Token0, use Stablecoin (USDT/USDC)
		if delta0.Sign() > 0 {
			stable := findStable(input.WalletBalance, input.PoolConfig.StableTokens)
			if stable != "" {
				// Buy delta0 with stable
				valNeed := floatFromBig(delta0, input.PoolConfig.Token0Decimals) * p0
				amtInStableFloat := (valNeed / input.Prices[stable]) * 1.01
				amtIn := bigFromFloat(amtInStableFloat, 6) // assume 6

				// Check stable balance
				haveStable := input.WalletBalance[stable]
				if haveStable.Cmp(amtIn) >= 0 {
					path := []string{stable, input.PoolConfig.Token1, input.PoolConfig.Token0}
					if strings.EqualFold(stable, input.PoolConfig.Token0) || strings.EqualFold(stable, input.PoolConfig.Token1) {
						path = nil
					}
					addSwap(stable, input.PoolConfig.Token0, amtIn, path)
					delta0 = big.NewInt(0)
				} else {
					// Partial fill
					path := []string{stable, input.PoolConfig.Token1, input.PoolConfig.Token0}
					if strings.EqualFold(stable, input.PoolConfig.Token0) || strings.EqualFold(stable, input.PoolConfig.Token1) {
						path = nil
					}
					addSwap(stable, input.PoolConfig.Token0, haveStable, path)
				}
			}
		}
	}

	// Handle Token1 Deficit (Similar logic)
	if delta1.Sign() > 0 {
		// Use Token0 Surplus?
		if delta0.Sign() < 0 {
			surplus0 := new(big.Int).Abs(delta0)
			valSurplus0 := floatFromBig(surplus0, input.PoolConfig.Token0Decimals) * p0
			valNeed1 := floatFromBig(delta1, input.PoolConfig.Token1Decimals) * p1

			if valSurplus0 >= valNeed1 {
				amtInFloat := (valNeed1 / p0) * 1.01
				amtIn := bigFromFloat(amtInFloat, input.PoolConfig.Token0Decimals)
				addSwap(input.PoolConfig.Token0, input.PoolConfig.Token1, amtIn, nil)
				delta1 = big.NewInt(0)
			} else {
				addSwap(input.PoolConfig.Token0, input.PoolConfig.Token1, surplus0, nil)
				restVal1 := valNeed1 - valSurplus0
				delta1 = bigFromFloat(restVal1/p1, input.PoolConfig.Token1Decimals)
			}
		}

		if delta1.Sign() > 0 {
			stable := findStable(input.WalletBalance, input.PoolConfig.StableTokens)
			if stable != "" {
				valNeed := floatFromBig(delta1, input.PoolConfig.Token1Decimals) * p1
				amtInStableFloat := (valNeed / input.Prices[stable]) * 1.01
				amtIn := bigFromFloat(amtInStableFloat, 6)

				haveStable := input.WalletBalance[stable]
				if haveStable.Cmp(amtIn) >= 0 {
					path := []string{stable, input.PoolConfig.Token0, input.PoolConfig.Token1}
					if strings.EqualFold(stable, input.PoolConfig.Token0) || strings.EqualFold(stable, input.PoolConfig.Token1) {
						path = nil
					}
					addSwap(stable, input.PoolConfig.Token1, amtIn, path)
					delta1 = big.NewInt(0)
				} else {
					path := []string{stable, input.PoolConfig.Token0, input.PoolConfig.Token1}
					if strings.EqualFold(stable, input.PoolConfig.Token0) || strings.EqualFold(stable, input.PoolConfig.Token1) {
						path = nil
					}
					addSwap(stable, input.PoolConfig.Token1, haveStable, path)
				}
			}
		}
	}

	// 6. Finalize LP Action
	// Based on "Virtual Balances" after swaps, we determine final Amount0/Amount1.
	// Note: In real exec, we might have slightly different amounts due to slippage.
	// The Strategy/Executor should re-check balances.
	// But `FinalLP` fields tell the Executor "This is what we WANT".

	plan.FinalLP.Amount0 = reqA0
	plan.FinalLP.Amount1 = reqA1

	// Check if swapped fulfilled enough?
	// If delta still > 0, we can't mint full size.
	// It's safer to not return specific Amounts in FinalLP if we are unsure,
	// BUT Executor needs guidance.
	// Let's assume Swaps will succeed and we get close to reqA0/reqA1.

	plan.UtilizedPct = targetPct

	return plan, nil
}

// Helpers
func isStable(token string) bool {
	t := strings.ToUpper(token)
	return strings.Contains(t, "USDT") || strings.Contains(t, "USDC") || strings.Contains(t, "DAI")
}

func findStable(balances map[string]*big.Int, preferred []string) string {
	for _, token := range preferred {
		if bal := balanceOf(balances, token); bal != nil && bal.Sign() > 0 {
			return token
		}
	}
	for t, bal := range balances {
		if bal.Sign() > 0 && isStable(t) {
			return t
		}
	}
	return ""
}

func parseMetaInt(meta map[string]string, key string) (int64, bool) {
	if v, ok := meta[key]; ok {
		var i int64
		fmt.Sscanf(v, "%d", &i) // Simple parse
		return i, true
	}
	return 0, false
}

func floatFromBig(n *big.Int, decimals int) float64 {
	f, _ := new(big.Float).SetInt(n).Float64()
	return f / math.Pow(10, float64(decimals))
}

func bigFromFloat(f float64, decimals int) *big.Int {
	val := f * math.Pow(10, float64(decimals))
	i := new(big.Int)
	new(big.Float).SetFloat64(val).Int(i)
	return i
}

func balanceOf(balances map[string]*big.Int, token string) *big.Int {
	for k, v := range balances {
		if strings.EqualFold(k, token) {
			return v
		}
	}
	return nil
}

func decimalsForToken(cfg PoolConfig, token string) int {
	switch {
	case strings.EqualFold(token, cfg.Token0):
		return cfg.Token0Decimals
	case strings.EqualFold(token, cfg.Token1):
		return cfg.Token1Decimals
	default:
		// default 6 for common stables
		return 6
	}
}

func estimateUSD(amount *big.Int, price float64, decimals int) float64 {
	if amount == nil || price <= 0 {
		return 0
	}
	val, _ := new(big.Float).SetInt(amount).Float64()
	if decimals <= 0 {
		decimals = 18
	}
	return val / math.Pow10(decimals) * price
}
