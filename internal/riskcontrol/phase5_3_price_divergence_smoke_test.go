package riskcontrol

import (
	"strings"
	"testing"
	"time"

	"phoenix-v3/internal/contracts"
)

func TestPhase53PriceDivergenceSmoke(t *testing.T) {
	rule := NewPriceSourceDivergenceRule(PriceSourceDivergenceConfig{
		Enabled:         true,
		MaxDeviationBps: 100,
		MaxStaleness:    30 * time.Second,
		SourceA:         "onchain",
		SourceB:         "exchange",
	})
	intent := contracts.Intent{ID: "i1", Type: contracts.IntentSwap, ChainID: 1, PoolID: "p1"}

	now := time.UnixMilli(1_700_000_000_000).UTC()

	// 1) Low deviation -> APPROVE
	ctxOK := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"onchain":  {SourceName: "onchain_tick", Price: 100.00, TsMS: now.UnixMilli()},
			"exchange": {SourceName: "price_aggregator", Price: 100.50, TsMS: now.UnixMilli()},
		},
	}
	d1 := rule.Evaluate(intent, ctxOK)
	if d1.Verdict != VerdictApprove {
		t.Fatalf("expected approve, got %s reason=%s", d1.Verdict, d1.Reason)
	}
	t.Logf("LOW_DEV_APPROVE: %s", d1.Reason)

	// 2) High deviation -> REJECT, reason contains deviation_bps + threshold.
	ctxBad := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"onchain":  {SourceName: "onchain_tick", Price: 100.00, TsMS: now.UnixMilli()},
			"exchange": {SourceName: "price_aggregator", Price: 110.00, TsMS: now.UnixMilli()},
		},
	}
	d2 := rule.Evaluate(intent, ctxBad)
	if d2.Verdict != VerdictReject {
		t.Fatalf("expected reject, got %s reason=%s", d2.Verdict, d2.Reason)
	}
	if !strings.Contains(d2.Reason, "deviation_bps=") || !strings.Contains(d2.Reason, "threshold_bps=100") {
		t.Fatalf("reject reason missing fields: %s", d2.Reason)
	}
	t.Logf("HIGH_DEV_REJECT: %s", d2.Reason)

	// 3) Missing source -> SKIP (represented as APPROVE with skip reason).
	ctxMissing := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"exchange": {SourceName: "price_aggregator", Price: 100.00, TsMS: now.UnixMilli()},
		},
	}
	d3 := rule.Evaluate(intent, ctxMissing)
	if d3.Verdict != VerdictApprove || !strings.Contains(d3.Reason, "skip missing_source") {
		t.Fatalf("expected skip missing_source approve, got %s reason=%s", d3.Verdict, d3.Reason)
	}
	t.Logf("MISSING_SOURCE_SKIP: %s", d3.Reason)

	// 4) Stale source -> SKIP (represented as APPROVE with stale reason).
	staleTs := now.Add(-2 * time.Minute).UnixMilli()
	ctxStale := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"onchain":  {SourceName: "onchain_tick", Price: 100.00, TsMS: staleTs},
			"exchange": {SourceName: "price_aggregator", Price: 100.00, TsMS: now.UnixMilli()},
		},
	}
	d4 := rule.Evaluate(intent, ctxStale)
	if d4.Verdict != VerdictApprove || !strings.Contains(d4.Reason, "skip stale_source") {
		t.Fatalf("expected skip stale_source approve, got %s reason=%s", d4.Verdict, d4.Reason)
	}
	t.Logf("STALE_SOURCE_SKIP: %s", d4.Reason)
}
