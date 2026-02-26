package advisory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvisoryStateHysteresis(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewAdvisoryConfig()
	cfg.HaltUpN = 3
	cfg.HaltDownM = 5
	cfg.SafeUpN = 2
	cfg.SafeDownM = 4

	t.Run("Case1_SingleSpikeNoUpgrade", func(t *testing.T) {
		mgr := NewAdvisoryStateManager(tmpDir)
		if err := mgr.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		now := time.Unix(1700000000, 0).UTC()

		if mgr.GetState().CurrentMode != SuggestionNoChange {
			t.Fatalf("expected initial mode NO_CHANGE")
		}

		stableMode, _ := mgr.UpdateState(SuggestionHalt, cfg, now)
		if stableMode != SuggestionNoChange {
			t.Fatalf("expected mode NO_CHANGE after 1 HALT, got %s", stableMode)
		}
		state := mgr.GetState()
		if state.ConsecutiveTriggerCount[SuggestionHalt] != 1 {
			t.Fatalf("expected halt_count=1, got %d", state.ConsecutiveTriggerCount[SuggestionHalt])
		}
	})

	t.Run("Case2_ConsecutiveTriggersUpgrade", func(t *testing.T) {
		mgr := NewAdvisoryStateManager(tmpDir)
		if err := mgr.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		now := time.Unix(1700000000, 0).UTC()

		var stableMode string
		for i := 1; i <= 3; i++ {
			now = now.Add(time.Minute)
			stableMode, _ = mgr.UpdateState(SuggestionHalt, cfg, now)
			if i < 3 && stableMode != SuggestionNoChange {
				t.Fatalf("iter %d: expected NO_CHANGE, got %s", i, stableMode)
			}
		}
		if stableMode != SuggestionHalt {
			t.Fatalf("expected HALT after 3 consecutive triggers, got %s", stableMode)
		}
	})

	t.Run("Case3_DowngradeHarder", func(t *testing.T) {
		mgr := NewAdvisoryStateManager(tmpDir)
		if err := mgr.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		now := time.Unix(1700000000, 0).UTC()

		for i := 0; i < 3; i++ {
			now = now.Add(time.Minute)
			mgr.UpdateState(SuggestionHalt, cfg, now)
		}
		if mgr.GetState().CurrentMode != SuggestionHalt {
			t.Fatalf("expected HALT precondition")
		}

		for i := 1; i <= 4; i++ {
			now = now.Add(time.Minute)
			stableMode, _ := mgr.UpdateState(SuggestionNoChange, cfg, now)
			if stableMode != SuggestionHalt {
				t.Fatalf("iter %d: expected still HALT, got %s", i, stableMode)
			}
		}

		now = now.Add(time.Minute)
		stableMode, _ := mgr.UpdateState(SuggestionNoChange, cfg, now)
		if stableMode != SuggestionNoChange {
			t.Fatalf("expected downgrade to NO_CHANGE, got %s", stableMode)
		}
	})

	t.Run("StatePersistence", func(t *testing.T) {
		mgr := NewAdvisoryStateManager(tmpDir)
		if err := mgr.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		now := time.Unix(1700000000, 0).UTC()

		mgr.UpdateState(SuggestionHalt, cfg, now)
		mgr.UpdateState(SuggestionHalt, cfg, now.Add(time.Minute))
		if err := mgr.Save(); err != nil {
			t.Fatalf("save: %v", err)
		}

		statePath := filepath.Join(tmpDir, "risk_advisory_state.json")
		if _, err := os.Stat(statePath); err != nil {
			t.Fatalf("expected state file created at %s: %v", statePath, err)
		}

		mgr2 := NewAdvisoryStateManager(tmpDir)
		if err := mgr2.Load(); err != nil {
			t.Fatalf("load2: %v", err)
		}
		state := mgr2.GetState()
		if state.ConsecutiveTriggerCount[SuggestionHalt] != 2 {
			t.Fatalf("expected halt_count=2 after reload, got %d", state.ConsecutiveTriggerCount[SuggestionHalt])
		}
	})
}
