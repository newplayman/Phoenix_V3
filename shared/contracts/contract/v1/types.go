package contractv1

const SchemaVersion = "v1"

type IntentType string

const (
	IntentTypeRebalanceV3 IntentType = "REBALANCE_V3"
	IntentTypeCexMake     IntentType = "CEX_MAKE"
	IntentTypeArbitrage   IntentType = "ARBITRAGE"
	IntentTypeHedgePerp   IntentType = "HEDGE_PERP"
)

type RiskLevel string

const (
	RiskLevelSafe     RiskLevel = "SAFE"
	RiskLevelDeny     RiskLevel = "DENY"
	RiskLevelPause    RiskLevel = "PAUSE"
	RiskLevelSafeMode RiskLevel = "SAFE_MODE"
	RiskLevelHalt     RiskLevel = "HALT"
)

type ExecutionStatus string

const (
	ExecutionStatusSimulated       ExecutionStatus = "SIMULATED"
	ExecutionStatusSubmitted       ExecutionStatus = "SUBMITTED"
	ExecutionStatusPartiallyFilled ExecutionStatus = "PARTIALLY_FILLED"
	ExecutionStatusFilled          ExecutionStatus = "FILLED"
	ExecutionStatusCanceled        ExecutionStatus = "CANCELED"
	ExecutionStatusFailed          ExecutionStatus = "FAILED"
)

type ErrorKind string

const (
	ErrorKindNone                ErrorKind = "NONE"
	ErrorKindTransient           ErrorKind = "TRANSIENT"
	ErrorKindAuth                ErrorKind = "AUTH"
	ErrorKindRateLimit           ErrorKind = "RATE_LIMIT"
	ErrorKindInsufficientBalance ErrorKind = "INSUFFICIENT_BALANCE"
	ErrorKindBadParams           ErrorKind = "BAD_PARAMS"
	ErrorKindUnknown             ErrorKind = "UNKNOWN"
)

type Mode string

const (
	ModeDryRun Mode = "DRY_RUN"
	ModeShadow Mode = "SHADOW"
	ModeLive   Mode = "LIVE"
	ModePaper  Mode = "PAPER"
)

type State string

const (
	StateRunning  State = "RUNNING"
	StatePaused   State = "PAUSED"
	StateSafeMode State = "SAFE_MODE"
	StateError    State = "ERROR"
)

type IntentV1 struct {
	SchemaVersion string         `json:"schema_version"`
	IntentID      string         `json:"intent_id"`
	IntentType    IntentType     `json:"intent_type"`
	TsLocalMS     int64          `json:"ts_local_ms"`
	DryRun        bool           `json:"dry_run"`
	TTLms         int64          `json:"ttl_ms"`
	Params        map[string]any `json:"params,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
	Summary       string         `json:"summary,omitempty"`
}

type RiskDecisionV1 struct {
	SchemaVersion string            `json:"schema_version"`
	DecisionID    string            `json:"decision_id,omitempty"`
	RunID         string            `json:"run_id,omitempty"`
	TsLocalMS     int64             `json:"ts_local_ms"`
	Level         RiskLevel         `json:"level"`
	Reasons       []string          `json:"reasons"`
	Fields        map[string]string `json:"fields"`
	CooldownMS    int64             `json:"cooldown_ms"`
}

type ExecutorResultV1 struct {
	SchemaVersion string          `json:"schema_version"`
	IntentID      string          `json:"intent_id"`
	TsLocalMS     int64           `json:"ts_local_ms"`
	Status        ExecutionStatus `json:"status"`
	ErrorKind     ErrorKind       `json:"error_kind"`
	Error         string          `json:"error,omitempty"`
	Receipt       map[string]any  `json:"receipt,omitempty"`
}

type StatusIntentV1 struct {
	IntentType IntentType     `json:"intent_type"`
	IntentID   string         `json:"intent_id"`
	TsLocalMS  int64          `json:"ts_local_ms"`
	Summary    string         `json:"summary,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
}

type StatusRiskV1 struct {
	Level        RiskLevel `json:"level"`
	TsLocalMS    int64     `json:"ts_local_ms"`
	ReasonsCount int       `json:"reasons_count"`
}

type StatusExecV1 struct {
	Status    ExecutionStatus `json:"status"`
	TsLocalMS int64           `json:"ts_local_ms"`
	ErrorKind ErrorKind       `json:"error_kind"`
}

type StatusV1 struct {
	SchemaVersion string          `json:"schema_version"`
	RunID         string          `json:"run_id"`
	Mode          Mode            `json:"mode"`
	State         State           `json:"state"`
	LastIntent    *StatusIntentV1 `json:"last_intent,omitempty"`
	LastRisk      *StatusRiskV1   `json:"last_risk,omitempty"`
	LastExec      *StatusExecV1   `json:"last_exec,omitempty"`
	UptimeSec     int64           `json:"uptime_sec"`
}
