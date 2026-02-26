package advisory

import (
	"fmt"
	"time"

	"phoenix-v3/internal/riskcontrol"
)

// StatsSnapshot is the input snapshot used to generate a risk advisory.
type StatsSnapshot = riskcontrol.RiskStatsSnapshot

// RiskAdvisory represents a read-only recommendation for the control plane.
// Phase 6+: This MUST NOT write to control.json; it only outputs recommendations.
type RiskAdvisory struct {
	TsMS              int64            `json:"ts_ms"`
	WindowSec         int64            `json:"window_sec"`
	SuggestedRiskMode string           `json:"suggested_risk_mode"` // NO_CHANGE, SAFE, HALT
	Confidence        float64          `json:"confidence"`          // 0.0-1.0
	Reasons           []string         `json:"reasons"`
	Evidence          AdvisoryEvidence `json:"evidence"`

	// Phase 6.3+: Hysteresis and severity
	StateSinceMs      int64          `json:"state_since_ts_ms"`
	InstantaneousMode string         `json:"instantaneous_mode"`
	SeverityScore     int            `json:"severity_score"`
	HysteresisCounts  map[string]int `json:"hysteresis_counts,omitempty"`
	Notes             string         `json:"notes,omitempty"`
}

// AdvisoryEvidence contains the statistical evidence supporting the advisory.
type AdvisoryEvidence struct {
	TotalEvaluations     int64   `json:"total_evaluations"`
	RejectRate           float64 `json:"reject_rate"`
	SkipRate             float64 `json:"skip_rate"`
	TimeMismatchSkipRate float64 `json:"time_mismatch_skip_rate"`
	DivergenceRejectRate float64 `json:"divergence_reject_rate"`
	CooldownRejectRate   float64 `json:"cooldown_reject_rate"`
	MaxDeviationBps      int64   `json:"max_deviation_bps"`
	P95DeviationBps      int64   `json:"p95_deviation_bps,omitempty"`
}

// Advisory suggestion constants.
const (
	SuggestionNoChange = "NO_CHANGE"
	SuggestionSafe     = "SAFE"
	SuggestionHalt     = "HALT"
)

func buildEvidence(stats StatsSnapshot) AdvisoryEvidence {
	total := stats.TotalEvaluations
	if total <= 0 {
		total = 1
	}

	get := func(m map[string]int64, k string) int64 {
		if m == nil {
			return 0
		}
		return m[k]
	}

	rejectN := get(stats.VerdictCounts, "REJECT")
	skipN := get(stats.VerdictCounts, "SKIP")

	divReject := get(stats.RejectCountsByRuleID, riskcontrol.PriceSourceDivergenceRuleID)
	cooldownReject := get(stats.RejectCountsByRuleID, riskcontrol.CooldownAndFrequencyRuleID)

	return AdvisoryEvidence{
		TotalEvaluations:     stats.TotalEvaluations,
		RejectRate:           float64(rejectN) / float64(total),
		SkipRate:             float64(skipN) / float64(total),
		TimeMismatchSkipRate: stats.TimeMismatchSkipRate,
		DivergenceRejectRate: float64(divReject) / float64(total),
		CooldownRejectRate:   float64(cooldownReject) / float64(total),
		MaxDeviationBps:      stats.PriceDivergence.MaxDeviationBps,
		P95DeviationBps:      stats.PriceDivergence.P95DeviationBps,
	}
}

func computeInstantaneousMode(cfg AdvisoryConfig, evidence AdvisoryEvidence) string {
	if evidence.TotalEvaluations >= cfg.HaltMinEvaluations {
		if evidence.DivergenceRejectRate >= cfg.HaltDivergenceRejectRate && evidence.MaxDeviationBps >= cfg.HaltMinDeviationBps {
			return SuggestionHalt
		}
		if evidence.CooldownRejectRate >= cfg.HaltCooldownRejectRate {
			return SuggestionHalt
		}
	}

	if evidence.TotalEvaluations >= cfg.SafeMinEvaluations {
		if evidence.TimeMismatchSkipRate >= cfg.SafeTimeMismatchSkipRate {
			return SuggestionSafe
		}
	}

	return SuggestionNoChange
}

func buildReasons(instantaneousMode, stableMode string, evidence AdvisoryEvidence, cfg AdvisoryConfig) ([]string, float64) {
	reasons := []string{}
	confidence := 0.50

	switch instantaneousMode {
	case SuggestionHalt:
		if evidence.DivergenceRejectRate >= cfg.HaltDivergenceRejectRate {
			reasons = append(reasons, fmt.Sprintf("divergence_reject_rate=%.4f >= %.4f", evidence.DivergenceRejectRate, cfg.HaltDivergenceRejectRate))
			confidence += 0.20
		}
		if evidence.MaxDeviationBps >= cfg.HaltMinDeviationBps {
			reasons = append(reasons, fmt.Sprintf("max_deviation_bps=%d >= %d", evidence.MaxDeviationBps, cfg.HaltMinDeviationBps))
			confidence += 0.20
		}
		if evidence.CooldownRejectRate >= cfg.HaltCooldownRejectRate {
			reasons = append(reasons, fmt.Sprintf("cooldown_reject_rate=%.4f >= %.4f", evidence.CooldownRejectRate, cfg.HaltCooldownRejectRate))
			confidence += 0.20
		}
	case SuggestionSafe:
		if evidence.TimeMismatchSkipRate >= cfg.SafeTimeMismatchSkipRate {
			reasons = append(reasons, fmt.Sprintf("time_mismatch_skip_rate=%.4f >= %.4f", evidence.TimeMismatchSkipRate, cfg.SafeTimeMismatchSkipRate))
			confidence += 0.15
		}
	default:
		reasons = append(reasons, "no risk conditions triggered, system operating normally")
	}

	if evidence.TotalEvaluations >= 100 {
		confidence += 0.10
	}
	if confidence > 1.0 {
		confidence = 1.0
	}

	if instantaneousMode != stableMode {
		reasons = append(reasons, fmt.Sprintf("stable_mode=%s (instantaneous=%s)", stableMode, instantaneousMode))
	}

	return reasons, confidence
}

// GenerateAdvisory creates a risk advisory from risk statistics.
func GenerateAdvisory(cfg AdvisoryConfig, stats StatsSnapshot, now time.Time, stateManager *AdvisoryStateManager) RiskAdvisory {
	evidence := buildEvidence(stats)
	instantaneousMode := computeInstantaneousMode(cfg, evidence)

	stableMode := instantaneousMode
	hysteresisReason := "hysteresis: disabled (no state manager)"
	stateSinceMs := now.UnixMilli()
	var hysteresisCounts map[string]int

	if stateManager != nil {
		stableMode, hysteresisReason = stateManager.UpdateState(instantaneousMode, cfg, now)
		state := stateManager.GetState()
		stateSinceMs = state.StateSinceMs
		hysteresisCounts = state.ConsecutiveTriggerCount
	}

	reasons, confidence := buildReasons(instantaneousMode, stableMode, evidence, cfg)
	reasons = append(reasons, hysteresisReason)

	severityScore, severityReason := CalculateSeverityScore(instantaneousMode, evidence)
	reasons = append(reasons, severityReason)

	return RiskAdvisory{
		TsMS:              now.UnixMilli(),
		WindowSec:         cfg.WindowSec,
		SuggestedRiskMode: stableMode,
		Confidence:        confidence,
		Reasons:           reasons,
		Evidence:          evidence,
		StateSinceMs:      stateSinceMs,
		InstantaneousMode: instantaneousMode,
		SeverityScore:     severityScore,
		HysteresisCounts:  hysteresisCounts,
	}
}
