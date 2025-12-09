package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Chains     []ChainConfig    `yaml:"chains"`
	Pools      []PoolConfig     `yaml:"pools"`
	Strategy   StrategyConfig   `yaml:"strategy"`
	Risk       RiskConfig       `yaml:"risk"`
	Monitoring MonitoringConfig `yaml:"monitoring"`
}

type ChainConfig struct {
	ID      int64  `yaml:"id"`
	Name    string `yaml:"name"`
	RPC     string `yaml:"rpc"`
	PrivKey string `yaml:"-"` // Loaded from env
}

type PoolConfig struct {
	ID            string `yaml:"id"`
	ChainID       int64  `yaml:"chain_id"`
	Token0        string `yaml:"token0"`
	Token1        string `yaml:"token1"`
	Fee           int    `yaml:"fee"`
	Address       string `yaml:"address"`
	MaxInvestment string `yaml:"max_investment"`
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

	// Validate or load data from ENV if needed
	return &cfg, nil
}
