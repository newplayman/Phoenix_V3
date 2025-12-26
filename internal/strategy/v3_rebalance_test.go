package strategy

import (
	"testing"
	"time"
)

func TestV3RebalanceAlignsToSpacing(t *testing.T) {
	s := NewV3RebalanceStrategy()
	cfg := V3RebalanceConfig{
		Enabled:         true,
		WidthTicks:      600,
		EdgeBufferTicks: 60,
		Cooldown:        0,
		MinTickMove:     30,
		TickSpacing:     60,
	}
	now := time.Unix(1700000000, 0).UTC()

	res, intent := s.EvaluateAt(cfg, now, V3RebalanceInput{
		ObservedAt:       now,
		PoolTick:         12345,
		CurrentLowerTick: 11000,
		CurrentUpperTick: 11600,
		AggPrice:         2000,
		RiskMode:         "normal",
	})
	if intent == nil {
		t.Fatalf("expected intent, got nil (res=%+v)", res)
	}
	if res.NewLower%cfg.TickSpacing != 0 || res.NewUpper%cfg.TickSpacing != 0 {
		t.Fatalf("new range not aligned to spacing: lower=%d upper=%d spacing=%d", res.NewLower, res.NewUpper, cfg.TickSpacing)
	}
	if (res.NewUpper - res.NewLower) != alignToSpacing(cfg.WidthTicks, cfg.TickSpacing) {
		t.Fatalf("width mismatch: got=%d want=%d", res.NewUpper-res.NewLower, alignToSpacing(cfg.WidthTicks, cfg.TickSpacing))
	}
}

func TestV3RebalanceOutOfRangePriority(t *testing.T) {
	s := NewV3RebalanceStrategy()
	cfg := V3RebalanceConfig{WidthTicks: 600, EdgeBufferTicks: 60, Cooldown: 0, MinTickMove: 30, TickSpacing: 60}
	now := time.Unix(1700000000, 0).UTC()

	res, intent := s.EvaluateAt(cfg, now, V3RebalanceInput{
		ObservedAt:       now,
		PoolTick:         14000,
		CurrentLowerTick: 12000,
		CurrentUpperTick: 12600,
	})
	if intent == nil || res.Action != "rebalance" || res.Reason != "out_of_range" {
		t.Fatalf("expected out_of_range rebalance, got action=%s reason=%s intent_nil=%v", res.Action, res.Reason, intent == nil)
	}
}

func TestV3RebalanceCooldownNoop(t *testing.T) {
	s := NewV3RebalanceStrategy()
	cfg := V3RebalanceConfig{WidthTicks: 600, EdgeBufferTicks: 60, Cooldown: 300 * time.Second, MinTickMove: 30, TickSpacing: 60}
	t0 := time.Unix(1700000000, 0).UTC()

	_, intent1 := s.EvaluateAt(cfg, t0, V3RebalanceInput{ObservedAt: t0, PoolTick: 12061, CurrentLowerTick: 12000, CurrentUpperTick: 12600})
	if intent1 == nil {
		t.Fatalf("expected first intent")
	}
	res2, intent2 := s.EvaluateAt(cfg, t0.Add(10*time.Second), V3RebalanceInput{ObservedAt: t0.Add(10 * time.Second), PoolTick: 12061, CurrentLowerTick: 12000, CurrentUpperTick: 12600})
	if intent2 != nil || res2.Action != "noop" || res2.Reason != "cooldown" {
		t.Fatalf("expected cooldown noop, got action=%s reason=%s intent_nil=%v", res2.Action, res2.Reason, intent2 == nil)
	}
	if res2.CooldownLeft <= 0 {
		t.Fatalf("expected cooldown_left>0, got %d", res2.CooldownLeft)
	}
}

func TestV3RebalanceNearEdgeTriggers(t *testing.T) {
	s := NewV3RebalanceStrategy()
	cfg := V3RebalanceConfig{WidthTicks: 600, EdgeBufferTicks: 60, Cooldown: 0, MinTickMove: 30, TickSpacing: 60}
	now := time.Unix(1700000000, 0).UTC()

	// within buffer of lower edge
	res, intent := s.EvaluateAt(cfg, now, V3RebalanceInput{ObservedAt: now, PoolTick: 12060, CurrentLowerTick: 12000, CurrentUpperTick: 12600})
	if intent == nil || res.Reason != "near_edge" {
		t.Fatalf("expected near_edge intent, got action=%s reason=%s", res.Action, res.Reason)
	}
}
