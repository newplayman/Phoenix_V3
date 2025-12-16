package config

// EffectiveMaxCapPct returns a clamped target utilization cap based on pool-level and global limits.
func EffectiveMaxCapPct(poolMax, globalMax float64) float64 {
	capPct := poolMax
	if globalMax > 0 && (capPct <= 0 || capPct > globalMax) {
		capPct = globalMax
	}
	if capPct <= 0 {
		capPct = 0.05
	}
	if capPct > 1 {
		capPct = 1
	}
	return capPct
}
