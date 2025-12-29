package riskcontrol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

type DivergenceRejectSample struct {
	TsMS int64 `json:"ts_ms"`

	Key        string `json:"key"`
	IntentType string `json:"intent_type"`
	ChainID    int64  `json:"chain_id"`
	PoolID     string `json:"pool_id"`

	SourceAName      string  `json:"source_a_name"`
	SourceAPriceNorm float64 `json:"source_a_price_norm"`
	SourceATsMS      int64   `json:"source_a_ts_ms"`

	SourceBName      string  `json:"source_b_name"`
	SourceBPriceNorm float64 `json:"source_b_price_norm"`
	SourceBTsMS      int64   `json:"source_b_ts_ms"`

	DeviationBps int64 `json:"deviation_bps"`
	ThresholdBps int64 `json:"threshold_bps"`

	AgeAMS int64 `json:"age_a_ms"`
	AgeBMS int64 `json:"age_b_ms"`

	NormalizationDetail string `json:"normalization_detail"`
	Note                string `json:"note,omitempty"`
}

type DivergenceRejectSamplesReport struct {
	RunID     string `json:"run_id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`

	ThresholdBps int64 `json:"threshold_bps"`

	SamplesTopByDeviation []DivergenceRejectSample `json:"samples_top_by_deviation"`
	SamplesRecent         []DivergenceRejectSample `json:"samples_recent,omitempty"`
}

type DivergenceRejectCollector struct {
	mu sync.Mutex

	runID     string
	startedAt time.Time

	topN    int
	recentN int

	topByDeviation []DivergenceRejectSample
	recent         []DivergenceRejectSample
}

func NewDivergenceRejectCollector(topN, recentN int, runID string, startedAt time.Time) *DivergenceRejectCollector {
	if topN <= 0 {
		topN = 10
	}
	if recentN < 0 {
		recentN = 0
	}
	return &DivergenceRejectCollector{
		runID:     strings.TrimSpace(runID),
		startedAt: startedAt.UTC(),
		topN:      topN,
		recentN:   recentN,
	}
}

func (c *DivergenceRejectCollector) ObserveReject(intent contracts.Intent, ctx RiskContext, d RiskDecision, thresholdBps int64) {
	if c == nil {
		return
	}
	if strings.TrimSpace(d.RuleID) != PriceSourceDivergenceRuleID || d.Verdict != VerdictReject {
		return
	}

	nowMS := ctx.Now.UnixMilli()
	aKey := "onchain"
	bKey := "exchange"
	a := ctx.PriceSources[aKey]
	b := ctx.PriceSources[bKey]

	aNorm := a.NormalizedPrice
	if aNorm <= 0 {
		aNorm = a.Price
	}
	bNorm := b.NormalizedPrice
	if bNorm <= 0 {
		bNorm = b.Price
	}

	devBps, ok := parseInt64After(d.Reason, "deviation_bps=")
	if !ok {
		devBps = deviationBps(aNorm, bNorm)
	}
	thBps, ok := parseInt64After(d.Reason, "threshold_bps=")
	if ok {
		thresholdBps = thBps
	}

	key := fmt.Sprintf("phoenix|%s|chain=%d|pool=%s", string(intent.Type), intent.ChainID, strings.TrimSpace(intent.PoolID))
	note := ""
	if absI64((nowMS-a.TsMS)-(nowMS-b.TsMS)) > 10_000 {
		note = "possible_timestamp_mismatch"
	}
	if isFinitePositive(aNorm) && (aNorm < 1e-9 || aNorm > 1e9) {
		if note != "" {
			note += ";"
		}
		note += "suspicious_onchain_norm_magnitude"
	}
	if isFinitePositive(bNorm) && (bNorm < 1e-9 || bNorm > 1e9) {
		if note != "" {
			note += ";"
		}
		note += "suspicious_exchange_norm_magnitude"
	}

	// "normalization_detail" is intentionally compact and non-sensitive.
	detail := strings.TrimSpace(a.NormalizationDetail)
	if detail != "" {
		detail = "a:" + detail
	}
	if bDetail := strings.TrimSpace(b.NormalizationDetail); bDetail != "" {
		if detail != "" {
			detail += " | "
		}
		detail += "b:" + bDetail
	}

	s := DivergenceRejectSample{
		TsMS: nowMS,

		Key:        key,
		IntentType: string(intent.Type),
		ChainID:    intent.ChainID,
		PoolID:     strings.TrimSpace(intent.PoolID),

		SourceAName:      strings.TrimSpace(a.SourceName),
		SourceAPriceNorm: aNorm,
		SourceATsMS:      a.TsMS,

		SourceBName:      strings.TrimSpace(b.SourceName),
		SourceBPriceNorm: bNorm,
		SourceBTsMS:      b.TsMS,

		DeviationBps: devBps,
		ThresholdBps: thresholdBps,

		AgeAMS: nowMS - a.TsMS,
		AgeBMS: nowMS - b.TsMS,

		NormalizationDetail: detail,
		Note:                note,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.recentN > 0 {
		c.recent = append(c.recent, s)
		if len(c.recent) > c.recentN {
			c.recent = c.recent[len(c.recent)-c.recentN:]
		}
	}

	c.topByDeviation = append(c.topByDeviation, s)
	sort.Slice(c.topByDeviation, func(i, j int) bool {
		return c.topByDeviation[i].DeviationBps > c.topByDeviation[j].DeviationBps
	})
	if len(c.topByDeviation) > c.topN {
		c.topByDeviation = c.topByDeviation[:c.topN]
	}
}

func (c *DivergenceRejectCollector) Snapshot(thresholdBps int64, endedAt time.Time) DivergenceRejectSamplesReport {
	if c == nil {
		return DivergenceRejectSamplesReport{
			ThresholdBps: thresholdBps,
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	return DivergenceRejectSamplesReport{
		RunID:     c.runID,
		StartedAt: c.startedAt.UTC().Format(time.RFC3339),
		EndedAt:   endedAt.UTC().Format(time.RFC3339),

		ThresholdBps: thresholdBps,

		SamplesTopByDeviation: append([]DivergenceRejectSample(nil), c.topByDeviation...),
		SamplesRecent:         append([]DivergenceRejectSample(nil), c.recent...),
	}
}

func (c *DivergenceRejectCollector) WriteJSON(path string, thresholdBps int64, endedAt time.Time) error {
	if c == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	rep := c.Snapshot(thresholdBps, endedAt)
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *DivergenceRejectCollector) WriteTXT(path string, thresholdBps int64, endedAt time.Time) error {
	if c == nil || strings.TrimSpace(path) == "" {
		return nil
	}

	rep := c.Snapshot(thresholdBps, endedAt)
	lines := make([]string, 0, 256)
	lines = append(lines, "Phase 5.6 PriceSourceDivergence REJECT samples (TopN)")
	lines = append(lines, fmt.Sprintf("run_id=%s started_at=%s ended_at=%s threshold_bps=%d", rep.RunID, rep.StartedAt, rep.EndedAt, rep.ThresholdBps))
	lines = append(lines, "")

	appendSample := func(s DivergenceRejectSample) {
		lines = append(lines, fmt.Sprintf("deviation_bps=%d threshold_bps=%d key=%s", s.DeviationBps, s.ThresholdBps, s.Key))
		lines = append(lines, fmt.Sprintf("pool=%s chain_id=%d intent_type=%s", s.PoolID, s.ChainID, s.IntentType))
		lines = append(lines, fmt.Sprintf("onchain_norm=%.12g ts_ms=%d age_ms=%d name=%s", s.SourceAPriceNorm, s.SourceATsMS, s.AgeAMS, s.SourceAName))
		lines = append(lines, fmt.Sprintf("exchange_norm=%.12g ts_ms=%d age_ms=%d name=%s", s.SourceBPriceNorm, s.SourceBTsMS, s.AgeBMS, s.SourceBName))
		lines = append(lines, fmt.Sprintf("normalization_detail=%s", s.NormalizationDetail))
		lines = append(lines, "diagnostic_hint="+diagnosticHint(s))
		if strings.TrimSpace(s.Note) != "" {
			lines = append(lines, "note="+strings.TrimSpace(s.Note))
		}
		lines = append(lines, "")
	}

	if len(rep.SamplesTopByDeviation) == 0 {
		lines = append(lines, "no price_source_divergence REJECT samples recorded")
		lines = append(lines, "")
	} else {
		for _, s := range rep.SamplesTopByDeviation {
			appendSample(s)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func diagnosticHint(s DivergenceRejectSample) string {
	hints := []string{}
	if absI64(s.AgeAMS-s.AgeBMS) > 10_000 {
		hints = append(hints, "possible_timestamp_mismatch(age_diff_ms>10000)")
	}
	if isFinitePositive(s.SourceAPriceNorm) && isFinitePositive(s.SourceBPriceNorm) {
		if absI64(s.AgeAMS-s.AgeBMS) <= 10_000 {
			// When ages are close but prices are far, highlight semantic/market mismatch.
			ratio := s.SourceAPriceNorm / s.SourceBPriceNorm
			if ratio < 0 {
				ratio = -ratio
			}
			if ratio > 2 || ratio < 0.5 {
				hints = append(hints, "prices_far_but_ages_close(possible_semantics_or_market_mismatch)")
			}
		}
	}
	if isFinitePositive(s.SourceAPriceNorm) && (s.SourceAPriceNorm < 1e-9 || s.SourceAPriceNorm > 1e9) {
		hints = append(hints, "onchain_norm_extreme(check_direction_or_decimals)")
	}
	if isFinitePositive(s.SourceBPriceNorm) && (s.SourceBPriceNorm < 1e-9 || s.SourceBPriceNorm > 1e9) {
		hints = append(hints, "exchange_norm_extreme(check_direction_or_decimals)")
	}
	if len(hints) == 0 {
		return "no_obvious_signal"
	}
	return strings.Join(hints, ";")
}

func absI64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
