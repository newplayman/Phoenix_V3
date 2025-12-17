package feed

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	OrderbookSnapshotType = "ORDERBOOK_SNAPSHOT"
	OrderbookDeltaType    = "ORDERBOOK_DELTA"
)

// OrderbookRawEvent is the payload written into the raw log.
// It is designed to be replayable: snapshots + deltas can reconstruct top-of-book.
type OrderbookRawEvent struct {
	Type             string     `json:"type"` // ORDERBOOK_SNAPSHOT | ORDERBOOK_DELTA
	Exchange         string     `json:"exchange"`
	Symbol           string     `json:"symbol"`
	EventTimeMS      int64      `json:"event_time_ms,omitempty"`
	LastUpdateID     int64      `json:"last_update_id,omitempty"` // snapshot only
	SeqStart         int64      `json:"seq_start,omitempty"`      // delta only (U)
	SeqEnd           int64      `json:"seq_end,omitempty"`        // delta only (u)
	Bids             [][]string `json:"bids,omitempty"`           // [["price","qty"],...]
	Asks             [][]string `json:"asks,omitempty"`
	Reason           string     `json:"reason,omitempty"`              // snapshot only (start|seq_gap|reconnect)
	PrevLastUpdate   int64      `json:"prev_last_update_id,omitempty"` // snapshot only (resync)
	AppliedFromSeq   int64      `json:"applied_from_seq,omitempty"`    // optional diagnostics
	AppliedToSeq     int64      `json:"applied_to_seq,omitempty"`
	DiscardedAsStale bool       `json:"discarded_as_stale,omitempty"` // delta diagnostics
}

type TopOfBook struct {
	BestBid float64
	BestAsk float64
	Spread  float64
}

func (t TopOfBook) SpreadPct() float64 {
	if t.BestBid <= 0 || t.BestAsk <= 0 {
		return 0
	}
	mid := (t.BestBid + t.BestAsk) / 2
	if mid <= 0 {
		return 0
	}
	return t.Spread / mid
}

type Orderbook struct {
	bids map[float64]float64
	asks map[float64]float64
}

func NewOrderbook() *Orderbook {
	return &Orderbook{
		bids: make(map[float64]float64),
		asks: make(map[float64]float64),
	}
}

func (o *Orderbook) Reset() {
	for k := range o.bids {
		delete(o.bids, k)
	}
	for k := range o.asks {
		delete(o.asks, k)
	}
}

func parseLevel(level []string) (price float64, qty float64, ok bool) {
	if len(level) < 2 {
		return 0, 0, false
	}
	p, err := strconv.ParseFloat(strings.TrimSpace(level[0]), 64)
	if err != nil || !isFinitePositive(p) {
		return 0, 0, false
	}
	q, err := strconv.ParseFloat(strings.TrimSpace(level[1]), 64)
	if err != nil || math.IsNaN(q) || math.IsInf(q, 0) || q < 0 {
		return 0, 0, false
	}
	return p, q, true
}

func isFinitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func (o *Orderbook) ApplySnapshot(bids, asks [][]string) {
	o.Reset()
	for _, lvl := range bids {
		p, q, ok := parseLevel(lvl)
		if !ok {
			continue
		}
		if q == 0 {
			continue
		}
		o.bids[p] = q
	}
	for _, lvl := range asks {
		p, q, ok := parseLevel(lvl)
		if !ok {
			continue
		}
		if q == 0 {
			continue
		}
		o.asks[p] = q
	}
}

func (o *Orderbook) ApplyDelta(bids, asks [][]string) {
	for _, lvl := range bids {
		p, q, ok := parseLevel(lvl)
		if !ok {
			continue
		}
		if q == 0 {
			delete(o.bids, p)
		} else {
			o.bids[p] = q
		}
	}
	for _, lvl := range asks {
		p, q, ok := parseLevel(lvl)
		if !ok {
			continue
		}
		if q == 0 {
			delete(o.asks, p)
		} else {
			o.asks[p] = q
		}
	}
}

func (o *Orderbook) Top() TopOfBook {
	bestBid := 0.0
	for p := range o.bids {
		if p > bestBid {
			bestBid = p
		}
	}
	bestAsk := 0.0
	for p := range o.asks {
		if bestAsk == 0 || p < bestAsk {
			bestAsk = p
		}
	}
	spread := 0.0
	if bestBid > 0 && bestAsk > 0 && bestAsk >= bestBid {
		spread = bestAsk - bestBid
	}
	return TopOfBook{BestBid: bestBid, BestAsk: bestAsk, Spread: spread}
}

func FormatTop(t TopOfBook) string {
	return fmt.Sprintf("best_bid=%.8f best_ask=%.8f spread=%.8f spread_pct=%.6f",
		t.BestBid, t.BestAsk, t.Spread, t.SpreadPct())
}

func SortLevelsDesc(levels [][]string) {
	sort.Slice(levels, func(i, j int) bool {
		pi, _, okI := parseLevel(levels[i])
		pj, _, okJ := parseLevel(levels[j])
		if okI != okJ {
			return okI
		}
		return pi > pj
	})
}

func SortLevelsAsc(levels [][]string) {
	sort.Slice(levels, func(i, j int) bool {
		pi, _, okI := parseLevel(levels[i])
		pj, _, okJ := parseLevel(levels[j])
		if okI != okJ {
			return okI
		}
		return pi < pj
	})
}
