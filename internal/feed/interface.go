package feed

import (
	"context"
	"time"
)

type Ticker struct {
	Symbol    string
	Price     float64
	Timestamp time.Time
}

type Feed interface {
	Start(ctx context.Context) error
	SubscribeTicker(symbol string) (<-chan Ticker, error)
}
