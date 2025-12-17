package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type BinanceOrderbook struct {
	symbol string

	wsURL   string
	restURL string
	client  *http.Client
}

type BinanceDepthSnapshot struct {
	LastUpdateID int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}

type BinanceDepthDelta struct {
	EventType string     `json:"e"` // "depthUpdate"
	EventTime int64      `json:"E"`
	Symbol    string     `json:"s"`
	FirstU    int64      `json:"U"`
	LastU     int64      `json:"u"`
	Bids      [][]string `json:"b"`
	Asks      [][]string `json:"a"`
}

func NewBinanceOrderbook(symbol string) *BinanceOrderbook {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	return &BinanceOrderbook{
		symbol:  symbol,
		wsURL:   fmt.Sprintf("%s/%s@depth@100ms", binanceWSURL, strings.ToLower(symbol)),
		restURL: "https://api.binance.com/api/v3/depth?symbol=%s&limit=1000",
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (b *BinanceOrderbook) FetchSnapshot(ctx context.Context) (*BinanceDepthSnapshot, error) {
	url := fmt.Sprintf(b.restURL, b.symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("binance depth rest status %d body=%q", resp.StatusCode, string(body))
	}
	var snap BinanceDepthSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (b *BinanceOrderbook) DialWS(ctx context.Context) (*websocket.Conn, error) {
	d := websocket.Dialer{}
	c, _, err := d.DialContext(ctx, b.wsURL, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

type OrderbookResyncReason string

const (
	ResyncStart     OrderbookResyncReason = "start"
	ResyncSeqGap    OrderbookResyncReason = "seq_gap"
	ResyncReconnect OrderbookResyncReason = "reconnect"
)

// BinanceOrderbookSync consumes deltas and maintains a replayable raw stream.
// It implements Binance's "snapshot + depthUpdate deltas" sequencing rules, and triggers a REST snapshot resync on gaps.
type BinanceOrderbookSync struct {
	exchange string
	symbol   string

	book *Orderbook

	lastUpdateID int64
	haveSnapshot bool
	seenDelta    bool

	fetchSnapshot func(ctx context.Context) (*BinanceDepthSnapshot, error)
}

func NewBinanceOrderbookSync(symbol string, fetchSnapshot func(ctx context.Context) (*BinanceDepthSnapshot, error)) *BinanceOrderbookSync {
	return &BinanceOrderbookSync{
		exchange:      "binance",
		symbol:        strings.ToUpper(strings.TrimSpace(symbol)),
		book:          NewOrderbook(),
		fetchSnapshot: fetchSnapshot,
	}
}

func (s *BinanceOrderbookSync) Snapshot(ctx context.Context, reason OrderbookResyncReason) (OrderbookRawEvent, error) {
	if s.fetchSnapshot == nil {
		return OrderbookRawEvent{}, fmt.Errorf("missing fetchSnapshot")
	}
	prev := s.lastUpdateID
	snap, err := s.fetchSnapshot(ctx)
	if err != nil {
		return OrderbookRawEvent{}, err
	}
	s.book.ApplySnapshot(snap.Bids, snap.Asks)
	s.lastUpdateID = snap.LastUpdateID
	s.haveSnapshot = true
	s.seenDelta = false

	return OrderbookRawEvent{
		Type:           OrderbookSnapshotType,
		Exchange:       s.exchange,
		Symbol:         s.symbol,
		LastUpdateID:   snap.LastUpdateID,
		Bids:           snap.Bids,
		Asks:           snap.Asks,
		Reason:         string(reason),
		PrevLastUpdate: prev,
	}, nil
}

func (s *BinanceOrderbookSync) ApplyDelta(ctx context.Context, d BinanceDepthDelta) (delta OrderbookRawEvent, resync *OrderbookRawEvent, err error) {
	delta = OrderbookRawEvent{
		Type:        OrderbookDeltaType,
		Exchange:    s.exchange,
		Symbol:      s.symbol,
		EventTimeMS: d.EventTime,
		SeqStart:    d.FirstU,
		SeqEnd:      d.LastU,
		Bids:        d.Bids,
		Asks:        d.Asks,
	}

	if !s.haveSnapshot {
		// Caller should snapshot first; we keep this delta for replay, but we cannot apply it.
		return delta, nil, nil
	}

	// Drop stale updates.
	if d.LastU <= s.lastUpdateID {
		delta.DiscardedAsStale = true
		return delta, nil, nil
	}

	// First delta after snapshot must satisfy: U <= lastUpdateId+1 <= u
	// Subsequent deltas must satisfy: U == lastUpdateId+1
	want := s.lastUpdateID + 1
	okSeq := false
	if !s.seenDelta {
		okSeq = d.FirstU <= want && want <= d.LastU
	} else {
		okSeq = d.FirstU == want
	}
	if !okSeq {
		// Sequence gap (or out-of-order). Resync via REST snapshot.
		snapEv, snapErr := s.Snapshot(ctx, ResyncSeqGap)
		if snapErr != nil {
			return delta, nil, snapErr
		}
		resync = &snapEv
		// After resync, caller may replay this delta stream; but for simplicity, we do not attempt re-apply the same delta.
		return delta, resync, nil
	}

	delta.AppliedFromSeq = want
	delta.AppliedToSeq = d.LastU
	s.book.ApplyDelta(d.Bids, d.Asks)
	s.lastUpdateID = d.LastU
	s.seenDelta = true
	return delta, nil, nil
}

func (s *BinanceOrderbookSync) Top() TopOfBook {
	return s.book.Top()
}

func (s *BinanceOrderbookSync) LastUpdateID() int64 {
	return s.lastUpdateID
}
