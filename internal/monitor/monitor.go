package monitor

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/config"
)

type FeedMetric struct {
	Source       string    `json:"source"`
	Healthy      bool      `json:"healthy"`
	DelayMs      int64     `json:"delay_ms"`
	LastUpdateAt time.Time `json:"last_update_at"`
}

type Monitor struct {
	cfg             config.MonitoringConfig
	statusProvider  func() map[string]interface{}
	metricsProvider func() string

	mu    sync.RWMutex
	feeds map[string]FeedMetric
}

func NewMonitor(cfg config.MonitoringConfig) *Monitor {
	return &Monitor{
		cfg:   cfg,
		feeds: make(map[string]FeedMetric),
	}
}

func (m *Monitor) UpdateFeedMetric(metric FeedMetric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if metric.Source == "" {
		metric.Source = "unknown"
	}
	m.feeds[metric.Source] = metric
}

func (m *Monitor) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		defer m.mu.RUnlock()

		resp := map[string]interface{}{
			"status": "ok",
			"feeds":  m.feeds,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{}
		m.mu.RLock()
		resp["feeds"] = m.feeds
		m.mu.RUnlock()
		if m.statusProvider != nil {
			for k, v := range m.statusProvider() {
				resp[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		var b strings.Builder
		m.mu.RLock()
		for _, metric := range m.feeds {
			b.WriteString(fmt.Sprintf("phoenix_feed_healthy{source=\"%s\"} %d\n", metric.Source, boolToInt(metric.Healthy)))
			b.WriteString(fmt.Sprintf("phoenix_feed_delay_ms{source=\"%s\"} %d\n", metric.Source, metric.DelayMs))
		}
		m.mu.RUnlock()
		if m.metricsProvider != nil {
			b.WriteString(m.metricsProvider())
			if !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
		}
		_, _ = w.Write([]byte(b.String()))
	})

	addr := fmt.Sprintf(":%d", m.cfg.Port)
	log.Printf("Monitor server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Monitor server failed: %v\n", err)
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (m *Monitor) SetStatusProvider(p func() map[string]interface{}) {
	m.statusProvider = p
}

func (m *Monitor) SetMetricsProvider(p func() string) {
	m.metricsProvider = p
}
