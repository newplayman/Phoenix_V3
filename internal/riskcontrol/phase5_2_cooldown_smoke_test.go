package riskcontrol

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phoenix-v3/internal/contracts"
)

func TestPhase52CooldownSmoke(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "risk_state.json")

	store := NewRiskStateStore(statePath)
	rule := NewCooldownAndFrequencyRule(store, CooldownConfig{
		Enabled:          true,
		Target:           "phoenix",
		MinInterval:      60 * time.Second,
		FailureThreshold: 3,
		CooldownFor:      300 * time.Second,
	})

	intent := contracts.Intent{
		ID:      "intent_1",
		Type:    contracts.IntentSwap,
		ChainID: 1,
		PoolID:  "pool_1",
	}
	key := CooldownKey("phoenix", intent)

	t0 := time.UnixMilli(1_700_000_000_000).UTC()

	// 1) First attempt approves.
	d1 := rule.Evaluate(intent, RiskContext{Now: t0})
	if d1.Verdict != VerdictApprove {
		t.Fatalf("expected approve, got %s reason=%s", d1.Verdict, d1.Reason)
	}
	t.Logf("APPROVE: verdict=%s reason=%s", d1.Verdict, d1.Reason)

	// 2) Second attempt within min interval rejects (reason includes remaining + min_interval).
	d2 := rule.Evaluate(intent, RiskContext{Now: t0.Add(10 * time.Second)})
	if d2.Verdict != VerdictReject {
		t.Fatalf("expected reject (min interval), got %s reason=%s", d2.Verdict, d2.Reason)
	}
	if !strings.Contains(d2.Reason, "min interval violation") ||
		!strings.Contains(d2.Reason, "cooldown_key="+key) ||
		!strings.Contains(d2.Reason, "remaining_ms=") ||
		!strings.Contains(d2.Reason, "min_interval_sec=60") {
		t.Fatalf("min interval reject reason missing fields: %s", d2.Reason)
	}
	t.Logf("MIN_INTERVAL_REJECT: %s", d2.Reason)

	// 3) Failure threshold triggers cooldown, and cooldown gate rejects.
	now := t0.Add(70 * time.Second)
	var untilMS int64
	for i := 0; i < 3; i++ {
		u, _, err := store.RecordFailure(now, key, 3, 300*time.Second)
		if err != nil {
			t.Fatalf("RecordFailure err: %v", err)
		}
		if u > 0 {
			untilMS = u
		}
	}
	if untilMS == 0 {
		t.Fatalf("expected cooldown_until_ts_ms to be set after threshold")
	}

	d3 := rule.Evaluate(intent, RiskContext{Now: now.Add(1 * time.Second)})
	if d3.Verdict != VerdictReject {
		t.Fatalf("expected reject (cooldown active), got %s reason=%s", d3.Verdict, d3.Reason)
	}
	if !strings.Contains(d3.Reason, "cooldown active") ||
		!strings.Contains(d3.Reason, "cooldown_key="+key) ||
		!strings.Contains(d3.Reason, "cooldown_until_ts_ms=") ||
		!strings.Contains(d3.Reason, "failure_count=") ||
		!strings.Contains(d3.Reason, "threshold=3") {
		t.Fatalf("cooldown reject reason missing fields: %s", d3.Reason)
	}
	t.Logf("FAILURE_COOLDOWN_REJECT: %s", d3.Reason)

	// 4) Persistence: reload store+rule and verify cooldown still rejects.
	store2 := NewRiskStateStore(statePath)
	rule2 := NewCooldownAndFrequencyRule(store2, CooldownConfig{
		Enabled:          true,
		Target:           "phoenix",
		MinInterval:      60 * time.Second,
		FailureThreshold: 3,
		CooldownFor:      300 * time.Second,
	})
	d4 := rule2.Evaluate(intent, RiskContext{Now: now.Add(2 * time.Second)})
	if d4.Verdict != VerdictReject {
		t.Fatalf("expected reject after reload, got %s reason=%s", d4.Verdict, d4.Reason)
	}
	t.Logf("PERSIST_RELOAD_REJECT: %s", d4.Reason)
}
