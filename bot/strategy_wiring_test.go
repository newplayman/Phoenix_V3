package bot

import (
	"testing"

	"phoenix-v3/internal/config"
)

func TestBuildStrategyConfig_UsesEffectiveCap(t *testing.T) {
	cfg := &config.AppConfig{
		StrategyVersion: "basic-v1",
		Chains:          []config.ChainConfig{{ID: 421614}},
		Risk:            config.RiskConfig{MaxUtilizationPct: 0.2},
	}
	pool := config.PoolConfig{ID: "p", ChainID: 421614, MaxCapPct: 0.9}
	got := BuildStrategyConfig(cfg, pool)
	if got.MaxCapPct != 0.2 {
		t.Fatalf("expected max cap 0.2, got %f", got.MaxCapPct)
	}
}
