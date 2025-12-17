package strategy

import (
	"context"
	"time"

	"phoenix-v3/internal/engine"
)

type IntentType string

const (
	IntentRebalance  IntentType = "rebalance"
	IntentSwap       IntentType = "swap"
	IntentWithdraw   IntentType = "withdraw"
	IntentCollectFee IntentType = "collect_fee"
)

type Intent struct {
	ID                string
	Type              IntentType
	PoolID            string
	ChainID           int64
	Urgency           int
	Deadline          time.Time
	ExpectedPnL       float64
	TargetNotionalPct float64 // Percentage of total equity to deploy (0.0 - 1.0)
	StrategyVersion   string
	RiskMode          string
	Metadata          map[string]string
}

type Strategy interface {
	Evaluate(ctx context.Context, input engine.EngineInput) ([]Intent, error)
}
