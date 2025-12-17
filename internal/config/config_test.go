package config

import "testing"

func minimalValidConfig() *AppConfig {
	return &AppConfig{
		SchemaVersion: CurrentSchemaVersion,
		Risk: RiskConfig{
			MaxDailyGas:      0.01,
			MaxDrawdown:      0.10,
			ConsecutiveFails: 3,
		},
		Chains: []ChainConfig{
			{ID: 421614, Name: "arbitrum-sepolia", RPC: "http://localhost:8545"},
		},
		Pools: []PoolConfig{
			{
				ID:              "pool",
				ChainID:         421614,
				Token0:          "0x0000000000000000000000000000000000000001",
				Token1:          "0x0000000000000000000000000000000000000002",
				CEXPriceToken:   "0x0000000000000000000000000000000000000002",
				Token0Decimals:  18,
				Token1Decimals:  6,
				Fee:             500,
				Address:         "0x0000000000000000000000000000000000000003",
				PositionManager: "0x0000000000000000000000000000000000000004",
				StableTokens: []string{
					"0x0000000000000000000000000000000000000001",
				},
				MaxInvestment: "0.5",
				MaxCapPct:     0.2,
			},
		},
	}
}

func TestValidateConfig_SetsSafetyDefaults(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Safety.KillSwitch = nil
	cfg.Safety.AllowTxBroadcast = nil
	cfg.Strategy.DryRun = nil

	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig: %v", err)
	}
	if cfg.Safety.KillSwitch == nil || *cfg.Safety.KillSwitch != true {
		t.Fatalf("expected kill_switch default true, got %#v", cfg.Safety.KillSwitch)
	}
	if cfg.Safety.AllowTxBroadcast == nil || *cfg.Safety.AllowTxBroadcast != false {
		t.Fatalf("expected allow_tx_broadcast default false, got %#v", cfg.Safety.AllowTxBroadcast)
	}
	if cfg.Strategy.DryRun == nil || *cfg.Strategy.DryRun != true {
		t.Fatalf("expected dry_run default true, got %#v", cfg.Strategy.DryRun)
	}

	s := SafetyFromConfig(cfg)
	if !s.EffectiveDryRun {
		t.Fatalf("expected EffectiveDryRun true by default, got %+v", s)
	}
}

func TestValidateConfig_TripleUnlockRequiredForBroadcast(t *testing.T) {
	cfg := minimalValidConfig()

	// allow_tx_broadcast=true requires kill_switch=false.
	cfg.Safety.AllowTxBroadcast = boolPtr(true)
	cfg.Safety.KillSwitch = boolPtr(true)
	cfg.Strategy.DryRun = boolPtr(false)
	if err := ValidateConfig(cfg); err == nil {
		t.Fatalf("expected error when allow_tx_broadcast=true with kill_switch=true")
	}

	// allow_tx_broadcast=true requires dry_run=false.
	cfg = minimalValidConfig()
	cfg.Safety.AllowTxBroadcast = boolPtr(true)
	cfg.Safety.KillSwitch = boolPtr(false)
	cfg.Strategy.DryRun = boolPtr(true)
	if err := ValidateConfig(cfg); err == nil {
		t.Fatalf("expected error when allow_tx_broadcast=true with dry_run=true")
	}

	// Triple-unlock passes.
	cfg = minimalValidConfig()
	cfg.Safety.AllowTxBroadcast = boolPtr(true)
	cfg.Safety.KillSwitch = boolPtr(false)
	cfg.Strategy.DryRun = boolPtr(false)
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error for triple-unlock, got %v", err)
	}
	s := SafetyFromConfig(cfg)
	if s.EffectiveDryRun {
		t.Fatalf("expected EffectiveDryRun false when explicitly unlocked, got %+v", s)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestValidateConfig_ArbitrumOneRequiresUnsafeOverride(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Chains[0].ID = 42161
	cfg.Pools[0].ChainID = 42161
	if err := ValidateConfig(cfg); err == nil {
		t.Fatalf("expected mainnet gate error for chainId=42161")
	}

	t.Setenv("PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE", "1")
	cfg = minimalValidConfig()
	cfg.Chains[0].ID = 42161
	cfg.Pools[0].ChainID = 42161
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected override to allow chainId=42161, got %v", err)
	}
}
