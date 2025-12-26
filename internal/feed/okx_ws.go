package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type OKXTickersWS struct{}

func NewOKXTickersWS() *OKXTickersWS { return &OKXTickersWS{} }

type okxSubscribeReq struct {
	Op   string      `json:"op"`
	Args []okxSubArg `json:"args"`
}

type okxSubArg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

type okxEventMsg struct {
	Event string `json:"event"`
	Code  string `json:"code"`
	Msg   string `json:"msg"`
}

type okxTickersMsg struct {
	Arg  okxSubArg         `json:"arg"`
	Data []okxTickersDatum `json:"data"`
}

type okxTickersDatum struct {
	Last string `json:"last"`
	Ts   string `json:"ts"` // ms
}

func (o *OKXTickersWS) Run(ctx context.Context, sym SymbolSpec, updates chan<- PriceUpdate, events chan<- SourceEvent) {
	source := "okx"
	endpoints := []string{
		"wss://ws.okx.com:8443/ws/v5/public",
		"wss://wsaws.okx.com:8443/ws/v5/public",
		"wss://wseea.okx.com:8443/ws/v5/public",
	}
	endpointIdx := 0
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		url := endpoints[endpointIdx%len(endpoints)]
		endpointIdx++

		dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second, Proxy: http.ProxyFromEnvironment}
		hdr := http.Header{}
		hdr.Set("User-Agent", "phoenix-v3")
		hdr.Set("Origin", "https://www.okx.com")
		conn, _, err := dialer.DialContext(ctx, url, hdr)
		if err != nil {
			kind, quick := classifyDialErr(err)
			events <- SourceEvent{Source: source, Connected: false, Err: fmt.Errorf("%s: %w", kind, err), At: time.Now()}
			if quick {
				sleepCtx(ctx, 250*time.Millisecond)
				continue
			}
			backoff = nextBackoff(backoff)
			sleepCtx(ctx, backoff)
			continue
		}

		events <- SourceEvent{Source: source, Connected: true, At: time.Now()}
		backoff = time.Second

		sub := okxSubscribeReq{
			Op: "subscribe",
			Args: []okxSubArg{{
				Channel: "tickers",
				InstID:  sym.OKX,
			}},
		}
		if err := conn.WriteJSON(sub); err != nil {
			events <- SourceEvent{Source: source, Connected: false, Err: fmt.Errorf("handshake: %w", err), At: time.Now()}
			_ = conn.Close()
			backoff = nextBackoff(backoff)
			sleepCtx(ctx, backoff)
			continue
		}

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

				// OKX can send subscription event frames.
				var evt okxEventMsg
				if json.Unmarshal(msg, &evt) == nil && evt.Event != "" {
					if evt.Event == "error" {
						readErr <- fmt.Errorf("handshake: okx subscribe error code=%s msg=%s", evt.Code, evt.Msg)
						return
					}
					continue
				}

				var tick okxTickersMsg
				if err := json.Unmarshal(msg, &tick); err != nil {
					continue
				}
				if tick.Arg.Channel != "tickers" || tick.Arg.InstID != sym.OKX || len(tick.Data) == 0 {
					continue
				}

				price := parseFloat(tick.Data[0].Last)
				if math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
					continue
				}
				if price < 500 || price > 100000 {
					events <- SourceEvent{Source: source, Connected: true, Err: fmt.Errorf("parse_error: price out of range (last=%.6f)", price), At: time.Now()}
					continue
				}
				eventAt := parseOKXMillis(tick.Data[0].Ts)
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
					kind, _ := classifyDialErr(err)
					events <- SourceEvent{Source: source, Connected: false, Err: fmt.Errorf("%s: %w", kind, err), At: time.Now()}
				} else {
					events <- SourceEvent{Source: source, Connected: false, Err: fmt.Errorf("okx ws read loop ended"), At: time.Now()}
				}
				break loop
			}
		}

		ping.Stop()
		_ = conn.Close()
		log.Printf("[Market][okx] reconnecting backoff=%s", backoff)
		backoff = nextBackoff(backoff)
		sleepCtx(ctx, backoff)
	}
}

func parseOKXMillis(ms string) time.Time {
	var v int64
	if _, err := fmt.Sscanf(ms, "%d", &v); err != nil || v <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func classifyDialErr(err error) (kind string, quickSwitch bool) {
	if err == nil {
		return "dial", false
	}

	// websocket may wrap errors in url.Error
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		err = uerr.Err
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "server misbehaving") || strings.Contains(msg, "temporary failure in name resolution") {
		return "dns", true
	}
	if strings.Contains(msg, "handshake") || strings.Contains(msg, "bad handshake") {
		return "handshake", false
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "i/o timeout") {
		return "dial_timeout", false
	}
	if strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "refused") {
		return "dial", false
	}
	return "dial", false
}
