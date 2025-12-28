package riskcontrol

import (
	"fmt"

	"phoenix-v3/internal/contracts"
)

const PriceSourceDivergenceRuleID = "price_source_divergence"

type PriceSourceDivergenceRule struct {
	Enabled bool

	// DivergencePctThreshold is compared against feed.MarketSnapshot.Aggregate.DivergencePct.
	DivergencePctThreshold float64
}

func NewPriceSourceDivergenceRule(divergencePctThreshold float64) *PriceSourceDivergenceRule {
	if divergencePctThreshold <= 0 {
		divergencePctThreshold = 0.01
	}
	return &PriceSourceDivergenceRule{
		Enabled:                false, // Phase 5.0: define interfaces; default to non-enforcing.
		DivergencePctThreshold: divergencePctThreshold,
	}
}

func (r *PriceSourceDivergenceRule) RuleID() string { return PriceSourceDivergenceRuleID }

func (r *PriceSourceDivergenceRule) Evaluate(_ contracts.Intent, ctx RiskContext) RiskDecision {
	if !r.Enabled {
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason:  "rule disabled (design-only)",
		}
	}

	div := ctx.Market.Aggregate.DivergencePct
	if div > r.DivergencePctThreshold {
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  PriceSourceDivergenceRuleID,
			Reason:  fmt.Sprintf("price source divergence too high: divergence_pct=%.6f threshold=%.6f", div, r.DivergencePctThreshold),
		}
	}

	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  PriceSourceDivergenceRuleID,
		Reason:  "ok",
	}
}
