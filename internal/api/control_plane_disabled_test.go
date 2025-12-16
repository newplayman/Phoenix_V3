package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/strategy"
)

func TestControlPlane_DisabledByDefault(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "testtoken")

	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)
	srv := NewServerWithConfig(q, nil, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})
	srv.AttachConfigProvider(func() *config.AppConfig {
		return &config.AppConfig{
			API: config.APIConfig{ControlPlaneEnabled: false},
		}
	})

	h := srv.Handler()
	body, _ := json.Marshal(map[string]any{
		"action_type":     "force_rebalance",
		"pool_id":         "pool",
		"chain_id":        421614,
		"params":          map[string]any{},
		"idempotency_key": "k",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operations/preview", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer testtoken")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got == "" || !bytes.Contains([]byte(got), []byte(`"control_plane_disabled"`)) {
		t.Fatalf("expected control_plane_disabled, got body=%s", rr.Body.String())
	}

	execBody, _ := json.Marshal(map[string]any{
		"operation_id":    "op_x",
		"confirm_text":    "CONFIRM",
		"pool_id":         "pool",
		"reason":          "test",
		"idempotency_key": "k2",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operations/execute", bytes.NewReader(execBody))
	req2.Header.Set("Authorization", "Bearer testtoken")
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	if got := rr2.Body.String(); got == "" || !bytes.Contains([]byte(got), []byte(`"control_plane_disabled"`)) {
		t.Fatalf("expected control_plane_disabled, got body=%s", rr2.Body.String())
	}
}
