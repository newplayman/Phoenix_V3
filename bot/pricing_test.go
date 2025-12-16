package bot

import (
	"sync/atomic"
	"testing"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/feed"
)

func TestLookupTokenPrice_USDHeuristic(t *testing.T) {
	var s atomic.Value
	s.Store(map[string]float64{})
	if got := LookupTokenPrice(&s, "USDC"); got != 1.0 {
		t.Fatalf("expected 1.0, got %f", got)
	}
}

func TestUpdateTokenPrices_SetsStableAndPriceToken(t *testing.T) {
	cfg := &config.AppConfig{
		Pools: []config.PoolConfig{
			{Token1: "0xTOKEN1", StableTokens: []string{"0xSTABLE"}},
		},
	}
	var s atomic.Value
	UpdateTokenPrices(cfg, &s, feed.Ticker{Price: 2000})
	if got := LookupTokenPrice(&s, "0xtoken1"); got != 2000 {
		t.Fatalf("expected 2000, got %f", got)
	}
	if got := LookupTokenPrice(&s, "0xstable"); got != 1.0 {
		t.Fatalf("expected 1.0, got %f", got)
	}
}
