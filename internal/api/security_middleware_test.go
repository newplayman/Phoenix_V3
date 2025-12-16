package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

func TestCORS_OptionsAllowedOrigin(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "testtoken")
	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)

	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	srv.AttachConfigProvider(func() *config.AppConfig {
		return &config.AppConfig{
			API: config.APIConfig{
				CORSAllowedOrigins: []string{"http://localhost:5173"},
			},
			StrategyVersion: "basic-v1",
			Chains:          []config.ChainConfig{{ID: 421614, Name: "arbitrum-sepolia", RPC: "http://localhost"}},
			Pools: []config.PoolConfig{{
				ID:              "pool",
				ChainID:         421614,
				Token0:          "0x0000000000000000000000000000000000000001",
				Token1:          "0x0000000000000000000000000000000000000002",
				CEXPriceToken:   "0x0000000000000000000000000000000000000002",
				Token0Decimals:  18,
				Token1Decimals:  6,
				Fee:             500,
				Address:         "0x0000000000000000000000000000000000000003",
				PositionManager: "0x0000000000000000000000000000000000000004",
				MaxCapPct:       0.5,
				StableTokens:    []string{"0x0000000000000000000000000000000000000001"},
			}},
			Risk:   config.RiskConfig{MaxDailyGas: 0.01, MaxDrawdown: 0.10, ConsecutiveFails: 3},
			Wallet: config.WalletConfig{MinIdlePct: 0.1},
		}
	})

	h := srv.Handler()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected allow origin set, got %q", got)
	}
}

func TestCORS_OptionsForbiddenOrigin(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "testtoken")
	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)

	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	srv.AttachConfigProvider(func() *config.AppConfig {
		return &config.AppConfig{
			API: config.APIConfig{
				CORSAllowedOrigins: []string{"http://localhost:5173"},
			},
			StrategyVersion: "basic-v1",
			Chains:          []config.ChainConfig{{ID: 421614, Name: "arbitrum-sepolia", RPC: "http://localhost"}},
			Pools: []config.PoolConfig{{
				ID:              "pool",
				ChainID:         421614,
				Token0:          "0x0000000000000000000000000000000000000001",
				Token1:          "0x0000000000000000000000000000000000000002",
				CEXPriceToken:   "0x0000000000000000000000000000000000000002",
				Token0Decimals:  18,
				Token1Decimals:  6,
				Fee:             500,
				Address:         "0x0000000000000000000000000000000000000003",
				PositionManager: "0x0000000000000000000000000000000000000004",
				MaxCapPct:       0.5,
				StableTokens:    []string{"0x0000000000000000000000000000000000000001"},
			}},
			Risk:   config.RiskConfig{MaxDailyGas: 0.01, MaxDrawdown: 0.10, ConsecutiveFails: 3},
			Wallet: config.WalletConfig{MinIdlePct: 0.1},
		}
	})

	h := srv.Handler()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no allow origin for forbidden origin, got %q", got)
	}
	var payload map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	if code, _ := payload["error"].(map[string]any)["code"].(string); code != "cors_forbidden" {
		t.Fatalf("expected cors_forbidden, got payload=%v", payload)
	}
}

func TestRateLimit_PerIPBucket(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "testtoken")
	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)

	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	h := srv.Handler()

	ok := 0
	limited := 0
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		req.Header.Set("Authorization", "Bearer testtoken")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			ok++
			continue
		}
		if rr.Code == http.StatusTooManyRequests {
			limited++
			continue
		}
		t.Fatalf("unexpected status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ok != 20 {
		t.Fatalf("expected 20 ok requests before limiting, got ok=%d limited=%d", ok, limited)
	}
	if limited == 0 {
		t.Fatalf("expected at least 1 rate-limited response")
	}
}
