package bot

import (
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/strategy"
)

func BuildStrategyConfig(cfg *config.AppConfig, pool config.PoolConfig) strategy.BasicStrategyConfig {
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
	sCfg.PoolID = pool.ID
	sCfg.ChainID = pool.ChainID
	sCfg.Token0Address = pool.Token0
	sCfg.Token1Address = pool.Token1
	sCfg.Token0Decimals = pool.Token0Decimals
	sCfg.Token1Decimals = pool.Token1Decimals
	sCfg.Fee = pool.Fee
	sCfg.PositionManager = pool.PositionManager
	sCfg.Amount0Desired = pool.Amount0
	sCfg.Amount1Desired = pool.Amount1
	sCfg.MaxCapPct = config.EffectiveMaxCapPct(pool.MaxCapPct, cfg.Risk.MaxUtilizationPct)
	sCfg.TickSpacing = strategy.TickSpacingForFee(pool.Fee)
	sCfg.TargetNotionalPct = sCfg.MaxCapPct
	if sCfg.ChainID == 0 && len(cfg.Chains) > 0 {
		sCfg.ChainID = cfg.Chains[0].ID
	}
	return sCfg
}

func BuildStrategyMapWithPolicy(cfg *config.AppConfig, policy *strategy.PolicyEngine) map[string]*strategy.BasicStrategy {
	result := make(map[string]*strategy.BasicStrategy)
	if cfg == nil {
		return result
	}
	for _, pool := range cfg.Pools {
		stratCfg := BuildStrategyConfig(cfg, pool)
		if policy != nil {
			stratCfg = policy.Apply(string(risk.ModeNormal), stratCfg)
		}
		result[pool.ID] = strategy.NewBasicStrategy(stratCfg)
	}
	return result
}

// BuildStrategyMap keeps backward compatibility for existing calls.
func BuildStrategyMap(cfg *config.AppConfig) map[string]*strategy.BasicStrategy {
	return BuildStrategyMapWithPolicy(cfg, strategy.NewPolicyEngine(nil))
}
