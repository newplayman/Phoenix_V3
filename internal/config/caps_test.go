package config

import "testing"

func TestEffectiveMaxCapPct(t *testing.T) {
	if got := EffectiveMaxCapPct(0, 0); got != 0.05 {
		t.Fatalf("expected default 0.05, got %f", got)
	}
	if got := EffectiveMaxCapPct(0.9, 0.2); got != 0.2 {
		t.Fatalf("expected clamp to global 0.2, got %f", got)
	}
	if got := EffectiveMaxCapPct(2.0, 0); got != 1.0 {
		t.Fatalf("expected clamp to 1.0, got %f", got)
	}
}
