package engine

import (
	"testing"
)

func TestPriceToTick(t *testing.T) {
	tickForOne := PriceToTick(1.0)
	if tickForOne >= 0 {
		t.Errorf("expected negative tick for price=1.0 (due to decimals), got %d", tickForOne)
	}

	tick2000 := PriceToTick(2000.0)
	if tick2000 >= tickForOne {
		t.Errorf("tick should decrease with price: tick(2000)=%d tick(1)=%d", tick2000, tickForOne)
	}

	// PriceToTickRaw skips decimal shift; 1.0 raw should map to 0
	if tick := PriceToTickRaw(1.0); tick != 0 {
		t.Errorf("expected raw price 1 to map to tick 0, got %d", tick)
	}

	// With decimals: higher price -> lower tick (since rawPrice is inverted).
	t1 := PriceToTickWithDecimals(1000, 6, 18)
	t2 := PriceToTickWithDecimals(2000, 6, 18)
	if t2 >= t1 {
		t.Fatalf("expected tick(2000) < tick(1000), got %d vs %d", t2, t1)
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

	if out.TargetLowerTick == out.TargetUpperTick {
		t.Error("expected different ticks")
	}
}
