package advisory

import (
	"os"
	"strconv"
	"strings"
)

// AdvisoryConfig holds configuration for risk advisory generation.
type AdvisoryConfig struct {
	Enabled   bool
	WindowSec int64 // Default: 3600 (1 hour)

	// HALT thresholds (high risk)
	HaltDivergenceRejectRate float64 // Default: 0.80
	HaltMinEvaluations       int64   // Default: 20
	HaltMinDeviationBps      int64   // Default: 500
	HaltCooldownRejectRate   float64 // Default: 0.50

	// SAFE thresholds (degraded mode)
	SafeTimeMismatchSkipRate float64 // Default: 0.70
	SafeMissingNormSkipRate  float64 // Default: 0.50
	SafeMinEvaluations       int64   // Default: 20
}

// NewAdvisoryConfig creates a new advisory config with conservative defaults.
func NewAdvisoryConfig() AdvisoryConfig {
	cfg := AdvisoryConfig{
		Enabled:   true,
		WindowSec: 3600,

		// HALT defaults (conservative)
		HaltDivergenceRejectRate: 0.80,
		HaltMinEvaluations:       20,
		HaltMinDeviationBps:      500,
		HaltCooldownRejectRate:   0.50,

		// SAFE defaults (conservative)
		SafeTimeMismatchSkipRate: 0.70,
		SafeMissingNormSkipRate:  0.50,
		SafeMinEvaluations:       20,
	}

	// Allow environment variable overrides
	if v := os.Getenv("RISK_ADVISORY_ENABLED"); v != "" {
		cfg.Enabled = v == "1" || strings.ToLower(v) == "true"
	}
	if v := os.Getenv("RISK_ADVISORY_WINDOW_SEC"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			cfg.WindowSec = i
		}
	}
	if v := os.Getenv("RISK_ADVISORY_HALT_DIVERGENCE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.HaltDivergenceRejectRate = f
		}
	}
	if v := os.Getenv("RISK_ADVISORY_HALT_MIN_EVALUATIONS"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			cfg.HaltMinEvaluations = i
		}
	}
	if v := os.Getenv("RISK_ADVISORY_HALT_MIN_DEVIATION_BPS"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			cfg.HaltMinDeviationBps = i
		}
	}
	if v := os.Getenv("RISK_ADVISORY_HALT_COOLDOWN_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.HaltCooldownRejectRate = f
		}
	}
	if v := os.Getenv("RISK_ADVISORY_SAFE_TIME_MISMATCH_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.SafeTimeMismatchSkipRate = f
		}
	}
	if v := os.Getenv("RISK_ADVISORY_SAFE_MISSING_NORM_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.SafeMissingNormSkipRate = f
		}
	}
	if v := os.Getenv("RISK_ADVISORY_SAFE_MIN_EVALUATIONS"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			cfg.SafeMinEvaluations = i
		}
	}

	return cfg
}
