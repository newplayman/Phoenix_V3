package bot

import (
	"testing"
	"time"
)

func TestPerPoolDailyLimiter(t *testing.T) {
	var l PerPoolDailyLimiter
	now := time.Date(2025, 12, 16, 10, 0, 0, 0, time.UTC)

	if !l.AllowAt("pool", 2, now) {
		t.Fatal("expected allow 1")
	}
	if !l.AllowAt("pool", 2, now) {
		t.Fatal("expected allow 2")
	}
	if l.AllowAt("pool", 2, now) {
		t.Fatal("expected deny at limit")
	}

	// next day resets
	next := now.Add(24 * time.Hour)
	if !l.AllowAt("pool", 2, next) {
		t.Fatal("expected allow after day rollover")
	}
}

func TestLastActionTracker(t *testing.T) {
	var tr LastActionTracker
	at := time.Unix(1_700_000_000, 0).UTC()
	tr.Set("pool", at)
	got, ok := tr.Get("pool")
	if !ok {
		t.Fatal("expected ok")
	}
	if !got.Equal(at) {
		t.Fatalf("expected %s got %s", at, got)
	}
}
