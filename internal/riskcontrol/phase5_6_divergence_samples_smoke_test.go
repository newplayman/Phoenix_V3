package riskcontrol

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"phoenix-v3/internal/contracts"
)

func TestPhase56DivergenceSamplesSmoke(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	collector := NewDivergenceRejectCollector(10, 10, "smoke-run", now)

	mkCtx := func() RiskContext {
		return RiskContext{
			Now: now,
			PriceSources: map[string]PricePoint{
				"onchain": {
					SourceName:          "onchain_tick",
					RawPrice:            0,
					Price:               0,
					NormalizedPrice:     0.00031,
					NormalizationOK:     true,
					NormalizationDetail: "a:smoke",
					TsMS:                now.Add(-2 * time.Second).UnixMilli(),
				},
				"exchange": {
					SourceName:          "price_aggregator",
					RawPrice:            0,
					Price:               0,
					NormalizedPrice:     0.00033,
					NormalizationOK:     true,
					NormalizationDetail: "b:smoke",
					TsMS:                now.Add(-1 * time.Second).UnixMilli(),
				},
			},
		}
	}

	intent := contracts.Intent{ID: "i1", Type: contracts.IntentMockRebalance, ChainID: 11155111, PoolID: "tusd-weth-005"}
	threshold := int64(100)

	collector.ObserveReject(intent, mkCtx(), RiskDecision{
		Verdict: VerdictReject,
		RuleID:  PriceSourceDivergenceRuleID,
		Reason:  "price divergence too high deviation_bps=500 threshold_bps=100",
	}, threshold)
	collector.ObserveReject(intent, mkCtx(), RiskDecision{
		Verdict: VerdictReject,
		RuleID:  PriceSourceDivergenceRuleID,
		Reason:  "price divergence too high deviation_bps=900 threshold_bps=100",
	}, threshold)
	collector.ObserveReject(intent, mkCtx(), RiskDecision{
		Verdict: VerdictReject,
		RuleID:  PriceSourceDivergenceRuleID,
		Reason:  "price divergence too high deviation_bps=700 threshold_bps=100",
	}, threshold)

	// go test runs with CWD at the package directory; write artifacts relative to repo root.
	jsonPath := "../../artifacts/phase5_6_divergence_reject_samples.json"
	txtPath := "../../artifacts/phase5_6_divergence_reject_samples.txt"
	if err := collector.WriteJSON(jsonPath, threshold, now.Add(10*time.Second)); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if err := collector.WriteTXT(txtPath, threshold, now.Add(10*time.Second)); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	t.Logf("wrote %s", jsonPath)
	t.Logf("wrote %s", txtPath)

	b, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var rep DivergenceRejectSamplesReport
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(rep.SamplesTopByDeviation) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(rep.SamplesTopByDeviation))
	}
	if rep.SamplesTopByDeviation[0].DeviationBps != 900 || rep.SamplesTopByDeviation[1].DeviationBps != 700 || rep.SamplesTopByDeviation[2].DeviationBps != 500 {
		t.Fatalf("topN not sorted by deviation desc: %+v", rep.SamplesTopByDeviation)
	}
}
