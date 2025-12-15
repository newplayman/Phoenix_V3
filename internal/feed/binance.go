package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	binanceWSURL   = "wss://stream.binance.com:9443/ws"
	binanceRESTURL = "https://api.binance.com/api/v3/ticker/price?symbol=%s"
)

type BinanceFeed struct {
	conn           *websocket.Conn
	status         FeedStatus
	onEvent        func(FeedStatus)
	client         *http.Client
	restInterval   time.Duration
	reconnectDelay time.Duration
}

type binanceTickerMsg struct {
	S string `json:"s"` // Symbol
	C string `json:"c"` // Price
}

type binanceRestResp struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

func NewBinanceFeed() *BinanceFeed {
	return &BinanceFeed{
		status: FeedStatus{
			Source: "binance",
		},
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		restInterval:   3 * time.Second,
		reconnectDelay: 5 * time.Second,
	}
}

func (b *BinanceFeed) Start(ctx context.Context) error {
	return nil
}

func (b *BinanceFeed) OnStatusUpdate(fn func(FeedStatus)) {
	b.onEvent = fn
}

func (b *BinanceFeed) SubscribeTicker(symbol string) (<-chan Ticker, error) {
	out := make(chan Ticker, 32)
	go b.run(symbol, out)
	return out, nil
}

func (b *BinanceFeed) run(symbol string, out chan<- Ticker) {
	streamName := fmt.Sprintf("%s@miniTicker", strings.ToLower(symbol))
	wsURL := fmt.Sprintf("%s/%s", binanceWSURL, streamName)

	for {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("Binance WS dial failed: %v", err)
			b.updateStatus(false, 0)
			b.pollREST(symbol, out)
			time.Sleep(b.reconnectDelay)
			continue
		}

		b.conn = conn
		log.Printf("✅ Subscribed to Binance stream %s", streamName)
		if !b.readLoop(conn, symbol, out) {
			time.Sleep(b.reconnectDelay)
			continue
		}
	}
}

func (b *BinanceFeed) readLoop(conn *websocket.Conn, symbol string, out chan<- Ticker) bool {
	defer conn.Close()
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Binance read error: %v", err)
			b.updateStatus(false, 0)
			return false
		}

		var msg binanceTickerMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		var price float64
		if _, err := fmt.Sscanf(msg.C, "%f", &price); err != nil {
			continue
		}

		tick := Ticker{
			Symbol:    strings.ToUpper(symbol),
			Price:     price,
			Timestamp: time.Now(),
		}
		b.updateStatus(true, 0)
		select {
		case out <- tick:
		default:
			// drop if downstream is slow
		}
	}
}

func (b *BinanceFeed) pollREST(symbol string, out chan<- Ticker) {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(b.restInterval)
	defer ticker.Stop()

	for {
		price, err := b.fetchREST(symbol)
		if err == nil {
			tick := Ticker{
				Symbol:    strings.ToUpper(symbol),
				Price:     price,
				Timestamp: time.Now(),
			}
			// Healthy=false 表示正在使用降级数据源
			b.updateStatus(false, b.restInterval.Milliseconds())
			select {
			case out <- tick:
			default:
			}
		}

		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (b *BinanceFeed) fetchREST(symbol string) (float64, error) {
	url := fmt.Sprintf(binanceRESTURL, strings.ToUpper(symbol))
	resp, err := b.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance rest status %d", resp.StatusCode)
	}

	var data binanceRestResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}
	var price float64
	if _, err := fmt.Sscanf(data.Price, "%f", &price); err != nil {
		return 0, err
	}
	return price, nil
}

func (b *BinanceFeed) updateStatus(healthy bool, delayMs int64) {
	b.status = FeedStatus{
		Source:       "binance",
		Healthy:      healthy,
		DelayMs:      delayMs,
		LastUpdateAt: time.Now(),
	}
	if b.onEvent != nil {
		b.onEvent(b.status)
	}
}
