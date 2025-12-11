package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type CoinGeckoFeed struct {
	interval time.Duration
	status   FeedStatus
	onEvent  func(FeedStatus)
	client   *http.Client
}

func NewCoinGeckoFeed(interval time.Duration) *CoinGeckoFeed {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &CoinGeckoFeed{
		interval: interval,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *CoinGeckoFeed) OnStatusUpdate(fn func(FeedStatus)) {
	c.onEvent = fn
}

func (c *CoinGeckoFeed) Start(ctx context.Context) error {
	return nil
}

func (c *CoinGeckoFeed) SubscribeTicker(symbol string) (<-chan Ticker, error) {
	out := make(chan Ticker, 1)
	go c.poll(strings.ToLower(symbol), out)
	return out, nil
}

func (c *CoinGeckoFeed) poll(symbol string, out chan<- Ticker) {
	defer close(out)
	for {
		price, err := c.fetchPrice(symbol)
		if err != nil {
			c.updateStatus(false, 0)
			time.Sleep(c.interval)
			continue
		}
		c.updateStatus(true, int64(c.interval/time.Millisecond))
		out <- Ticker{
			Symbol:    strings.ToUpper(symbol),
			Price:     price,
			Timestamp: time.Now(),
		}
		time.Sleep(c.interval)
	}
}

func (c *CoinGeckoFeed) fetchPrice(symbol string) (float64, error) {
	id := mapSymbol(symbol)
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", id)
	resp, err := c.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coingecko status %d", resp.StatusCode)
	}
	var result map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	price := result[id]["usd"]
	if price == 0 {
		return 0, fmt.Errorf("price not found for %s", id)
	}
	return price, nil
}

func mapSymbol(symbol string) string {
	switch strings.ToUpper(symbol) {
	case "ETHUSDT", "ETHUSD", "ETH":
		return "ethereum"
	case "BTCUSDT", "BTCUSD", "BTC":
		return "bitcoin"
	default:
		return "ethereum"
	}
}

func (c *CoinGeckoFeed) updateStatus(healthy bool, delay int64) {
	c.status = FeedStatus{
		Source:       "coingecko",
		Healthy:      healthy,
		DelayMs:      delay,
		LastUpdateAt: time.Now(),
	}
	if c.onEvent != nil {
		c.onEvent(c.status)
	}
}
