package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	binanceWSURL   = "wss://stream.binance.com:9443/ws"
	binanceRESTURL = "https://api.binance.com/api/v3"
	// Alternative endpoint for restricted regions
	binanceUSRESTURL = "https://api.binance.us/api/v3"
	binanceDEXURL    = "https://dex.binance.org/api/v1"
)

type BinanceFeed struct {
	conn    *websocket.Conn
	status  FeedStatus
	onEvent func(FeedStatus)
}

type FeedStatus struct {
	Source       string
	Healthy      bool
	DelayMs      int64
	LastUpdateAt time.Time
}

type binanceTickerMsg struct {
	S string `json:"s"` // Symbol
	C string `json:"c"` // Price
}

func NewBinanceFeed() *BinanceFeed {
	return &BinanceFeed{}
}

func (b *BinanceFeed) OnStatusUpdate(fn func(FeedStatus)) {
	b.onEvent = fn
}

func (b *BinanceFeed) Start(ctx context.Context) error {
	// Simple implementation doesn't need a persistent background loop if it's just a passive subscriber
	// But usually we manage reconnection here. For Phase 1 we keep it simple.
	return nil
}

// GetPriceREST fetches current price from Binance REST API
type binancePriceResponse struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

type coingeckoResponse map[string]struct {
	USD float64 `json:"usd"`
}

func (b *BinanceFeed) GetPriceCoinGecko(symbol string) (float64, error) {
	// Map symbols to CoinGecko IDs
	symbolToID := map[string]string{
		"ETHUSDT": "ethereum",
		"BTCUSDT": "bitcoin",
	}

	coingeckoID, ok := symbolToID[strings.ToUpper(symbol)]
	if !ok {
		coingeckoID = "ethereum" // Default to ETH
	}

	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", coingeckoID)
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from CoinGecko: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coingecko API returned %d", resp.StatusCode)
	}

	var result coingeckoResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to parse coingecko response: %w", err)
	}

	data, exists := result[coingeckoID]
	if !exists {
		return 0, fmt.Errorf("price data not found for %s", coingeckoID)
	}

	return data.USD, nil
}

func (b *BinanceFeed) GetPriceREST(symbol string) (float64, error) {
	url := fmt.Sprintf("%s/ticker/price?symbol=%s", binanceRESTURL, strings.ToUpper(symbol))
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch price from Binance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance API returned status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	var priceResp binancePriceResponse
	if err := json.Unmarshal(body, &priceResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	var price float64
	if _, err := fmt.Sscanf(priceResp.Price, "%f", &price); err != nil {
		return 0, fmt.Errorf("failed to parse price: %w", err)
	}

	return price, nil
}

func (b *BinanceFeed) SubscribeTicker(symbol string) (<-chan Ticker, error) {
	out := make(chan Ticker, 10)

	// Binance stream name: <symbol>@miniTicker or <symbol>@ticker
	// Use a simpler WebSocket URL format for better compatibility
	streamName := fmt.Sprintf("%s@ticker", strings.ToLower(symbol))
	// Some regions/block require different endpoints
	// Primary: stream.binance.com:9443, Backup: stream.binance.com:443
	backupWSURL := "wss://stream.binance.com/ws"

	// Try primary endpoint first
	url := fmt.Sprintf("%s/%s", binanceWSURL, streamName)
	log.Printf("Trying primary Binance endpoint: %s", url)

	var conn *websocket.Conn
	var err error

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	conn, _, err = dialer.Dial(url, nil)
	if err != nil {
		log.Printf("Primary endpoint failed, trying backup: %v", err)
		url = fmt.Sprintf("%s/%s", backupWSURL, streamName)
		log.Printf("Connecting to backup Binance: %s", url)
		conn, _, err = dialer.Dial(url, nil)
		if err != nil {
			// If WebSocket fails, start REST polling
			log.Printf("WebSocket failed, starting REST polling fallback: %v", err)
			go b.pollREST(symbol, out)
			return out, nil
		}
	}

	log.Printf("Successfully connected to Binance: %s", url)
	b.conn = conn
	b.updateStatus(true, 0)

	go func() {
		defer close(out)
		defer conn.Close()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Binance read error: %v", err)
				// Fallback to REST on WebSocket disconnect
				b.updateStatus(false, 0)
				go b.pollREST(symbol, out)
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
			b.updateStatus(true, 0)
		}
	}()

	return out, nil
}

// pollREST fetches price periodically from REST API when WebSocket fails
func (b *BinanceFeed) pollREST(symbol string, out chan<- Ticker) {
	log.Printf("Starting REST polling for %s", symbol)
	ticker := time.NewTicker(5 * time.Second) // Poll every 5 seconds
	defer ticker.Stop()

	// Try Binance REST first, fallback to CoinGecko
	apis := []struct {
		name string
		fn   func(string) (float64, error)
	}{
		{"Binance", b.GetPriceREST},
		{"CoinGecko", b.GetPriceCoinGecko},
	}

	currentAPI := 0

	for {
		price, err := apis[currentAPI].fn(symbol)
		if err != nil {
			log.Printf("%s poll error: %v", apis[currentAPI].name, err)
			// Try next API
			currentAPI = (currentAPI + 1) % len(apis)
			log.Printf("Switching to %s", apis[currentAPI].name)
			time.Sleep(10 * time.Second)
			continue
		}

		out <- Ticker{
			Symbol:    strings.ToUpper(symbol),
			Price:     price,
			Timestamp: time.Now(),
		}
		b.updateStatus(true, 5000)

		<-ticker.C
	}
}

func (b *BinanceFeed) updateStatus(healthy bool, delay int64) {
	b.status = FeedStatus{
		Source:       "binance",
		Healthy:      healthy,
		DelayMs:      delay,
		LastUpdateAt: time.Now(),
	}
	if b.onEvent != nil {
		b.onEvent(b.status)
	}
}
