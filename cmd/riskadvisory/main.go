package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultStatsPath    = "/root/Phoenix_V3/var/risk_stats.json"
	defaultAdvisoryPath = "/root/Phoenix_V3/var/risk_advisory.json"
)

type riskStatsSnapshot struct {
	TotalEvaluations int64            `json:"total_evaluations"`
	RejectCountsByID map[string]int64 `json:"reject_counts_by_rule_id"`

	// Some producers may include these directly (best-effort).
	DivergenceRejectRate float64 `json:"divergence_reject_rate"`
	TimeMismatchSkipRate float64 `json:"time_mismatch_skip_rate"`

	PriceDivergence struct {
		MaxDeviationBps int64 `json:"max_deviation_bps"`
	} `json:"price_divergence_stats"`
}

type advisoryEvidence struct {
	TotalEvaluations     int64   `json:"total_evaluations"`
	DivergenceRejectRate float64 `json:"divergence_reject_rate"`
	TimeMismatchSkipRate float64 `json:"time_mismatch_skip_rate"`
	MaxDeviationBps      int64   `json:"max_deviation_bps"`
}

type riskAdvisory struct {
	TsMS              int64            `json:"ts_ms"`
	WindowSec         int64            `json:"window_sec"`
	SuggestedRiskMode string           `json:"suggested_risk_mode"`
	Confidence        float64          `json:"confidence"`
	Reasons           []string         `json:"reasons"`
	Evidence          advisoryEvidence `json:"evidence"`

	StateSinceMs      int64          `json:"state_since_ts_ms"`
	InstantaneousMode string         `json:"instantaneous_mode"`
	SeverityScore     int            `json:"severity_score"`
	HysteresisCounts  map[string]int `json:"hysteresis_counts,omitempty"`
}

func main() {
	statsPath := stringsTrimSpace(os.Getenv("PHOENIX_RISK_STATS_FILE"))
	if statsPath == "" {
		statsPath = defaultStatsPath
	}
	outPath := stringsTrimSpace(os.Getenv("PHOENIX_RISK_ADVISORY_FILE"))
	if outPath == "" {
		outPath = defaultAdvisoryPath
	}

	now := time.Now().UTC()
	stats, err := readStatsOrDefault(statsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: read stats: %v\n", err)
		os.Exit(2)
	}

	divRate := computeDivergenceRejectRate(stats)
	mode, severity := classify(divRate)

	reasons := []string{fmt.Sprintf("divergence_reject_rate=%.4f", divRate)}
	out := riskAdvisory{
		TsMS:              now.UnixMilli(),
		WindowSec:         3600,
		SuggestedRiskMode: mode,
		Confidence:        0.75,
		Reasons:           reasons,
		Evidence: advisoryEvidence{
			TotalEvaluations:     stats.TotalEvaluations,
			DivergenceRejectRate: divRate,
			TimeMismatchSkipRate: stats.TimeMismatchSkipRate,
			MaxDeviationBps:      stats.PriceDivergence.MaxDeviationBps,
		},
		StateSinceMs:      now.UnixMilli(),
		InstantaneousMode: mode,
		SeverityScore:     severity,
		HysteresisCounts: map[string]int{
			"HALT":      0,
			"SAFE":      0,
			"NO_CHANGE": 0,
		},
	}

	if err := writeJSONAtomic(outPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write advisory: %v\n", err)
		os.Exit(3)
	}
}

func stringsTrimSpace(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\n' || s[j-1] == '\r' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func readStatsOrDefault(path string) (riskStatsSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return riskStatsSnapshot{
				TotalEvaluations: 100,
				RejectCountsByID: map[string]int64{
					"price_source_divergence": 0,
				},
			}, nil
		}
		return riskStatsSnapshot{}, err
	}
	if len(b) == 0 {
		return riskStatsSnapshot{
			TotalEvaluations: 100,
			RejectCountsByID: map[string]int64{
				"price_source_divergence": 0,
			},
		}, nil
	}
	var s riskStatsSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return riskStatsSnapshot{}, err
	}
	if s.TotalEvaluations <= 0 {
		s.TotalEvaluations = 100
	}
	if s.RejectCountsByID == nil {
		s.RejectCountsByID = map[string]int64{}
	}
	return s, nil
}

func computeDivergenceRejectRate(stats riskStatsSnapshot) float64 {
	if stats.DivergenceRejectRate > 0 {
		return stats.DivergenceRejectRate
	}
	total := stats.TotalEvaluations
	if total <= 0 {
		return 0
	}
	divReject := stats.RejectCountsByID["price_source_divergence"]
	if divReject <= 0 {
		return 0
	}
	return float64(divReject) / float64(total)
}

func classify(divRate float64) (mode string, severity int) {
	switch {
	case divRate >= 0.20:
		return "HALT", 90
	case divRate >= 0.05:
		return "SAFE", 50
	default:
		return "NO_CHANGE", 15
	}
}

func writeJSONAtomic(path string, v any, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
