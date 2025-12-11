package feed

import (
	"sync"
	"time"
)

type Aggregator struct {
	mu    sync.Mutex
	last  map[string]Ticker
	out   chan Ticker
	close chan struct{}
}

func NewAggregator() *Aggregator {
	return &Aggregator{
		last:  make(map[string]Ticker),
		out:   make(chan Ticker, 32),
		close: make(chan struct{}),
	}
}

func (a *Aggregator) AddSource(name string, ch <-chan Ticker) {
	go func() {
		for {
			select {
			case <-a.close:
				return
			case t, ok := <-ch:
				if !ok {
					return
				}
				a.mu.Lock()
				a.last[name] = t
				agg := a.aggregateLocked(t.Symbol)
				a.mu.Unlock()
				if agg.Timestamp.IsZero() {
					continue
				}
				select {
				case a.out <- agg:
				default:
					// drop to avoid blocking
				}
			}
		}
	}()
}

func (a *Aggregator) aggregateLocked(symbol string) Ticker {
	var (
		sum    float64
		count  int
		latest time.Time
	)
	for _, tick := range a.last {
		if tick.Symbol != symbol {
			continue
		}
		sum += tick.Price
		count++
		if tick.Timestamp.After(latest) {
			latest = tick.Timestamp
		}
	}
	if count == 0 {
		return Ticker{}
	}
	return Ticker{
		Symbol:    symbol,
		Price:     sum / float64(count),
		Timestamp: latest,
	}
}

func (a *Aggregator) Output() <-chan Ticker {
	return a.out
}

func (a *Aggregator) Close() {
	select {
	case <-a.close:
	default:
		close(a.close)
	}
}
