package riskcontrol

import (
	"strings"

	"phoenix-v3/internal/contracts"
)

type Evaluator struct {
	Rules []RiskRule
}

func NewEvaluator(rules ...RiskRule) *Evaluator {
	cp := make([]RiskRule, 0, len(rules))
	for _, r := range rules {
		if r != nil {
			cp = append(cp, r)
		}
	}
	return &Evaluator{Rules: cp}
}

type Evaluation struct {
	FinalVerdict RiskVerdict
	FinalReason  string
	FinalRuleID  string

	FinalIntent contracts.Intent

	Decisions []RiskDecision
}

func (e *Evaluator) Evaluate(intent contracts.Intent, ctx RiskContext) Evaluation {
	out := Evaluation{
		FinalVerdict: VerdictApprove,
		FinalReason:  "all_rules_approved",
		FinalRuleID:  "risk_control",
		FinalIntent:  intent,
	}

	var bestModify *RiskDecision
	for _, rule := range e.Rules {
		d := rule.Evaluate(intent, ctx)
		d.RuleID = strings.TrimSpace(d.RuleID)
		if d.RuleID == "" {
			d.RuleID = strings.TrimSpace(rule.RuleID())
		}
		if d.Reason == "" {
			d.Reason = "unspecified"
		}
		if d.Verdict == "" {
			d.Verdict = VerdictApprove
		}
		out.Decisions = append(out.Decisions, d)

		switch d.Verdict {
		case VerdictReject:
			out.FinalVerdict = VerdictReject
			out.FinalRuleID = d.RuleID
			out.FinalReason = d.Reason
			return out
		case VerdictModify:
			if bestModify == nil {
				cp := d
				bestModify = &cp
			} else {
				bestModify = mostConservativeModify(*bestModify, d)
			}
		case VerdictApprove:
		default:
			out.FinalVerdict = VerdictReject
			out.FinalRuleID = d.RuleID
			out.FinalReason = "invalid_verdict=" + string(d.Verdict)
			return out
		}
	}

	if bestModify != nil {
		out.FinalVerdict = VerdictModify
		out.FinalRuleID = bestModify.RuleID
		out.FinalReason = bestModify.Reason
		return out
	}

	return out
}

func mostConservativeModify(a, b RiskDecision) *RiskDecision {
	// Phase 5.0: interface-level definition. Prefer the decision that results in a more reduced urgency/deadline.
	// If both are incomparable, prefer the one with a deterministic rule_id tie-breaker.
	if a.Degrade == nil && b.Degrade != nil {
		return &b
	}
	if a.Degrade != nil && b.Degrade == nil {
		return &a
	}
	if a.Degrade == nil && b.Degrade == nil {
		if a.RuleID <= b.RuleID {
			return &a
		}
		return &b
	}

	// Both have degradations; compare urgency/deadline where available.
	aUrg := urgencyOrMax(a.Degrade.SetUrgencyLower)
	bUrg := urgencyOrMax(b.Degrade.SetUrgencyLower)
	if aUrg != bUrg {
		if aUrg < bUrg {
			return &a
		}
		return &b
	}
	aDl := deadlineOrMax(a.Degrade.SetDeadlineEarlier)
	bDl := deadlineOrMax(b.Degrade.SetDeadlineEarlier)
	if !aDl.Equal(bDl) {
		if aDl.Before(bDl) {
			return &a
		}
		return &b
	}
	if a.RuleID <= b.RuleID {
		return &a
	}
	return &b
}
