package advisory

import (
	"time"
)

// RiskAdvisory represents a read-only recommendation for the control plane.
// Phase 6.0: This NEVER writes to control.json, only outputs recommendations.
type RiskAdvisory struct {
	TsMS              int64            `json:"ts_ms"`
	WindowSec         int64            `json:"window_sec"`
	SuggestedRiskMode string           `json:"suggested_risk_mode"` // NO_CHANGE, SAFE, HALT
	Confidence        float64          `json:"confidence"`          // 0.0-1.0
	Reasons           []string         `json:"reasons"`
	Evidence          AdvisoryEvidence `json:"evidence"`
	Notes             string           `json:"notes,omitempty"`
}

// AdvisoryEvidence contains the statistical evidence supporting the advisory.
type AdvisoryEvidence struct {
	TotalEvaluations     int64    `json:"total_evaluations"`
	RejectRate           float64  `json:"reject_rate"`
	SkipRate             float64  `json:"skip_rate"`
	TimeMismatchSkipRate float64  `json:"time_mismatch_skip_rate"`
	DivergenceRejectRate float64  `json:"divergence_reject_rate"`
	CooldownRejectRate   float64  `json:"cooldown_reject_rate"`
	MaxDeviationBps      int64    `json:"max_deviation_bps"`
	P95DeviationBps      int64    `json:"p95_deviation_bps,omitempty"`
	TopKeys              []string `json:"top_keys,omitempty"`
}

// Advisory suggestion constants
const (
	SuggestionNoChange = "NO_CHANGE"
	SuggestionSafe     = "SAFE"
	SuggestionHalt     = "HALT"
)

// GenerateAdvisory creates a risk advisory from risk statistics.
// This is the core decision logic for Phase 6.0.
func GenerateAdvisory(cfg AdvisoryConfig, stats StatsSnapshot, now time.Time) RiskAdvisory {
	evidence := buildEvidence(stats)

	// Determine suggestion and reasons
	suggestion, reasons, confidence := evaluateRisk(cfg, evidence, stats)

	return RiskAdvisory{
		TsMS:              now.UnixMilli(),
		WindowSec:         cfg.WindowSec,
		SuggestedRiskMode: suggestion,
		Confidence:        confidence,
		Reasons:           reasons,
		Evidence:          evidence,
		Notes:             "",
	}
}

func buildEvidence(stats StatsSnapshot) AdvisoryEvidence {
	total := float64(stats.TotalEvaluations)
	if total == 0 {
		total = 1 // Avoid division by zero
	}

	rejectCount := stats.VerdictCounts["REJECT"]
	skipCount := stats.VerdictCounts["SKIP"]

	divRejectCount := stats.RejectCountsByRuleID["price_source_divergence"]
	cooldownRejectCount := stats.RejectCountsByRuleID["cooldown_frequency"]

	timeMismatchSkipCount := stats.TimeMismatchSkipCount

	// Top cooldown keys (up to 3)
	topKeys := []string{}
	for k := range stats.CooldownRejectCountByKey {
		topKeys = append(topKeys, k)
		if len(topKeys) >= 3 {
			break
		}
	}

	return AdvisoryEvidence{
		TotalEvaluations:     stats.TotalEvaluations,
		RejectRate:           float64(rejectCount) / total,
		SkipRate:             float64(skipCount) / total,
		TimeMismatchSkipRate: stats.TimeMismatchSkipRate,
		DivergenceRejectRate: float64(divRejectCount) / total,
		CooldownRejectRate:   float64(cooldownRejectCount) / total,
		MaxDeviationBps:      stats.PriceDivergence.MaxDeviationBps,
		P95DeviationBps:      stats.PriceDivergence.P95DeviationBps,
		TopKeys:              topKeys,
	}
}

func evaluateRisk(cfg AdvisoryConfig, ev AdvisoryEvidence, stats StatsSnapshot) (string, []string, float64) {
	reasons := []string{}
	conditionsMet := 0
	baseConfidence := 0.5

	// Check HALT conditions (highest priority)
	if ev.TotalEvaluations >= cfg.HaltMinEvaluations {
		// HALT condition 1: High divergence reject rate with significant deviation
		if ev.DivergenceRejectRate >= cfg.HaltDivergenceRejectRate && ev.MaxDeviationBps >= cfg.HaltMinDeviationBps {
			reasons = append(reasons, formatReason(
				"divergence_reject_rate", ev.DivergenceRejectRate, cfg.HaltDivergenceRejectRate,
				"max_deviation_bps", ev.MaxDeviationBps, cfg.HaltMinDeviationBps,
			))
			conditionsMet++
			return SuggestionHalt, reasons, calculateConfidence(baseConfidence, conditionsMet, ev.TotalEvaluations)
		}

		// HALT condition 2: High cooldown reject rate (strategy thrashing)
		if ev.CooldownRejectRate >= cfg.HaltCooldownRejectRate {
			reasons = append(reasons, formatReason(
				"cooldown_reject_rate", ev.CooldownRejectRate, cfg.HaltCooldownRejectRate,
				"total_evaluations", ev.TotalEvaluations, cfg.HaltMinEvaluations,
			))
			conditionsMet++
			return SuggestionHalt, reasons, calculateConfidence(baseConfidence, conditionsMet, ev.TotalEvaluations)
		}
	}

	// Check SAFE conditions (medium priority)
	if ev.TotalEvaluations >= cfg.SafeMinEvaluations {
		// SAFE condition 1: High time_mismatch skip rate (data sync issues)
		if ev.TimeMismatchSkipRate >= cfg.SafeTimeMismatchSkipRate {
			reasons = append(reasons, formatReason(
				"time_mismatch_skip_rate", ev.TimeMismatchSkipRate, cfg.SafeTimeMismatchSkipRate,
				"total_evaluations", ev.TotalEvaluations, cfg.SafeMinEvaluations,
			))
			conditionsMet++
			return SuggestionSafe, reasons, calculateConfidence(baseConfidence, conditionsMet, ev.TotalEvaluations)
		}

		// SAFE condition 2: High missing normalization skip rate
		missingNormSkipRate := 0.0
		if ev.TotalEvaluations > 0 {
			missingNormSkipCount := stats.SkipReasons["missing_decimals_for_normalization"]
			missingNormSkipRate = float64(missingNormSkipCount) / float64(ev.TotalEvaluations)
		}
		if missingNormSkipRate >= cfg.SafeMissingNormSkipRate {
			reasons = append(reasons, formatReason(
				"missing_normalization_skip_rate", missingNormSkipRate, cfg.SafeMissingNormSkipRate,
				"total_evaluations", ev.TotalEvaluations, cfg.SafeMinEvaluations,
			))
			conditionsMet++
			return SuggestionSafe, reasons, calculateConfidence(baseConfidence, conditionsMet, ev.TotalEvaluations)
		}
	}

	// Default: NO_CHANGE
	reasons = append(reasons, "no risk conditions triggered, system operating normally")
	return SuggestionNoChange, reasons, calculateConfidence(baseConfidence, 0, ev.TotalEvaluations)
}

func formatReason(metric1 string, val1 float64, threshold1 float64, metric2 string, val2 int64, threshold2 int64) string {
	if val2 > 0 {
		return formatReasonF(metric1, val1, threshold1) + ", " + formatReasonI(metric2, val2, threshold2)
	}
	return formatReasonF(metric1, val1, threshold1)
}

func formatReasonF(metric string, value, threshold float64) string {
	return formatString("%s=%.4f >= threshold %.4f", metric, value, threshold)
}

func formatReasonI(metric string, value, threshold int64) string {
	return formatString("%s=%d >= threshold %d", metric, value, threshold)
}

func formatString(format string, args ...interface{}) string {
	// Simple sprintf-like formatting
	result := format
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			result = replaceFirst(result, "%s", v)
		case int64:
			result = replaceFirst(result, "%d", formatInt64(v))
		case float64:
			result = replaceFirst(result, "%.4f", formatFloat64(v))
		}
	}
	return result
}

func replaceFirst(s, old, new string) string {
	// Simple string replacement (first occurrence)
	idx := 0
	for i := 0; i < len(s)-len(old)+1; i++ {
		if s[i:i+len(old)] == old {
			idx = i
			break
		}
	}
	if idx >= 0 && idx < len(s) {
		return s[:idx] + new + s[idx+len(old):]
	}
	return s
}

func formatInt64(v int64) string {
	// Simple int64 to string
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func formatFloat64(v float64) string {
	// Simple float64 to string with 4 decimal places
	intPart := int64(v)
	fracPart := int64((v - float64(intPart)) * 10000)
	if fracPart < 0 {
		fracPart = -fracPart
	}
	return formatInt64(intPart) + "." + padLeft(formatInt64(fracPart), 4, '0')
}

func padLeft(s string, width int, pad byte) string {
	if len(s) >= width {
		return s
	}
	padding := make([]byte, width-len(s))
	for i := range padding {
		padding[i] = pad
	}
	return string(padding) + s
}

func calculateConfidence(base float64, conditionsMet int, totalEvals int64) float64 {
	confidence := base

	// Increase confidence for each condition met
	confidence += float64(conditionsMet) * 0.1

	// Increase confidence for larger sample size
	if totalEvals >= 50 {
		confidence += 0.2
	} else if totalEvals >= 20 {
		confidence += 0.1
	}

	// Cap at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// StatsSnapshot is a minimal interface to avoid circular dependency
type StatsSnapshot struct {
	TotalEvaluations         int64
	VerdictCounts            map[string]int64
	RejectCountsByRuleID     map[string]int64
	SkipReasons              map[string]int64
	CooldownRejectCountByKey map[string]int64
	TimeMismatchSkipCount    int64
	TimeMismatchSkipRate     float64
	PriceDivergence          struct {
		MaxDeviationBps int64
		P95DeviationBps int64
	}
}
