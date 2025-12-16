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

func TestV1ReadContractShape(t *testing.T) {
	t.Setenv("SUPABASE_DB_URL", "")
	t.Setenv("ADMIN_TOKEN", "testtoken")

	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)
	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})

	cfg := &config.AppConfig{
		API: config.APIConfig{
			ControlPlaneEnabled: false,
			EnableLegacy:        false,
		},
		StrategyVersion: "basic-v1",
		Wallet:          config.WalletConfig{MinIdlePct: 0.05},
		Risk:            config.RiskConfig{MaxSwapSlippagePct: 0.02},
		Strategy:        config.StrategyConfig{Range: config.StrategyRangeConfig{MinWidthPct: 0.002, MaxWidthPct: 0.02}},
		Pools: []config.PoolConfig{
			{
				ID:             "tusd-weth-005",
				ChainID:        11155111,
				Address:        "0x1111111111111111111111111111111111111111",
				Token0:         "0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9",
				Token1:         "0x88048434d39d1174bad7F543C022a02dB70F62c4",
				Token0Decimals: 18,
				Token1Decimals: 6,
				Fee:            500,
				MaxCapPct:      0.5,
				CEXPriceToken:  "0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9",
				StableTokens:   []string{"0x88048434d39d1174bad7F543C022a02dB70F62c4"},
			},
		},
	}

	srv.AttachConfigProvider(func() *config.AppConfig { return cfg })
	srv.AttachPoolStateProvider(func(poolID string) (PoolStateSnapshot, bool) {
		return PoolStateSnapshot{
			PoolID:          poolID,
			ChainID:         11155111,
			PoolAddress:     cfg.Pools[0].Address,
			Token0:          cfg.Pools[0].Token0,
			Token1:          cfg.Pools[0].Token1,
			Token0Decimals:  18,
			Token1Decimals:  6,
			Fee:             500,
			PositionTokenID: "0",
			PosTickLower:    -10,
			PosTickUpper:    10,
			PosLiquidity:    "1",
			DexTick:         0,
			DexPrice:        2000,
			PoolLiquidity:   "1",
			CexPrice:        2000,
			SigmaDaily:      0.1,
			WidthPct:        0.01,
			VolWindow:       "1m",
			Profile:         "normal",
			MinInterval:     "10s",
		}, true
	})

	h := srv.Handler()
	auth := "Bearer testtoken"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", auth)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rr.Code, rr.Body.String())
	}
	var health map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &health)
	if _, ok := health["bot"]; !ok {
		t.Fatalf("missing bot: %v", health)
	}
	botObj, _ := health["bot"].(map[string]any)
	if _, ok := botObj["manual_only"]; !ok {
		t.Fatalf("missing bot.manual_only: %v", health)
	}
	if _, ok := health["rpc"]; !ok {
		t.Fatalf("missing rpc: %v", health)
	}
	if _, ok := health["safety"]; !ok {
		t.Fatalf("missing safety: %v", health)
	}
	if _, ok := health["risk"]; !ok {
		t.Fatalf("missing risk: %v", health)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	req2.Header.Set("Authorization", auth)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("pools status=%d body=%s", rr2.Code, rr2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/pools/tusd-weth-005/state", nil)
	req3.Header.Set("Authorization", auth)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("state status=%d body=%s", rr3.Code, rr3.Body.String())
	}

	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/tx?limit=10", nil)
	req4.Header.Set("Authorization", auth)
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("tx status=%d body=%s", rr4.Code, rr4.Body.String())
	}

	reqAudit := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=10", nil)
	reqAudit.Header.Set("Authorization", auth)
	rrAudit := httptest.NewRecorder()
	h.ServeHTTP(rrAudit, reqAudit)
	if rrAudit.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", rrAudit.Code, rrAudit.Body.String())
	}
	var auditResp map[string]any
	_ = json.Unmarshal(rrAudit.Body.Bytes(), &auditResp)
	if _, ok := auditResp["actions"]; !ok {
		t.Fatalf("missing actions: %v", auditResp)
	}

	req5 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/preview", nil)
	req5.Header.Set("Authorization", auth)
	rr5 := httptest.NewRecorder()
	h.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected control plane disabled, got %d body=%s", rr5.Code, rr5.Body.String())
	}
}
