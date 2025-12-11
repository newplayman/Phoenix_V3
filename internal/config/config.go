package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const CurrentSchemaVersion = "2024-07-01"

type AppConfig struct {
	SchemaVersion   string           `yaml:"schema_version"`
	StrategyVersion string           `yaml:"strategy_version"`
	Events          EventConfig      `yaml:"events"`
	Chains          []ChainConfig    `yaml:"chains"`
	Pools           []PoolConfig     `yaml:"pools"`
	Strategy        StrategyConfig   `yaml:"strategy"`
	Risk            RiskConfig       `yaml:"risk"`
	Monitoring      MonitoringConfig `yaml:"monitoring"`
}

type EventConfig struct {
	Driver      string `yaml:"driver"`       // memory | redis
	RedisURL    string `yaml:"redis_url"`    // redis://user:pass@host:6379/0
	RedisPrefix string `yaml:"redis_prefix"` // default: phoenix
	RedisGroup  string `yaml:"redis_group"`  // default: phoenix-consumer
}

type ChainConfig struct {
	ID      int64  `yaml:"id"`
	Name    string `yaml:"name"`
	RPC     string `yaml:"rpc"`
	PrivKey string `yaml:"-"` // Loaded from env
}

type PoolConfig struct {
	ID              string `yaml:"id"`
	ChainID         int64  `yaml:"chain_id"`
	Token0          string `yaml:"token0"`
	Token1          string `yaml:"token1"`
	Fee             int    `yaml:"fee"`
	Address         string `yaml:"address"`
	MaxInvestment   string `yaml:"max_investment"`
	PositionManager string `yaml:"position_manager"`
	Amount0         string `yaml:"amount0"`
	Amount1         string `yaml:"amount1"`
	Token0Decimals  int    `yaml:"token0_decimals"`
	Token1Decimals  int    `yaml:"token1_decimals"`
}

type StrategyConfig struct {
	Name   string                 `yaml:"name"`
	Params map[string]interface{} `yaml:"params"`
	DryRun bool                   `yaml:"dry_run"`
}

type RiskConfig struct {
	MaxDailyGas      float64 `yaml:"max_daily_gas"`
	MaxDrawdown      float64 `yaml:"max_drawdown"`
	ConsecutiveFails int     `yaml:"consecutive_fails"`
}

type MonitoringConfig struct {
	Port int `yaml:"port"`
}

func LoadConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func ValidateConfig(cfg *AppConfig) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	if cfg.SchemaVersion != "" && cfg.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("schema version mismatch: expect %s got %s", CurrentSchemaVersion, cfg.SchemaVersion)
	}

	if len(cfg.Chains) == 0 {
		return errors.New("at least one chain must be configured")
	}
	for i, chain := range cfg.Chains {
		if chain.RPC == "" {
			return fmt.Errorf("chains[%d] missing rpc url", i)
		}
		if chain.ID == 0 {
			return fmt.Errorf("chains[%d] missing id", i)
		}
	}

	if len(cfg.Pools) == 0 {
		return errors.New("at least one pool must be configured")
	}
	for i, pool := range cfg.Pools {
		if pool.Token0Decimals <= 0 || pool.Token1Decimals <= 0 {
			return fmt.Errorf("pools[%d] token decimals must be > 0", i)
		}
	}

	if cfg.Risk.MaxDailyGas <= 0 {
		return errors.New("risk.max_daily_gas must be > 0")
	}

	if cfg.Risk.MaxDrawdown <= 0 || cfg.Risk.MaxDrawdown >= 1 {
		return errors.New("risk.max_drawdown must be between (0,1)")
	}

	if cfg.Risk.ConsecutiveFails <= 0 {
		return errors.New("risk.consecutive_fails must be > 0")
	}

	if cfg.Events.Driver == "" {
		cfg.Events.Driver = "memory"
	}
	switch cfg.Events.Driver {
	case "memory":
	case "redis":
		if cfg.Events.RedisURL == "" {
			return errors.New("events.redis_url required for redis driver")
		}
		if cfg.Events.RedisPrefix == "" {
			cfg.Events.RedisPrefix = "phoenix"
		}
		if cfg.Events.RedisGroup == "" {
			cfg.Events.RedisGroup = "phoenix-consumer"
		}
	default:
		return fmt.Errorf("unsupported events driver: %s", cfg.Events.Driver)
	}

	return nil
}
