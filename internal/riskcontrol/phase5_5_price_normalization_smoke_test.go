package riskcontrol

import (
	"strings"
	"testing"
	"time"

	"phoenix-v3/internal/contracts"
)

func TestPhase55PriceNormalizationSmoke(t *testing.T) {
	rule := NewPriceSourceDivergenceRule(PriceSourceDivergenceConfig{
		Enabled:         true,
		MaxDeviationBps: 100,
		MaxStaleness:    30 * time.Second,
		SourceA:         "onchain",
		SourceB:         "exchange",
	})
	intent := contracts.Intent{ID: "i1", Type: contracts.IntentSwap, ChainID: 1, PoolID: "p1"}
	now := time.UnixMilli(1_700_000_000_000).UTC()

	// 1) Same semantics: onchain=100 exchange=110 => deviation_bps=1000 (not astronomical).
	ctx := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"onchain": {
				SourceName:          "onchain_tick",
				RawPrice:            100.0,
				NormalizedPrice:     100.0,
				Price:               100.0,
				NormalizationOK:     true,
				NormalizationDetail: "smoke normalized=true",
				TsMS:                now.UnixMilli(),
			},
			"exchange": {
				SourceName:          "price_aggregator",
				RawPrice:            110.0,
				NormalizedPrice:     110.0,
				Price:               110.0,
				NormalizationOK:     true,
				NormalizationDetail: "smoke normalized=true",
				TsMS:                now.UnixMilli(),
			},
		},
	}
	d1 := rule.Evaluate(intent, ctx)
	if d1.Verdict != VerdictReject {
		t.Fatalf("expected reject, got %s reason=%s", d1.Verdict, d1.Reason)
	}
	if !strings.Contains(d1.Reason, "deviation_bps=1000") {
		t.Fatalf("expected deviation_bps=1000, got: %s", d1.Reason)
	}
	t.Logf("SCENARIO1_REJECT_OK: %s", d1.Reason)

	// 2) Missing decimals / unsafe normalization => SKIP (represented as APPROVE).
	ctxSkip := RiskContext{
		Now: now,
		PriceSources: map[string]PricePoint{
			"onchain": {
				SourceName:          "onchain_tick",
				RawPrice:            123.0,
				NormalizedPrice:     0,
				Price:               0,
				NormalizationOK:     false,
				NormalizationDetail: "missing_decimals_for_normalization",
				TsMS:                now.UnixMilli(),
			},
			"exchange": {
				SourceName:          "price_aggregator",
				RawPrice:            110.0,
				NormalizedPrice:     110.0,
				Price:               110.0,
				NormalizationOK:     true,
				NormalizationDetail: "smoke normalized=true",
				TsMS:                now.UnixMilli(),
			},
		},
	}
	d2 := rule.Evaluate(intent, ctxSkip)
	if d2.Verdict != VerdictApprove || !strings.Contains(d2.Reason, "skip missing_decimals_for_normalization") {
		t.Fatalf("expected skip missing_decimals_for_normalization approve, got %s reason=%s", d2.Verdict, d2.Reason)
	}
	if strings.Contains(d2.Reason, "deviation_bps=") {
		t.Fatalf("skip reason must not include deviation_bps: %s", d2.Reason)
	}
	t.Logf("SCENARIO2_SKIP_OK: %s", d2.Reason)

	// 3) Realistic normalization: the Phase 5.4 pool produced tick=-80666 and an astronomical raw price
	// due to decimals mismatch. With Phase 5.5 normalization (token1_per_token0), we should recover a
	// reasonable normalized price close to 1/exchange.
	ex := NormalizeExchangePriceToken1PerToken0(3000.0) // token0/token1 -> token1/token0
	on := NormalizeOnchainTickToken1PerToken0(-80666, 6, 18, 3.184965e15, ex.NormalizedPrice)
	if !on.NormalizationOK {
		t.Fatalf("expected normalization ok, got detail=%s", on.NormalizationDetail)
	}
	if on.NormalizedPrice < 1e-6 || on.NormalizedPrice > 1e-2 {
		t.Fatalf("normalized price out of expected range: %g detail=%s", on.NormalizedPrice, on.NormalizationDetail)
	}
	t.Logf("SCENARIO3_NORM_OK: exchange_norm=%g onchain_norm=%g detail=%s", ex.NormalizedPrice, on.NormalizedPrice, on.NormalizationDetail)
}
