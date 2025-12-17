package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/rebalancer"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

type fakeGuard struct{}

func (f fakeGuard) Snapshot() map[string]poolguard.PoolCheckResult {
	return map[string]poolguard.PoolCheckResult{}
}

type fakeBalanceReader struct {
	wallet common.Address
	bals   map[common.Address]*big.Int
}

func (f *fakeBalanceReader) BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error) {
	if b, ok := f.bals[token]; ok && b != nil {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}

func (f *fakeBalanceReader) WalletAddress() common.Address { return f.wallet }

func TestV1OperationsPreviewExecute_IdempotencyAndConfirm(t *testing.T) {
	t.Setenv("SUPABASE_DB_URL", "")
	t.Setenv("ADMIN_TOKEN", "testtoken")

	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)

	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	srv.UpdateCEXPrice(feedTicker(2000))

	cfg := &config.AppConfig{
		StrategyVersion: "basic-v1",
		API:             config.APIConfig{ControlPlaneEnabled: true},
		Wallet:          config.WalletConfig{MinIdlePct: 0.05},
		Risk:            config.RiskConfig{MaxSwapSlippagePct: 0.02},
		Strategy: config.StrategyConfig{
			Range: config.StrategyRangeConfig{MinWidthPct: 0.002, MaxWidthPct: 0.02},
		},
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
		if poolID != "tusd-weth-005" {
			return PoolStateSnapshot{}, false
		}
		return PoolStateSnapshot{
			PoolID:          "tusd-weth-005",
			ChainID:         11155111,
			Token0:          cfg.Pools[0].Token0,
			Token1:          cfg.Pools[0].Token1,
			Token0Decimals:  18,
			Token1Decimals:  6,
			Fee:             500,
			PositionTokenID: "",
			DexTick:         -195490,
			DexPrice:        3200,
			PoolLiquidity:   "1000000",
			CexPrice:        2000,
			SigmaDaily:      0.5,
			WidthPct:        0.002,
			VolWindow:       "1m",
			Profile:         "normal",
			MinInterval:     "10s",
		}, true
	})

	stable := common.HexToAddress(cfg.Pools[0].Token1)
	weth := common.HexToAddress(cfg.Pools[0].Token0)
	srv.AttachBalanceProvider(func(chainID int64) BalanceReader {
		return &fakeBalanceReader{
			wallet: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			bals: map[common.Address]*big.Int{
				stable: big.NewInt(1_000_000_000), // 1000 stable (6 decimals)
				weth:   big.NewInt(0),
			},
		}
	})
	srv.AttachRebalancer(rebalancer.NewRebalancer())

	h := srv.Handler()

	previewBody := opPreviewRequest{
		ActionType:     "force_rebalance",
		PoolID:         "tusd-weth-005",
		ChainID:        11155111,
		IdempotencyKey: "k1",
	}
	b1, _ := json.Marshal(previewBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operations/preview", bytes.NewReader(b1))
	req.Header.Set("Authorization", "Bearer testtoken")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp1 map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp1)
	op1, _ := resp1["operation_id"].(string)
	if op1 == "" {
		t.Fatalf("missing operation_id: %v", resp1)
	}
	if plan, ok := resp1["plan"].([]interface{}); !ok || len(plan) == 0 {
		t.Fatalf("missing plan: %v", resp1["plan"])
	}

	// Same idempotency_key should return the same operation_id (within expires window).
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/preview", bytes.NewReader(b1))
	req2.Header.Set("Authorization", "Bearer testtoken")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("preview2 status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	var resp2 map[string]interface{}
	_ = json.Unmarshal(rr2.Body.Bytes(), &resp2)
	op2, _ := resp2["operation_id"].(string)
	if op2 != op1 {
		t.Fatalf("expected same operation_id, got %q vs %q", op2, op1)
	}

	// Execute without confirm should fail.
	execBody := opExecuteRequest{OperationID: op1, PoolID: "tusd-weth-005", ConfirmText: "", Reason: "x"}
	bExec, _ := json.Marshal(execBody)
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/execute", bytes.NewReader(bExec))
	req3.Header.Set("Authorization", "Bearer testtoken")
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("execute missing confirm: status=%d body=%s", rr3.Code, rr3.Body.String())
	}

	// Execute with confirm should enqueue exactly one intent.
	execBody.ConfirmText = "CONFIRM"
	execBody.Reason = "test rebalance"
	bExec2, _ := json.Marshal(execBody)
	rr4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/execute", bytes.NewReader(bExec2))
	req4.Header.Set("Authorization", "Bearer testtoken")
	h.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", rr4.Code, rr4.Body.String())
	}
	if q.Len() != 1 {
		t.Fatalf("expected queue len=1, got %d", q.Len())
	}

	// Execute again should be idempotent and not enqueue again.
	rr5 := httptest.NewRecorder()
	req5 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/execute", bytes.NewReader(bExec2))
	req5.Header.Set("Authorization", "Bearer testtoken")
	h.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusOK {
		t.Fatalf("execute2 status=%d body=%s", rr5.Code, rr5.Body.String())
	}
	if q.Len() != 1 {
		t.Fatalf("expected queue len=1 after idempotent execute, got %d", q.Len())
	}
}

func TestV1OperationsPreview_UnsupportedActionType(t *testing.T) {
	t.Setenv("SUPABASE_DB_URL", "")
	t.Setenv("ADMIN_TOKEN", "testtoken")

	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)

	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	srv.UpdateCEXPrice(feedTicker(2000))

	cfg := &config.AppConfig{
		StrategyVersion: "basic-v1",
		API:             config.APIConfig{ControlPlaneEnabled: true},
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
			PoolID:          "tusd-weth-005",
			ChainID:         11155111,
			Token0:          cfg.Pools[0].Token0,
			Token1:          cfg.Pools[0].Token1,
			Token0Decimals:  18,
			Token1Decimals:  6,
			Fee:             500,
			PositionTokenID: "",
			DexTick:         -195490,
			DexPrice:        3200,
			PoolLiquidity:   "1000000",
			CexPrice:        2000,
			SigmaDaily:      0.5,
			WidthPct:        0.002,
			VolWindow:       "1m",
			Profile:         "normal",
			MinInterval:     "10s",
		}, true
	})
	srv.AttachBalanceProvider(func(chainID int64) BalanceReader {
		return &fakeBalanceReader{
			wallet: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			bals:   map[common.Address]*big.Int{},
		}
	})
	srv.AttachRebalancer(rebalancer.NewRebalancer())

	h := srv.Handler()
	body := opPreviewRequest{ActionType: "pause_pool", PoolID: "tusd-weth-005", ChainID: 11155111, IdempotencyKey: "k-unsupported"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operations/preview", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer testtoken")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["error"] == nil || out["error"]["code"] != "unsupported" {
		t.Fatalf("expected error.code=unsupported, got %v", out)
	}
}

func feedTicker(price float64) feed.Ticker {
	return feed.Ticker{Symbol: "ETHUSDT", Price: price, Timestamp: time.Now()}
}
