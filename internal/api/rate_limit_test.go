package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

func TestV1RateLimitTriggers(t *testing.T) {
	t.Setenv("SUPABASE_DB_URL", "")
	t.Setenv("ADMIN_TOKEN", "testtoken")

	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)
	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})

	h := srv.Handler()
	auth := "Bearer testtoken"

	limited := 0
	for i := 0; i < 120; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("Authorization", auth)
		req.RemoteAddr = "203.0.113.10:12345"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			limited++
			break
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status=%d body=%s", rr.Code, rr.Body.String())
		}
	}
	if limited == 0 {
		t.Fatalf("expected at least one 429 too many requests")
	}
}
