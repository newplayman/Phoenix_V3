package bot

import (
	"math"
	"math/big"
	"strings"
	"sync/atomic"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/feed"
)

func UpdateTokenPrices(cfg *config.AppConfig, store *atomic.Value, ticker feed.Ticker) {
	if cfg == nil || store == nil {
		return
	}
	current := map[string]float64{}
	if prev := store.Load(); prev != nil {
		if m, ok := prev.(map[string]float64); ok {
			for k, v := range m {
				current[k] = v
			}
		}
	}

	for _, pool := range cfg.Pools {
		priceToken := pool.CEXPriceToken
		if priceToken == "" {
			priceToken = pool.Token1
		}
		current[strings.ToLower(priceToken)] = ticker.Price
		for _, stable := range pool.StableTokens {
			current[strings.ToLower(stable)] = 1.0
		}
	}
	store.Store(current)
}

func LookupTokenPrice(store *atomic.Value, token string) float64 {
	if token == "" || store == nil {
		return 0
	}
	data := store.Load()
	if data == nil {
		return 0
	}
	if price, ok := data.(map[string]float64)[strings.ToLower(token)]; ok {
		return price
	}
	if strings.Contains(strings.ToUpper(token), "USD") {
		return 1.0
	}
	return 0
}

func FloatFromBigInt(amount *big.Int, decimals int) float64 {
	if amount == nil {
		return 0
	}
	if decimals <= 0 {
		decimals = 18
	}
	f, _ := new(big.Float).SetInt(amount).Float64()
	return f / math.Pow10(decimals)
}
