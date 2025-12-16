package bot

import (
	"context"
	"sync/atomic"
	"testing"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/config"
)

func TestCleanupController_BlockedWhenEffectiveDryRun(t *testing.T) {
	var cfgValue atomic.Value
	cfgValue.Store(&config.AppConfig{
		Strategy: config.StrategyConfig{DryRun: boolPtr(true)},
		Safety:   config.SafetyConfig{KillSwitch: boolPtr(true), AllowTxBroadcast: boolPtr(false)},
	})

	flags := &ControlFlags{}
	c := &CleanupController{
		Ctx:      context.Background(),
		Flags:    flags,
		Gateways: map[int64]*gateway.EthGateway(nil), // not used when blocked
		CfgValue: &cfgValue,
		Store:    nil,
	}
	if err := c.TriggerCleanup(); err == nil {
		t.Fatalf("expected blocked error")
	}
	if flags.CleanupInProgress() {
		t.Fatalf("expected cleanup not started")
	}
}

func boolPtr(v bool) *bool { return &v }
