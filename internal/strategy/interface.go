package strategy

import (
	"context"
	"time"
)

type IntentType string

const (
	IntentRebalance  IntentType = "rebalance"
	IntentWithdraw   IntentType = "withdraw"
	IntentCollectFee IntentType = "collect_fee"
)

type Intent struct {
	ID          string
	Type        IntentType
	PoolID      string
	Urgency     int // Higher is more urgent
	Deadline    time.Time
	ExpectedPnL float64
}

type Strategy interface {
	// Evaluate decides whether to generate an Intent based on current state
	Evaluate(ctx context.Context, input interface{}) ([]Intent, error)
}
