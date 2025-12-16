package bot

import "phoenix-v3/internal/config"

// ComputeTargetWidthPct computes the active LP width (percent terms) based on realized volatility.
// It returns (widthPct, minWidthPct, maxWidthPct).
func ComputeTargetWidthPct(cfg *config.AppConfig, pool config.PoolConfig, profile config.StrategyProfile, sigmaDaily float64) (widthPct float64, minWidthPct float64, maxWidthPct float64) {
	minW := 0.02
	maxW := 0.20
	k := 2.0
	if cfg != nil {
		if cfg.Strategy.Range.MinWidthPct > 0 && cfg.Strategy.Range.MinWidthPct < 1 {
			minW = cfg.Strategy.Range.MinWidthPct
		}
		if cfg.Strategy.Range.MaxWidthPct > 0 && cfg.Strategy.Range.MaxWidthPct < 1 {
			maxW = cfg.Strategy.Range.MaxWidthPct
		}
		if cfg.Strategy.Range.VolK > 0 {
			k = cfg.Strategy.Range.VolK
		}
	}
	if pool.MinWidthPct > 0 && pool.MinWidthPct < 1 {
		minW = pool.MinWidthPct
	}
	if pool.MaxWidthPct > 0 && pool.MaxWidthPct < 1 {
		maxW = pool.MaxWidthPct
	}
	if maxW < minW {
		maxW = minW
	}
	mult := profile.RangeWidthMultiplier
	if mult <= 0 {
		mult = 1.0
	}
	if sigmaDaily <= 0 {
		return minW, minW, maxW
	}
	width := sigmaDaily * k * mult
	if width < minW {
		width = minW
	}
	if width > maxW {
		width = maxW
	}
	return width, minW, maxW
}
