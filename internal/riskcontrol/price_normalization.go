package riskcontrol

import (
	"fmt"
	"math"
)

// Phase 5.5: unified price semantics for divergence checks.
//
// We normalize all comparable prices to:
//   normalized_price = token1 per 1 token0 (human units).
//
// This file intentionally avoids any network requests. Decimals must come from config/metadata already
// loaded by the bot, and normalization must be best-effort and safe (prefer SKIP over bogus REJECT).

type NormalizationResult struct {
	RawPrice            float64
	NormalizedPrice     float64
	NormalizationOK     bool
	NormalizationDetail string
}

func NormalizeExchangePriceToken1PerToken0(aggPriceToken0PerToken1 float64) NormalizationResult {
	if aggPriceToken0PerToken1 <= 0 || !isFinitePositive(aggPriceToken0PerToken1) {
		return NormalizationResult{
			RawPrice:            aggPriceToken0PerToken1,
			NormalizedPrice:     0,
			NormalizationOK:     false,
			NormalizationDetail: "missing_price_for_normalization",
		}
	}
	return NormalizationResult{
		RawPrice:            aggPriceToken0PerToken1,
		NormalizedPrice:     1.0 / aggPriceToken0PerToken1,
		NormalizationOK:     true,
		NormalizationDetail: "semantics=token1_per_token0 direction_inverted=true",
	}
}

type tickCandidate struct {
	name   string
	price  float64
	detail string
}

// NormalizeOnchainTickToken1PerToken0 returns a best-effort normalized price from a Uniswap V3 slot0 tick.
//
// The primary path assumes tick corresponds to Uniswap's raw ratio and applies decimals:
//
//	raw_ratio = 1.0001^tick
//	human_ratio(token1/token0) = raw_ratio * 10^(dec0-dec1)
//
// Some test pools may be initialized with a "human" sqrtPrice that already embeds decimals; in that case,
// applying decimals again produces astronomical deviations. To avoid false positives, we evaluate a small
// set of deterministic candidate interpretations and pick the one closest (by log-distance) to the
// reference exchange normalized price. If no safe candidate exists, we return NormalizationOK=false.
func NormalizeOnchainTickToken1PerToken0(
	tick int64,
	token0Decimals, token1Decimals int,
	rawPriceObserved float64,
	refExchangeNormalized float64,
) NormalizationResult {
	if token0Decimals <= 0 || token1Decimals <= 0 {
		return NormalizationResult{
			RawPrice:            rawPriceObserved,
			NormalizedPrice:     0,
			NormalizationOK:     false,
			NormalizationDetail: "missing_decimals_for_normalization",
		}
	}

	base := math.Pow(1.0001, float64(tick))
	if !isFinitePositive(base) {
		return NormalizationResult{
			RawPrice:            rawPriceObserved,
			NormalizedPrice:     0,
			NormalizationOK:     false,
			NormalizationDetail: "invalid_tick_for_normalization",
		}
	}

	scale := math.Pow10(token0Decimals - token1Decimals)
	// Candidate interpretations (all output token1 per token0, human units).
	cands := []tickCandidate{
		{
			name:   "uniswap_raw_token1_per_token0",
			price:  base * scale,
			detail: fmt.Sprintf("semantics=token1_per_token0 source=tick variant=uniswap_raw dec0=%d dec1=%d scale=10^(%d-%d)=%g direction_inverted=false", token0Decimals, token1Decimals, token0Decimals, token1Decimals, scale),
		},
		{
			name:   "uniswap_raw_token0_per_token1_inverted",
			price:  (1.0 / base) * scale,
			detail: fmt.Sprintf("semantics=token1_per_token0 source=tick variant=uniswap_raw dec0=%d dec1=%d scale=10^(%d-%d)=%g direction_inverted=true", token0Decimals, token1Decimals, token0Decimals, token1Decimals, scale),
		},
		{
			name:   "tick_human_token1_per_token0",
			price:  base,
			detail: fmt.Sprintf("semantics=token1_per_token0 source=tick variant=tick_human direction_inverted=false dec0=%d dec1=%d decimals_ignored=true", token0Decimals, token1Decimals),
		},
		{
			name:   "tick_human_token0_per_token1_inverted",
			price:  1.0 / base,
			detail: fmt.Sprintf("semantics=token1_per_token0 source=tick variant=tick_human direction_inverted=true dec0=%d dec1=%d decimals_ignored=true", token0Decimals, token1Decimals),
		},
	}

	// Filter out non-finite candidates.
	valid := make([]tickCandidate, 0, len(cands))
	for _, c := range cands {
		if isFinitePositive(c.price) {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return NormalizationResult{
			RawPrice:            rawPriceObserved,
			NormalizedPrice:     0,
			NormalizationOK:     false,
			NormalizationDetail: "no_valid_candidate_for_normalization",
		}
	}

	// If no reference exists, prefer the standard Uniswap+decimals candidate.
	if !isFinitePositive(refExchangeNormalized) {
		best := valid[0]
		return NormalizationResult{
			RawPrice:            rawPriceObserved,
			NormalizedPrice:     best.price,
			NormalizationOK:     true,
			NormalizationDetail: best.detail,
		}
	}

	best := tickCandidate{}
	bestScore := math.Inf(1)
	for _, c := range valid {
		// score = |log(candidate/ref)|
		score := math.Abs(math.Log(c.price / refExchangeNormalized))
		if score < bestScore {
			bestScore = score
			best = c
		}
	}

	// Safety gate: if the best candidate is still wildly off, declare normalization failure.
	// This prevents turning bad inputs into bogus REJECTs with astronomical bps.
	if bestScore > math.Log(1e6) { // > 1e6x away
		return NormalizationResult{
			RawPrice:            rawPriceObserved,
			NormalizedPrice:     0,
			NormalizationOK:     false,
			NormalizationDetail: fmt.Sprintf("missing_decimals_for_normalization best_candidate=%s best_score=%g", best.name, bestScore),
		}
	}

	return NormalizationResult{
		RawPrice:            rawPriceObserved,
		NormalizedPrice:     best.price,
		NormalizationOK:     true,
		NormalizationDetail: best.detail,
	}
}

func isFinitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
