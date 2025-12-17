package bot

import (
	"math"
	"testing"

	"phoenix-v3/internal/config"
)

func TestComputeTargetWidthPct(t *testing.T) {
	cfg := &config.AppConfig{
		Strategy: config.StrategyConfig{
			Range: config.StrategyRangeConfig{
				MinWidthPct: 0.01,
				MaxWidthPct: 0.05,
				VolK:        0.1,
			},
		},
	}
	pool := config.PoolConfig{}
	profile := config.StrategyProfile{RangeWidthMultiplier: 2.0}

	if w, minW, maxW := ComputeTargetWidthPct(cfg, pool, profile, 0); w != 0.01 || minW != 0.01 || maxW != 0.05 {
		t.Fatalf("unexpected: w=%f min=%f max=%f", w, minW, maxW)
	}
	if w, _, _ := ComputeTargetWidthPct(cfg, pool, profile, 0.1); math.Abs(w-0.02) > 1e-12 {
		t.Fatalf("expected ~0.02 got %f", w)
	}
	if w, _, _ := ComputeTargetWidthPct(cfg, pool, profile, 10); w != 0.05 {
		t.Fatalf("expected clamp to 0.05 got %f", w)
	}
}
