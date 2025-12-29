package riskcontrol

import (
	"time"

	"phoenix-v3/internal/contracts"
	"phoenix-v3/internal/control/filecontrol"
	"phoenix-v3/internal/feed"
)

type RiskVerdict string

const (
	VerdictApprove RiskVerdict = "APPROVE"
	VerdictModify  RiskVerdict = "MODIFY"
	VerdictReject  RiskVerdict = "REJECT"
)

// RiskDecision is the output of a single RiskRule evaluation.
//
// Constraints:
// - Every decision must have an explainable Reason.
// - RuleID must be stable for logging/audit.
// - Verdict=MODIFY can only propose degradations, never expand risk.
type RiskDecision struct {
	Verdict RiskVerdict
	RuleID  string
	Reason  string

	Degrade *IntentDegradation
}

type IntentDegradation struct {
	// SetUrgencyLower can only reduce urgency (smaller value means less urgent).
	SetUrgencyLower *int

	// SetDeadlineEarlier can only reduce risk by making intent expire earlier.
	SetDeadlineEarlier *time.Time

	// MetadataOverride is intended for safe reductions (e.g. reduce size/amount encoded in metadata).
	// The concrete semantics are owned by the executor/adapter and must never expand risk.
	MetadataOverride map[string]string

	// MetadataDelete removes keys (e.g. drop optional hints that could increase execution aggressiveness).
	MetadataDelete []string
}

type RiskContext struct {
	Now time.Time

	Control filecontrol.ControlState

	// Market is a snapshot from the in-process PriceAggregator (no new external dependency).
	Market feed.MarketSnapshot

	// PriceSources holds same-time best-effort prices from multiple sources (no new network requests).
	// Expected keys in Phase 5.3: "onchain" and "exchange" (or "aggregator").
	PriceSources map[string]PricePoint

	// LastDecisionAt is the best-effort timestamp of when the candidate intent was produced.
	LastDecisionAt time.Time

	// System is an optional, best-effort summary of system health metrics.
	System SystemHealth

	// CandidateIsDryRun indicates whether the executor would broadcast if allowed.
	// Risk control must reject any live broadcast in this phase.
	CandidateIsDryRun bool
}

type PricePoint struct {
	SourceName string

	// RawPrice is the best-effort raw value as produced by the source before any Phase 5.5 normalization.
	// It is used for observability only and MUST NOT be used for comparisons unless NormalizationOK is true.
	RawPrice float64

	// Price is the Phase 5.5 comparable price in the unified semantics (token1 per token0, human units).
	// Kept for backwards compatibility; prefer NormalizedPrice for clarity.
	Price float64

	// NormalizedPrice is the comparable price in the unified semantics:
	// normalized_price = token1 per 1 token0 (human units).
	NormalizedPrice float64

	// NormalizationOK indicates whether NormalizedPrice is safe to compare.
	// If false, PriceSourceDivergenceRule should SKIP instead of rejecting on bogus deviations.
	NormalizationOK bool

	// NormalizationDetail is a compact, stable string describing direction/decimals used.
	NormalizationDetail string

	TsMS int64
}

type SystemHealth struct {
	HealthzOK bool

	BacklogLen int

	// TickIntervalMsP95 is a summary field (not necessarily loop lag).
	TickIntervalMsP95 int64

	RPCOK bool
}

type RiskRule interface {
	RuleID() string
	Evaluate(intent contracts.Intent, ctx RiskContext) RiskDecision
}
