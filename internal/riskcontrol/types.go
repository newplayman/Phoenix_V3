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

	// System is an optional, best-effort summary of system health metrics.
	System SystemHealth

	// CandidateIsDryRun indicates whether the executor would broadcast if allowed.
	// Risk control must reject any live broadcast in this phase.
	CandidateIsDryRun bool
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
