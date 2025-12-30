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

	// Phase 6.3: Hysteresis and severity
	StateSinceMs     int64            `json:"state_since_ts_ms"`
	InstantaneousMode string          `json:"instantaneous_mode"`
	SeverityScore    int              `json:"severity_score"`
	HysteresisCounts map[string]int   `json:"hysteresis_counts,omitempty"`
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


// computeInstantaneousMode calculates the instantaneous advisory mode without hysteresis

// buildReasons generates explainable reasons for the advisory
func buildReasons(instantaneousMode, stableMode string, evidence AdvisoryEvidence, stats StatsSnapshot, cfg AdvisoryConfig) ([]string, float64) {
reasons := []string{}
confidence := 0.5

// Instantaneous conditions
if instantaneousMode == SuggestionHalt {
ce.DivergenceRejectRate >= cfg.HaltDivergenceRejectRate {
s = append(reasons, formatReasonF("divergence_reject_rate=%.2f >= threshold %.2f", 
ce.DivergenceRejectRate, cfg.HaltDivergenceRejectRate))
fidence += 0.2
ce.MaxDeviationBps >= cfg.HaltMinDeviationBps {
s = append(reasons, formatReasonF("max_deviation_bps=%d >= threshold %d",
ce.MaxDeviationBps, cfg.HaltMinDeviationBps))
fidence += 0.2
ce.CooldownRejectRate >= cfg.HaltCooldownRejectRate {
s = append(reasons, formatReasonF("cooldown_reject_rate=%.2f >= threshold %.2f",
ce.CooldownRejectRate, cfg.HaltCooldownRejectRate))
fidence += 0.2
if instantaneousMode == SuggestionSafe {
ce.TimeMismatchSkipRate >= cfg.SafeTimeMismatchSkipRate {
s = append(reasons, formatReasonF("time_mismatch_skip_rate=%.2f >= threshold %.2f",
ce.TimeMismatchSkipRate, cfg.SafeTimeMismatchSkipRate))
fidence += 0.15
ce.MissingNormSkipRate >= cfg.SafeMissingNormSkipRate {
s = append(reasons, formatReasonF("missing_norm_skip_rate=%.2f >= threshold %.2f",
ce.MissingNormSkipRate, cfg.SafeMissingNormSkipRate))
fidence += 0.15
{
s = append(reasons, "no risk conditions triggered, system operating normally")
}

// Sample size contribution
if evidence.TotalEvaluations >= 100 {
fidence += 0.1
}

if confidence > 1.0 {
fidence = 1.0
}

return reasons, confidence
}


func computeInstantaneousMode(cfg AdvisoryConfig, evidence AdvisoryEvidence, stats StatsSnapshot) string {
// HALT conditions (Phase 6.0 logic preserved)
if evidence.TotalEvaluations >= cfg.HaltMinEvaluations {
ce.DivergenceRejectRate >= cfg.HaltDivergenceRejectRate && evidence.MaxDeviationBps >= cfg.HaltMinDeviationBps {
 SuggestionHalt
ce.CooldownRejectRate >= cfg.HaltCooldownRejectRate {
 SuggestionHalt
conditions
if evidence.TotalEvaluations >= cfg.SafeMinEvaluations {
ce.TimeMismatchSkipRate >= cfg.SafeTimeMismatchSkipRate {
 SuggestionSafe
ce.MissingNormSkipRate >= cfg.SafeMissingNormSkipRate {
 SuggestionSafe
 SuggestionNoChange
}



// GenerateAdvisory creates a risk advisory from risk statistics.
// This is the core decision logic for Phase 6.0.
func GenerateAdvisory(cfg AdvisoryConfig, stats StatsSnapshot, now time.Time, stateManager *AdvisoryStateManager) RiskAdvisory {
	evidence := buildEvidence(stats)

	// Phase 6.3: Compute instantaneous mode (Phase 6.0 logic)
	instantaneousMode := computeInstantaneousMode(cfg, evidence, stats)

	// Phase 6.3: Apply hysteresis if state manager available
	var stableMode string
	var hysteresisReason string
	var stateSinceMs int64
	var hysteresisCounts map[string]int
	
	if stateManager != nil {
		stableMode, hysteresisReason = stateManager.UpdateState(instantaneousMode, cfg, now)
		state := stateManager.GetState()
		stateSinceMs = state.StateSinceMs
		hysteresisCounts = state.ConsecutiveTriggerCount
	} else {
		// Fallback: no hysteresis
		stableMode = instantaneousMode
		hysteresisReason = "hysteresis: disabled (no state manager)"
		stateSinceMs = now.UnixMilli()
	}


	// Determine suggestion and reasons
	// Phase 6.3: Use stable mode from hysteresis
	suggestion := stableMode
	reasons, confidence := buildReasons(instantaneousMode, stableMode, evidence, stats, cfg)
	reasons = append(reasons, hysteresisReason)


	// Phase 6.3: Calculate severity score
	severityScore, severityReason := CalculateSeverityScore(instantaneousMode, evidence)
	reasons = append(reasons, severityReason)

	return RiskAdvisory{
		TsMS:              now.UnixMilli(),
		WindowSec:         cfg.WindowSec,
		SuggestedRiskMode: suggestion,
		Confidence:        confidence,
		Reasons:           reasons,
		Evidence:          evidence,

		// Phase 6.3: Hysteresis and severity
		StateSinceMs:      stateSinceMs,
		InstantaneousMode: instantaneousMode,
		SeverityScore:     severityScore,
		HysteresisCounts:  hysteresisCounts,
