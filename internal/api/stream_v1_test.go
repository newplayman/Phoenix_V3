package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"phoenix-v3/internal/events"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

type sseRecorder struct {
	mu     sync.Mutex
	header http.Header
	code   int
	buf    bytes.Buffer
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{header: make(http.Header)}
}

func (r *sseRecorder) Header() http.Header { return r.header }

func (r *sseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = statusCode
	}
}

func (r *sseRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.buf.Write(p)
}

func (r *sseRecorder) Flush() {}

func (r *sseRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *sseRecorder) statusCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func TestV1StreamEmitsEvents(t *testing.T) {
	t.Setenv("SUPABASE_DB_URL", "")
	t.Setenv("ADMIN_TOKEN", "testtoken")

	store, err := storage.NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q := strategy.NewIntentQueue()
	riskMgr := risk.NewManager(0.05, 5, 0.10)
	srv := NewServerWithConfig(q, store, riskMgr, fakeGuard{}, func() []PoolStatus { return nil }, ServerConfig{})

	es := events.NewMemoryStream(32)
	srv.AttachEventStream(es)

	h := srv.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer testtoken")

	rw := newSSERecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rw, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = es.Publish(context.Background(), events.TopicTicker, map[string]any{"hello": "world"})
		time.Sleep(50 * time.Millisecond)
		out := rw.bodyString()
		if strings.Contains(out, "event: ticker\n") && strings.Contains(out, "\"type\":\"ticker\"") && strings.Contains(out, "\"hello\":\"world\"") {
			cancel()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
			}
			if ct := rw.Header().Get("Content-Type"); ct != "text/event-stream" {
				t.Fatalf("content-type=%q body=%q", ct, out)
			}
			if rw.statusCode() != http.StatusOK {
				t.Fatalf("status=%d body=%q", rw.statusCode(), out)
			}
			return
		}
	}

	cancel()
	<-done
	t.Fatalf("timed out waiting for SSE event; body=%q", rw.bodyString())
}
