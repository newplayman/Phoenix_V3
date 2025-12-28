package riskcontrol

import (
	"strings"

	"phoenix-v3/internal/contracts"
)

const RiskModeHALTRuleID = "risk_mode_halt"

type RiskModeHALTRule struct{}

func NewRiskModeHALTRule() *RiskModeHALTRule { return &RiskModeHALTRule{} }

func (r *RiskModeHALTRule) RuleID() string { return RiskModeHALTRuleID }

func (r *RiskModeHALTRule) Evaluate(_ contracts.Intent, ctx RiskContext) RiskDecision {
	if strings.TrimSpace(ctx.Control.RiskMode) == "HALT" {
		reason := "control.json risk_mode=HALT"
		if strings.TrimSpace(ctx.Control.Reason) != "" {
			reason += " reason=" + strings.TrimSpace(ctx.Control.Reason)
		}
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  RiskModeHALTRuleID,
			Reason:  reason,
		}
	}
	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  RiskModeHALTRuleID,
		Reason:  "risk_mode is not HALT",
	}
}
