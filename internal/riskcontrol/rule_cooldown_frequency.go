package riskcontrol

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

const CooldownAndFrequencyRuleID = "cooldown_frequency"

type CooldownConfig struct {
	Enabled bool

	Target string

	MinInterval      time.Duration
	FailureThreshold int
	CooldownFor      time.Duration
}

type CooldownAndFrequencyRule struct {
	store *RiskStateStore
	cfg   CooldownConfig

	mu           sync.Mutex
	lastMatchKey string
	lastUntilMS  int64
}

func NewCooldownAndFrequencyRule(store *RiskStateStore, cfg CooldownConfig) *CooldownAndFrequencyRule {
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = 60 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.CooldownFor <= 0 {
		cfg.CooldownFor = 300 * time.Second
	}
	if strings.TrimSpace(cfg.Target) == "" {
		cfg.Target = "phoenix"
	}
	return &CooldownAndFrequencyRule{
		store: store,
		cfg:   cfg,
	}
}

func (r *CooldownAndFrequencyRule) RuleID() string { return CooldownAndFrequencyRuleID }

func (r *CooldownAndFrequencyRule) FailurePolicy() (threshold int, cooldownFor time.Duration) {
	if r == nil {
		return 0, 0
	}
	return r.cfg.FailureThreshold, r.cfg.CooldownFor
}

func (r *CooldownAndFrequencyRule) LastMatch() (key string, untilMS int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastMatchKey, r.lastUntilMS
}

func (r *CooldownAndFrequencyRule) setLastMatch(key string, untilMS int64) {
	r.mu.Lock()
	r.lastMatchKey = key
	r.lastUntilMS = untilMS
	r.mu.Unlock()
}

func (r *CooldownAndFrequencyRule) Evaluate(intent contracts.Intent, ctx RiskContext) RiskDecision {
	if r == nil {
		return RiskDecision{Verdict: VerdictApprove, RuleID: CooldownAndFrequencyRuleID, Reason: "nil rule"}
	}
	if !r.cfg.Enabled {
		return RiskDecision{Verdict: VerdictApprove, RuleID: CooldownAndFrequencyRuleID, Reason: "rule disabled"}
	}
	if r.store == nil {
		return RiskDecision{Verdict: VerdictReject, RuleID: CooldownAndFrequencyRuleID, Reason: "risk state store not configured"}
	}

	now := ctx.Now
	nowMS := now.UnixMilli()
	key := CooldownKey(r.cfg.Target, intent)

	// 1) Failure cooldown gate (cooldown期间一票否决).
	cooldownUntil := r.store.GetCooldownUntilMS(now, key)
	failCount := r.store.GetConsecutiveFails(now, key)
	if cooldownUntil > 0 && nowMS < cooldownUntil {
		remaining := cooldownUntil - nowMS
		reason := fmt.Sprintf(
			"cooldown active cooldown_key=%s remaining_ms=%d cooldown_until_ts_ms=%d failure_count=%d threshold=%d",
			key, remaining, cooldownUntil, failCount, r.cfg.FailureThreshold,
		)
		_ = r.store.SetLastRejectReason(now, key, reason)
		r.setLastMatch(key, cooldownUntil)
		return RiskDecision{Verdict: VerdictReject, RuleID: CooldownAndFrequencyRuleID, Reason: reason}
	}

	// 2) Frequency gate (min interval).
	lastIntentMS := r.store.GetLastIntentMS(now, key)
	minIntervalMS := int64(r.cfg.MinInterval / time.Millisecond)
	if lastIntentMS > 0 && minIntervalMS > 0 {
		elapsed := nowMS - lastIntentMS
		if elapsed >= 0 && elapsed < minIntervalMS {
			remaining := minIntervalMS - elapsed
			nextAllowed := lastIntentMS + minIntervalMS
			reason := fmt.Sprintf(
				"min interval violation cooldown_key=%s remaining_ms=%d min_interval_sec=%d next_allowed_ts_ms=%d",
				key, remaining, int64(r.cfg.MinInterval/time.Second), nextAllowed,
			)
			_ = r.store.SetLastRejectReason(now, key, reason)
			r.setLastMatch(key, nextAllowed)
			return RiskDecision{Verdict: VerdictReject, RuleID: CooldownAndFrequencyRuleID, Reason: reason}
		}
	}

	// Approve: mark last intent timestamp for this key.
	_ = r.store.SetLastIntentMS(now, key, nowMS)
	r.setLastMatch("", 0)
	return RiskDecision{
		Verdict: VerdictApprove,
		RuleID:  CooldownAndFrequencyRuleID,
		Reason:  "ok cooldown_key=" + key,
	}
}
