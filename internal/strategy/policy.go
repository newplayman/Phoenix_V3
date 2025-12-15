package strategy

import (
	"math"

	"phoenix-v3/internal/config"
)

// PolicyEngine maps RiskMode to StrategyProfile and applies it to a base config.
type PolicyEngine struct {
	profiles map[string]config.StrategyProfile
}

func NewPolicyEngine(profiles map[string]config.StrategyProfile) *PolicyEngine {
	if profiles == nil {
		profiles = map[string]config.StrategyProfile{}
	}
	return &PolicyEngine{profiles: profiles}
}

func (p *PolicyEngine) Profile(mode string) config.StrategyProfile {
	if p == nil {
		return config.StrategyProfile{}
	}
	if prof, ok := p.profiles[mode]; ok {
		return prof
	}
	if prof, ok := p.profiles["normal"]; ok {
		return prof
	}
	return config.StrategyProfile{TargetNotionalPctMultiplier: 1.0, MinSpreadTicksMultiplier: 1.0, EngineRiskFactor: 1.0}
}

func (p *PolicyEngine) EngineRiskFactor(mode string) float64 {
	prof := p.Profile(mode)
	if prof.EngineRiskFactor <= 0 {
		return 1.0
	}
	return prof.EngineRiskFactor
}

func (p *PolicyEngine) Apply(mode string, base BasicStrategyConfig) BasicStrategyConfig {
	prof := p.Profile(mode)
	base.RiskMode = mode
	base.TargetNotionalPct *= prof.TargetNotionalPctMultiplier
	base.MinSpreadTicks = int(math.Round(float64(base.MinSpreadTicks) * prof.MinSpreadTicksMultiplier))
	base.EngineRiskFactor = prof.EngineRiskFactor
	return base
}
