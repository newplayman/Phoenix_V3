package engine

import (
	"testing"
)

func TestPriceToTick(t *testing.T) {
	// 1.0001^0 = 1
	if tick := PriceToTick(1.0); tick != 0 {
		t.Errorf("Expected tick 0 for price 1.0, got %d", tick)
	}

	// 1.0001^276324 ~= 1000000000 (roughly) - let's test small numbers
	// log(1.0001) is approx 0.000099995

	// Test standard price, e.g. 2000
	tick2000 := PriceToTick(2000.0)
	if tick2000 < 75000 || tick2000 > 77000 {
		t.Errorf("Tick for 2000 seems off, got %d", tick2000)
	}
}

func TestStandardASMMEngine_Calculate(t *testing.T) {
	eng := NewStandardASMMEngine()

	input := EngineInput{
		CexPrice:   2000.0,
		DexPrice:   1995.0,
		Volatility: 0.02, // 2%
		Params: StrategyParams{
			RiskFactor: 1.0,
		},
	}

	out, err := eng.Calculate(input)
	if err != nil {
		t.Fatalf("Calculate failed: %v", err)
	}

	if out.TargetDelta != 0 {
		t.Logf("Delta: %f", out.TargetDelta)
	}

	// Fair price approx 1999.
	// Spread = 0.02 * 1.0 = 2%
	// Lower ~ 1959, Upper ~ 2038

	if out.TargetLowerTick >= out.TargetUpperTick {
		t.Error("Lower tick should be less than upper tick")
	}
}
