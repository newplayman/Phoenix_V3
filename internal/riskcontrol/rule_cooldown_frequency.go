package riskcontrol

import (
	"fmt"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

const CooldownAndFrequencyRuleID = "cooldown_and_frequency"

type CooldownAndFrequencyRule struct {
	Enabled bool

	MinInterval time.Duration
	CooldownFor time.Duration

	mu sync.Mutex

	lastAt        map[string]time.Time
	cooldownUntil map[string]time.Time
}

func NewCooldownAndFrequencyRule(minInterval, cooldownFor time.Duration) *CooldownAndFrequencyRule {
	return &CooldownAndFrequencyRule{
		Enabled:       false, // Phase 5.0: define interfaces; default to non-enforcing.
		MinInterval:   minInterval,
		CooldownFor:   cooldownFor,
		lastAt:        make(map[string]time.Time, 64),
		cooldownUntil: make(map[string]time.Time, 64),
	}
}

func (r *CooldownAndFrequencyRule) RuleID() string { return CooldownAndFrequencyRuleID }

func (r *CooldownAndFrequencyRule) Evaluate(intent contracts.Intent, ctx RiskContext) RiskDecision {
	if !r.Enabled {
		return RiskDecision{
			Verdict: VerdictApprove,
			RuleID:  CooldownAndFrequencyRuleID,
			Reason:  "rule disabled (design-only)",
		}
	}

	key := fmt.Sprintf("%d/%s/%s", intent.ChainID, intent.PoolID, intent.Type)

	r.mu.Lock()
	defer r.mu.Unlock()

	if until, ok := r.cooldownUntil[key]; ok && !until.IsZero() && ctx.Now.Before(until) {
		remain := until.Sub(ctx.Now)
		if remain < 0 {
			remain = 0
		}
		return RiskDecision{
			Verdict: VerdictReject,
			RuleID:  CooldownAndFrequencyRuleID,
			Reason:  fmt.Sprintf("cooldown active for key=%s remain=%s", key, remain),
		}
	}

	if r.MinInterval > 0 {
		if last, ok := r.lastAt[key]; ok && !last.IsZero() && ctx.Now.Sub(last) < r.MinInterval {
			// Enter cooldown to avoid repeated churn.
			if r.CooldownFor > 0 {
				r.cooldownUntil[key] = ctx.Now.Add(r.CooldownFor)
			}
			return RiskDecision{
				Verdict: VerdictReject,
				RuleID:  CooldownAndFrequencyRuleID,
				Reason:  fmt.Sprintf("min interval violation for key=%s min=%s", key, r.MinInterval),
			}
		}
	}

	r.lastAt[key] = ctx.Now

	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  CooldownAndFrequencyRuleID,
		Reason:  "ok",
	}
}
