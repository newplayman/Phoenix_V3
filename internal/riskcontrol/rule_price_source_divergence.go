package riskcontrol

import (
	"fmt"
	"math"
	"strings"
	"time"

	"phoenix-v3/internal/contracts"
)

const PriceSourceDivergenceRuleID = "price_source_divergence"

type PriceSourceDivergenceConfig struct {
	Enabled bool

	// MaxDeviationBps is the maximum allowed relative deviation in basis points.
	// Default: 100 bps = 1.00%.
	MaxDeviationBps int64

	// MaxStaleness is the maximum allowed age of each source snapshot.
	// Default: 30s.
	MaxStaleness time.Duration

	// AlignMaxGap is the maximum allowed timestamp gap between sources to be considered "same-time".
	// Default: 5s. If exceeded, we SKIP (approve with a "skip time_mismatch" reason) to avoid false rejects.
	AlignMaxGap time.Duration

	// SourceA/SourceB are the primary comparison keys in RiskContext.PriceSources.
	// Default: onchain vs exchange.
	SourceA string
	SourceB string
}

type PriceSourceDivergenceRule struct {
	cfg PriceSourceDivergenceConfig
}

func NewPriceSourceDivergenceRule(cfg PriceSourceDivergenceConfig) *PriceSourceDivergenceRule {
	if cfg.MaxDeviationBps <= 0 {
		cfg.MaxDeviationBps = 100
	}
	if cfg.MaxStaleness <= 0 {
		cfg.MaxStaleness = 30 * time.Second
	}
	if cfg.AlignMaxGap <= 0 {
		cfg.AlignMaxGap = 5 * time.Second
	}
	if strings.TrimSpace(cfg.SourceA) == "" {
		cfg.SourceA = "onchain"
	}
	if strings.TrimSpace(cfg.SourceB) == "" {
		cfg.SourceB = "exchange"
	}
	return &PriceSourceDivergenceRule{cfg: cfg}
}

func (r *PriceSourceDivergenceRule) RuleID() string { return PriceSourceDivergenceRuleID }

func (r *PriceSourceDivergenceRule) Evaluate(_ contracts.Intent, ctx RiskContext) RiskDecision {
	if r == nil {
		return RiskDecision{Verdict: VerdictApprove, RuleID: PriceSourceDivergenceRuleID, Reason: "nil rule"}
	}
	if !r.cfg.Enabled {
		return RiskDecision{Verdict: VerdictApprove, RuleID: PriceSourceDivergenceRuleID, Reason: "rule disabled"}
	}

	aKey := strings.TrimSpace(r.cfg.SourceA)
	bKey := strings.TrimSpace(r.cfg.SourceB)

	a, aOK := ctx.PriceSources[aKey]
	b, bOK := ctx.PriceSources[bKey]
	if !aOK || !bOK || a.TsMS <= 0 || b.TsMS <= 0 {
		miss := []string{}
		if !aOK || a.TsMS <= 0 {
			miss = append(miss, aKey)
		}
		if !bOK || b.TsMS <= 0 {
			miss = append(miss, bKey)
		}
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason:  "skip missing_source=" + strings.Join(miss, ","),
		}
	}

	// Phase 5.5: normalization gate. If either source cannot be normalized safely, SKIP to avoid bogus REJECT.
	aNorm := a.NormalizedPrice
	if aNorm <= 0 {
		aNorm = a.Price
	}
	bNorm := b.NormalizedPrice
	if bNorm <= 0 {
		bNorm = b.Price
	}
	if !a.NormalizationOK || !b.NormalizationOK || aNorm <= 0 || bNorm <= 0 {
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason: fmt.Sprintf(
				"skip missing_decimals_for_normalization source_a=%s ok_a=%v detail_a=%q source_b=%s ok_b=%v detail_b=%q",
				aKey, a.NormalizationOK, strings.TrimSpace(a.NormalizationDetail), bKey, b.NormalizationOK, strings.TrimSpace(b.NormalizationDetail),
			),
		}
	}

	nowMS := ctx.Now.UnixMilli()
	aAgeMS := nowMS - a.TsMS
	bAgeMS := nowMS - b.TsMS
	maxStaleMS := int64(r.cfg.MaxStaleness / time.Millisecond)
	if maxStaleMS > 0 && (aAgeMS > maxStaleMS || bAgeMS > maxStaleMS) {
		// Phase 5.3 choice: stale snapshots are treated as "not comparable" -> SKIP.
		// Rationale: a stale price might be transient (source reconnect), and other gates
		// (HALT/force-dry-run/cooldown) still protect the system; we avoid false positives.
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason: fmt.Sprintf(
				"skip stale_source source_a=%s age_ms=%d source_b=%s age_ms=%d max_staleness_sec=%d",
				aKey, aAgeMS, bKey, bAgeMS, int64(r.cfg.MaxStaleness/time.Second),
			),
		}
	}

	// Phase 5.7 (Path A): ensure the two prices are aligned in time before comparing.
	alignGapMS := int64(r.cfg.AlignMaxGap / time.Millisecond)
	tsGapMS := int64(0)
	if a.TsMS >= b.TsMS {
		tsGapMS = a.TsMS - b.TsMS
	} else {
		tsGapMS = b.TsMS - a.TsMS
	}
	if alignGapMS > 0 && tsGapMS > alignGapMS {
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason: fmt.Sprintf(
				"skip time_mismatch source_a=%s ts_a=%d age_ms=%d source_b=%s ts_b=%d age_ms=%d ts_gap_ms=%d align_max_gap_ms=%d",
				aKey, a.TsMS, aAgeMS, bKey, b.TsMS, bAgeMS, tsGapMS, alignGapMS,
			),
		}
	}

	devBps := deviationBps(aNorm, bNorm)
	if devBps > r.cfg.MaxDeviationBps {
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason: fmt.Sprintf(
				"price divergence too high normalized_ok=true source_a=%s raw_price_a=%.10f normalized_price_a=%.10f ts_a=%d normalized_a_detail=%q source_b=%s raw_price_b=%.10f normalized_price_b=%.10f ts_b=%d normalized_b_detail=%q deviation_bps=%d threshold_bps=%d",
				aKey, a.RawPrice, aNorm, a.TsMS, strings.TrimSpace(a.NormalizationDetail),
				bKey, b.RawPrice, bNorm, b.TsMS, strings.TrimSpace(b.NormalizationDetail),
				devBps, r.cfg.MaxDeviationBps,
			),
		}
	}

	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  PriceSourceDivergenceRuleID,
		Reason: fmt.Sprintf(
			"ok normalized_ok=true source_a=%s source_b=%s deviation_bps=%d threshold_bps=%d",
			aKey, bKey, devBps, r.cfg.MaxDeviationBps,
		),
	}
}

func deviationBps(p1, p2 float64) int64 {
	// deviation = abs(p1-p2) / max(min(p1,p2), eps)
	const eps = 1e-9
	if p1 <= 0 || p2 <= 0 {
		return math.MaxInt64
	}
	num := math.Abs(p1 - p2)
	den := math.Max(math.Min(p1, p2), eps)
	dev := num / den
	return int64(math.Round(dev * 10000.0))
}
