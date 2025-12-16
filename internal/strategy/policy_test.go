package strategy

import (
	"testing"

	"phoenix-v3/internal/config"
)

func TestPolicyEngineApply(t *testing.T) {
	profiles := map[string]config.StrategyProfile{
		"normal": {
			TargetNotionalPctMultiplier: 1.0,
			MinSpreadTicksMultiplier:    1.0,
			EngineRiskFactor:            1.0,
		},
		"caution": {
			TargetNotionalPctMultiplier: 0.5,
			MinSpreadTicksMultiplier:    2.0,
			EngineRiskFactor:            0.7,
		},
	}

	engine := NewPolicyEngine(profiles)
	base := BasicStrategyConfig{TargetNotionalPct: 0.2, MinSpreadTicks: 100, EngineRiskFactor: 1.0}

	out := engine.Apply("caution", base)
	if out.RiskMode != "caution" {
		t.Fatalf("expected risk mode caution, got %s", out.RiskMode)
	}
	if out.TargetNotionalPct != 0.1 {
		t.Fatalf("expected target pct 0.1, got %f", out.TargetNotionalPct)
	}
	if out.MinSpreadTicks != 200 {
		t.Fatalf("expected min spread 200, got %d", out.MinSpreadTicks)
	}
	if out.EngineRiskFactor != 0.7 {
		t.Fatalf("expected engine risk factor 0.7, got %f", out.EngineRiskFactor)
	}
}

func TestPolicyEngineFallback(t *testing.T) {
	engine := NewPolicyEngine(map[string]config.StrategyProfile{
		"normal": {TargetNotionalPctMultiplier: 1.0, MinSpreadTicksMultiplier: 1.0, EngineRiskFactor: 1.0},
	})

	base := BasicStrategyConfig{TargetNotionalPct: 0.1, MinSpreadTicks: 100, EngineRiskFactor: 1.0}
	out := engine.Apply("unknown", base)
	if out.TargetNotionalPct != 0.1 {
		t.Fatalf("expected fallback to normal profile, got %f", out.TargetNotionalPct)
	}
}
