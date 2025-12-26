package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type BinanceBookTickerWS struct{}

func NewBinanceBookTickerWS() *BinanceBookTickerWS { return &BinanceBookTickerWS{} }

func (b *BinanceBookTickerWS) Run(ctx context.Context, sym SymbolSpec, updates chan<- PriceUpdate, events chan<- SourceEvent) {
	source := "binance"
	stream := fmt.Sprintf("%s@bookTicker", sym.Binance)
	endpoints := []string{
		// WS-only hard preference: use the public mirror endpoint which reliably returns correct spot bookTicker in this environment.
		// (Other endpoints may be blocked or MITM'd and can return corrupted prices.)
		"wss://data-stream.binance.vision/ws",
	}
	endpointIdx := 0

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		base := endpoints[endpointIdx%len(endpoints)]
		url := fmt.Sprintf("%s/%s", base, stream)
		endpointIdx++

		dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second, Proxy: http.ProxyFromEnvironment}
		hdr := http.Header{}
		hdr.Set("User-Agent", "phoenix-v3")
		hdr.Set("Origin", "https://www.binance.com")
		conn, _, err := dialer.DialContext(ctx, url, hdr)
		if err != nil {
			events <- SourceEvent{Source: source, Connected: false, Err: err, At: time.Now()}
			backoff = nextBackoff(backoff)
			sleepCtx(ctx, backoff)
			continue
		}

		events <- SourceEvent{Source: source, Connected: true, At: time.Now()}
		backoff = time.Second

		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
			return nil
		})

		ping := time.NewTicker(15 * time.Second)
		readErr := make(chan error, 1)
		go func() {
			defer close(readErr)
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					readErr <- err
					return
				}
				symbol, bid, ask, eventAt, ok, perr := parseBinanceBookTicker(msg)
				if !ok {
					if perr != nil {
						events <- SourceEvent{Source: source, Connected: true, Err: fmt.Errorf("parse_error: %v", perr), At: time.Now()}
					}
					continue
				}
				if strings.TrimSpace(symbol) != strings.ToUpper(sym.Binance) {
					continue
				}
				if bid <= 0 || ask <= 0 {
					events <- SourceEvent{Source: source, Connected: true, Err: errors.New("parse_error: invalid bid/ask (<=0)"), At: time.Now()}
					continue
				}
				price := (bid + ask) / 2.0
				if math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
					continue
				}
				// Sanity check: prevent obviously wrong parsing poisoning the aggregator.
				// ETH/USDT should not be sub-$500 in normal conditions; treat anything below as corrupted feed.
				if price < 500 || price > 100000 {
					events <- SourceEvent{Source: source, Connected: true, Err: fmt.Errorf("parse_error: price out of range (mid=%.6f)", price), At: time.Now()}
					continue
				}
				if eventAt.IsZero() {
					eventAt = time.Now()
				}
				now := time.Now()
				select {
				case updates <- PriceUpdate{
					Source:     source,
					Symbol:     sym.Display,
					Price:      price,
					EventTime:  eventAt,
					ReceivedAt: now,
				}:
				default:
					// drop to avoid head-of-line blocking
				}
			}
		}()

	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-ping.C:
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(3*time.Second))
			case err := <-readErr:
				if err != nil {
					events <- SourceEvent{Source: source, Connected: false, Err: err, At: time.Now()}
				} else {
					events <- SourceEvent{Source: source, Connected: false, Err: fmt.Errorf("binance ws read loop ended"), At: time.Now()}
				}
				break loop
			}
		}

		ping.Stop()
		_ = conn.Close()
		log.Printf("[Market][binance] reconnecting backoff=%s", backoff)
		backoff = nextBackoff(backoff)
		sleepCtx(ctx, backoff)
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	if cur <= 0 {
		return time.Second
	}
	next := cur * 2
	if next > 30*time.Second {
		next = 30 * time.Second
	}
	return next
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func parseFloat(s string) float64 {
	var v float64
	_, _ = fmt.Sscanf(s, "%f", &v)
	return v
}

func parseFloatStrict(s string) (float64, bool) {
	var v float64
	n, err := fmt.Sscanf(s, "%f", &v)
	if err != nil || n != 1 {
		return 0, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// parseBinanceBookTicker parses bookTicker payloads while explicitly ignoring quantity fields "B"/"A".
// Important: encoding/json matches keys case-insensitively, so struct tags like `json:"b"` can be overwritten by "B".
func parseBinanceBookTicker(msg []byte) (symbol string, bid float64, ask float64, eventAt time.Time, ok bool, err error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg, &raw); err != nil {
		return "", 0, 0, time.Time{}, false, err
	}

	// symbol: "s"
	if v, exists := raw["s"]; exists {
		_ = json.Unmarshal(v, &symbol)
	}
	if strings.TrimSpace(symbol) == "" {
		return "", 0, 0, time.Time{}, false, errors.New("missing symbol (s)")
	}

	// exact lowercase keys only: "b" and "a"
	var bidStr, askStr string
	if v, exists := raw["b"]; exists {
		_ = json.Unmarshal(v, &bidStr)
	}
	if v, exists := raw["a"]; exists {
		_ = json.Unmarshal(v, &askStr)
	}
	bidStr = strings.TrimSpace(bidStr)
	askStr = strings.TrimSpace(askStr)
	if bidStr == "" || askStr == "" {
		return symbol, 0, 0, time.Time{}, false, errors.New("missing bid/ask price fields (b/a)")
	}

	var okBid, okAsk bool
	bid, okBid = parseFloatStrict(bidStr)
	ask, okAsk = parseFloatStrict(askStr)
	if !okBid || !okAsk {
		return symbol, 0, 0, time.Time{}, false, fmt.Errorf("invalid bid/ask price (b=%q a=%q)", bidStr, askStr)
	}

	// event time: if "E" exists use it, else leave zero.
	if v, exists := raw["E"]; exists {
		var ms int64
		if json.Unmarshal(v, &ms) == nil && ms > 0 {
			eventAt = time.UnixMilli(ms)
		}
	}
	return symbol, bid, ask, eventAt, true, nil
}
