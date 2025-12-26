package feed

import "time"

type PriceUpdate struct {
	Source     string
	Symbol     string
	Price      float64
	EventTime  time.Time
	ReceivedAt time.Time
}

type SourceEvent struct {
	Source    string
	Connected bool
	Err       error
	At        time.Time
}

type PriceSourceState struct {
	Name         string    `json:"name"`
	Connected    bool      `json:"connected"`
	LastPrice    float64   `json:"last_price"`
	LastUpdateAt time.Time `json:"last_update_at"`
	Fresh        bool      `json:"fresh"`
	UpdateAgeMs  int64     `json:"update_age_ms"`
	LatencyMs    int64     `json:"latency_ms"`
	Err1m        int       `json:"err_1m"`
	LastErr      string    `json:"last_err"`
	LastErrAt    time.Time `json:"last_err_at"`
}

type AggregateState struct {
	AggPrice      float64   `json:"agg_price"`
	AggUpdatedAt  time.Time `json:"agg_updated_at"`
	Stale         bool      `json:"stale"`
	StaleAgeMs    int64     `json:"stale_age_ms"`
	Confidence    float64   `json:"confidence"`
	DivergencePct float64   `json:"divergence_pct"`
}

type RiskState struct {
	Mode   string `json:"mode"`   // normal|degraded|frozen
	Reason string `json:"reason"` // human-readable or stable reason key
}

type MarketSnapshot struct {
	Symbol    string             `json:"symbol"`
	Aggregate AggregateState     `json:"aggregate"`
	Sources   []PriceSourceState `json:"sources"`
	Risk      RiskState          `json:"risk"`
}
