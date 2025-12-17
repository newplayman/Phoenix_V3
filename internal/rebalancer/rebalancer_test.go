package rebalancer

import (
	"context"
	"math/big"
	"testing"

	"phoenix-v3/internal/strategy"
)

func TestRebalance_StableToLP(t *testing.T) {
	// Scenario: Need ETH/USDC LP. Have 10000 USDC only. Target 50% equity.
	// Prices: ETH=2000, USDC=1.
	// Target Notional = 5000 USD.
	// Range: Wide range such that Token0/Token1 ratio is ~ 50/50 value.
	// Expect: Swap 2500 USDC -> 1.25 ETH. LP with 1.25 ETH + 2500 USDC.

	rebal := NewRebalancer()

	intent := strategy.Intent{
		ID:                "test-1",
		TargetNotionalPct: 0.5,
		Metadata: map[string]string{
			"lower_tick":      "-201000",
			"upper_tick":      "-199000",
			"token0_decimals": "18",
			"token1_decimals": "6",
		},
	}

	// Balances
	usdcAddr := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	ethAddr := "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"

	bals := map[string]*big.Int{
		usdcAddr: big.NewInt(10000_000000), // 10k USDC
	}

	prices := map[string]float64{
		ethAddr:  2000.0,
		usdcAddr: 1.0,
	}

	// Mock EngineOutput? Not used in current code (removed).
	// Current implementation calculates Liquidity from Intent Metadata ticks.

	input := RebalanceInput{
		Intent:        intent,
		WalletBalance: bals,
		Prices:        prices,
		PoolConfig: PoolConfig{
			PoolID:         "pool-eth-usdc",
			Token0:         ethAddr,
			Token1:         usdcAddr,
			Token0Decimals: 18,
			Token1Decimals: 6,
			MaxCapPct:      1.0,
		},
		RiskLimits: RiskLimits{
			MinIdleCashPct:     0.1,
			MaxSwapSlippagePct: 0.01,
		},
	}

	// Mock TickToSqrtPriceX96?
	// Our Math implementation is pure Go, so it works.
	// But we removed `input.EngineOutput`.
	// However, `Rebalance` function derives `dexPrice` from `prices`.
	// dexPrice = P0/P1 = 2000.

	// Run
	plan, err := rebal.Rebalance(context.Background(), input)
	if err != nil {
		t.Fatalf("Rebalance failed: %v", err)
	}

	if len(plan.Swaps) == 0 {
		t.Errorf("Expected swaps, got 0")
	} else {
		s := plan.Swaps[0]
		t.Logf("Swap: %s -> %s, AmountIn: %s", s.FromToken.Hex(), s.ToToken.Hex(), s.AmountIn.String())
		if s.FromToken.Hex() != usdcAddr {
			t.Errorf("Expected Swap From USDC")
		}
		if s.ToToken.Hex() != ethAddr {
			t.Errorf("Expected Swap To ETH")
		}
		// Expect ~2500 USD used.
		// Total Equity 10k. Target 50% = 5000.
		// If ratio is 50/50, need 2500 USD of ETH.
		// 2500 USDC = 2500_000000.
		// Check amount roughly.
		amtInFloat := float64(s.AmountIn.Int64()) / 1e6
		if amtInFloat < 2000 || amtInFloat > 4000 {
			t.Errorf("Expected swap amount around 2500 USDC, got %.2f", amtInFloat)
		}
	}

	t.Logf("Final LP: T0=%s T1=%s", plan.FinalLP.Amount0, plan.FinalLP.Amount1)
}

func TestRebalance_StableMultiHopPath(t *testing.T) {
	// Scenario: Stable token is not pool token; expect multi-hop path via pool token.
	rebal := NewRebalancer()
	intent := strategy.Intent{
		ID:                "test-multihop",
		TargetNotionalPct: 0.5,
		Metadata: map[string]string{
			"lower_tick": "-201000",
			"upper_tick": "-199000",
		},
	}
	usdc := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	eth := "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	dai := "0x6B175474E89094C44Da98b954EedeAC495271d0F"
	bals := map[string]*big.Int{
		dai: new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e18)),
	}
	prices := map[string]float64{
		eth:  2000,
		usdc: 1,
		dai:  1,
	}
	input := RebalanceInput{
		Intent:        intent,
		WalletBalance: bals,
		Prices:        prices,
		PoolConfig: PoolConfig{
			PoolID:         "pool-eth-usdc",
			Token0:         eth,
			Token1:         usdc,
			Token0Decimals: 18,
			Token1Decimals: 6,
			Fee:            3000,
			MaxCapPct:      1.0,
			StableTokens:   []string{dai},
		},
		RiskLimits: RiskLimits{MinIdleCashPct: 0.1, MaxSwapSlippagePct: 0.01},
	}
	plan, err := rebal.Rebalance(context.Background(), input)
	if err != nil {
		t.Fatalf("Rebalance failed: %v", err)
	}
	if len(plan.Swaps) == 0 {
		t.Fatalf("expected swap")
	}
	if len(plan.Swaps[0].Path) != 3 {
		t.Fatalf("expected 3-hop path, got %d", len(plan.Swaps[0].Path))
	}
}
