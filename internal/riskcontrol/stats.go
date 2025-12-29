package riskcontrol

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

type PriceDivergenceStats struct {
	SampleCount     int64   `json:"sample_count"`
	RejectCount     int64   `json:"reject_count"`
	MaxDeviationBps int64   `json:"max_deviation_bps"`
	AvgDeviationBps float64 `json:"avg_deviation_bps"`
	P50DeviationBps int64   `json:"p50_deviation_bps,omitempty"`
	P95DeviationBps int64   `json:"p95_deviation_bps,omitempty"`
}

type RiskStatsSnapshot struct {
	UpdatedAtMS int64 `json:"updated_at_ms"`

	TotalEvaluations int64            `json:"total_evaluations"`
	VerdictCounts    map[string]int64 `json:"verdict_counts"`

	RuleCounts           map[string]int64 `json:"rule_counts"`
	RejectCountsByRuleID map[string]int64 `json:"reject_counts_by_rule_id"`
	SkipCountsByRuleID   map[string]int64 `json:"skip_counts_by_rule_id"`
	SkipReasons          map[string]int64 `json:"skip_reasons"` // missing_source / stale_source / other

	CooldownRejectCountByKey map[string]int64 `json:"cooldown_reject_count_by_key"`

	PriceDivergence PriceDivergenceStats `json:"price_divergence_stats"`
}

type RiskStats struct {
	mu sync.Mutex

	path       string
	lastSaveAt time.Time

	totalEvaluations int64
	verdictCounts    map[string]int64

	ruleCounts           map[string]int64
	rejectCountsByRuleID map[string]int64
	skipCountsByRuleID   map[string]int64
	skipReasons          map[string]int64

	cooldownRejectCountByKey map[string]int64

	divSamples []int64
	divMax     int64
	divSum     int64
	divReject  int64
}

func NewRiskStats(path string) *RiskStats {
	return &RiskStats{
		path:                     path,
		verdictCounts:            map[string]int64{},
		ruleCounts:               map[string]int64{},
		rejectCountsByRuleID:     map[string]int64{},
		skipCountsByRuleID:       map[string]int64{},
		skipReasons:              map[string]int64{},
		cooldownRejectCountByKey: map[string]int64{},
		divSamples:               make([]int64, 0, 2048),
		divMax:                   0,
		divSum:                   0,
		divReject:                0,
	}
}

func (s *RiskStats) ObserveEvaluation(intent contracts.Intent, ctx RiskContext, ev Evaluation) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalEvaluations++

	evalVerdict := string(ev.FinalVerdict)
	if evalVerdict == "" {
		evalVerdict = string(VerdictApprove)
	}

	// Phase 5.4: treat "skip" decisions as SKIP verdict at evaluation level when no REJECT.
	if ev.FinalVerdict != VerdictReject && hasSkipDecision(ev) {
		evalVerdict = "SKIP"
	}
	s.verdictCounts[evalVerdict]++

	for _, d := range ev.Decisions {
		ruleID := strings.TrimSpace(d.RuleID)
		if ruleID == "" {
			ruleID = "unknown_rule"
		}
		s.ruleCounts[ruleID]++

		if d.Verdict == VerdictReject {
			s.rejectCountsByRuleID[ruleID]++
			if ruleID == CooldownAndFrequencyRuleID {
				if key := parseCooldownKey(d.Reason); key != "" {
					s.cooldownRejectCountByKey[key]++
				}
			}
		}

		if isSkipDecision(d) {
			s.skipCountsByRuleID[ruleID]++
			s.skipReasons[skipReasonKind(d.Reason)]++
		}

		if ruleID == PriceSourceDivergenceRuleID {
			if dev, ok := parseInt64After(d.Reason, "deviation_bps="); ok {
				s.observeDeviationBps(dev)
			}
			if d.Verdict == VerdictReject {
				s.divReject++
			}
		}
	}
}

func (s *RiskStats) Snapshot(now time.Time) RiskStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := RiskStatsSnapshot{
		UpdatedAtMS:              now.UnixMilli(),
		TotalEvaluations:         s.totalEvaluations,
		VerdictCounts:            copyMapI64(s.verdictCounts),
		RuleCounts:               copyMapI64(s.ruleCounts),
		RejectCountsByRuleID:     copyMapI64(s.rejectCountsByRuleID),
		SkipCountsByRuleID:       copyMapI64(s.skipCountsByRuleID),
		SkipReasons:              copyMapI64(s.skipReasons),
		CooldownRejectCountByKey: copyMapI64(s.cooldownRejectCountByKey),
	}

	ps := PriceDivergenceStats{
		SampleCount:     int64(len(s.divSamples)),
		RejectCount:     s.divReject,
		MaxDeviationBps: s.divMax,
	}
	if len(s.divSamples) > 0 {
		ps.AvgDeviationBps = float64(s.divSum) / float64(len(s.divSamples))
		cpy := append([]int64(nil), s.divSamples...)
		sort.Slice(cpy, func(i, j int) bool { return cpy[i] < cpy[j] })
		ps.P50DeviationBps = quantileSortedI64(cpy, 0.50)
		ps.P95DeviationBps = quantileSortedI64(cpy, 0.95)
	}
	out.PriceDivergence = ps
	return out
}

func (s *RiskStats) SaveIfDue(now time.Time, minInterval time.Duration) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if minInterval <= 0 {
		minInterval = 2 * time.Second
	}

	s.mu.Lock()
	due := s.lastSaveAt.IsZero() || now.Sub(s.lastSaveAt) >= minInterval
	s.mu.Unlock()
	if !due {
		return nil
	}
	return s.Save(now)
}

func (s *RiskStats) Save(now time.Time) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}

	snap := s.Snapshot(now)
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.lastSaveAt = now
	s.mu.Unlock()
	return nil
}

func (s *RiskStats) observeDeviationBps(v int64) {
	if v < 0 {
		v = 0
	}
	if v > s.divMax {
		s.divMax = v
	}
	s.divSum += v
	if len(s.divSamples) < 8192 {
		s.divSamples = append(s.divSamples, v)
		return
	}
	// Bounded memory: simple ring overwrite.
	idx := int(s.totalEvaluations % int64(len(s.divSamples)))
	s.divSum -= s.divSamples[idx]
	s.divSamples[idx] = v
	s.divSum += v
}

func hasSkipDecision(ev Evaluation) bool {
	for _, d := range ev.Decisions {
		if isSkipDecision(d) {
			return true
		}
	}
	return false
}

func isSkipDecision(d RiskDecision) bool {
	// SKIP is encoded as APPROVE with a "skip ..." reason in Phase 5.3.
	if strings.TrimSpace(d.Reason) == "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(d.Reason), "skip")
}

func skipReasonKind(reason string) string {
	r := strings.TrimSpace(reason)
	switch {
	case strings.Contains(r, "missing_source"):
		return "missing_source"
	case strings.Contains(r, "missing_decimals_for_normalization"):
		return "missing_decimals_for_normalization"
	case strings.Contains(r, "time_mismatch"):
		return "time_mismatch"
	case strings.Contains(r, "stale_source"):
		return "stale_source"
	default:
		return "other"
	}
}

func parseCooldownKey(reason string) string {
	// Look for "cooldown_key=...".
	key, _ := parseStringAfter(reason, "cooldown_key=")
	return key
}

func parseStringAfter(s, marker string) (string, bool) {
	i := strings.Index(s, marker)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(marker):]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	// Token ends at whitespace.
	end := len(rest)
	for j, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			end = j
			break
		}
	}
	return rest[:end], true
}

func parseInt64After(s, marker string) (int64, bool) {
	raw, ok := parseStringAfter(s, marker)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func copyMapI64(in map[string]int64) map[string]int64 {
	if in == nil {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func quantileSortedI64(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	i := int(math.Floor(pos))
	j := int(math.Ceil(pos))
	if i == j {
		return sorted[i]
	}
	return sorted[i]
}
