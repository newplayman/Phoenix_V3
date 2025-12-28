package riskcontrol

import "phoenix-v3/internal/contracts"

const ForceDryRunRuleID = "force_dry_run"

type ForceDryRunRule struct{}

func NewForceDryRunRule() *ForceDryRunRule { return &ForceDryRunRule{} }

func (r *ForceDryRunRule) RuleID() string { return ForceDryRunRuleID }

func (r *ForceDryRunRule) Evaluate(_ contracts.Intent, ctx RiskContext) RiskDecision {
	if ctx.Control.ForceDryRun && !ctx.CandidateIsDryRun {
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  ForceDryRunRuleID,
			Reason:  "control.json force_dry_run=true but candidate would broadcast",
		}
	}
	if !ctx.CandidateIsDryRun {
		// Phase 5.0 hard constraint: do not enable real trading.
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  ForceDryRunRuleID,
			Reason:  "phase5 design-only: live broadcast is not allowed (force_dry_run required)",
		}
	}
	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  ForceDryRunRuleID,
		Reason:  "dry-run enforced",
	}
}
