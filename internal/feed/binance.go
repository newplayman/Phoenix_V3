package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const binanceWSURL = "wss://stream.binance.com:9443/ws"

type BinanceFeed struct {
	conn *websocket.Conn
}

type binanceTickerMsg struct {
	S string `json:"s"` // Symbol
	C string `json:"c"` // Price
}

func NewBinanceFeed() *BinanceFeed {
	return &BinanceFeed{}
}

func (b *BinanceFeed) Start(ctx context.Context) error {
	// Simple implementation doesn't need a persistent background loop if it's just a passive subscriber
	// But usually we manage reconnection here. For Phase 1 we keep it simple.
	return nil
}

func (b *BinanceFeed) SubscribeTicker(symbol string) (<-chan Ticker, error) {
	out := make(chan Ticker, 10)

	// Binance stream name: <symbol>@miniTicker or <symbol>@ticker
	streamName := fmt.Sprintf("%s@miniTicker", strings.ToLower(symbol))
	url := fmt.Sprintf("%s/%s", binanceWSURL, streamName)

	log.Printf("Connecting to Binance: %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	b.conn = conn

	go func() {
		defer close(out)
		defer conn.Close()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Binance read error: %v", err)
				return
			}

			var msg binanceTickerMsg
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			// Parse price
			var price float64
			fmt.Sscanf(msg.C, "%f", &price) // Simple parse

			out <- Ticker{
				Symbol:    msg.S,
				Price:     price,
				Timestamp: time.Now(),
			}
		}
	}()

	return out, nil
}
