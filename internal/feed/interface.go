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

type FeedStatus struct {
	Source       string
	Healthy      bool
	DelayMs      int64
	LastUpdateAt time.Time
}

type Feed interface {
	Start(ctx context.Context) error
	SubscribeTicker(symbol string) (<-chan Ticker, error)
}
