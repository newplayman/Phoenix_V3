package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	cfg config.MonitoringConfig

	mu          sync.RWMutex
	feedMetrics map[string]FeedMetric
}

func NewMonitor(cfg config.MonitoringConfig) *Monitor {
	return &Monitor{
		cfg:         cfg,
		feedMetrics: make(map[string]FeedMetric),
	}
}

func (m *Monitor) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		resp := map[string]interface{}{
			"status": "ok",
			"feeds":  m.feedMetrics,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	addr := fmt.Sprintf(":%d", m.cfg.Port)
	fmt.Printf("Monitor server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("Monitor server failed: %v\n", err)
	}
}

func (m *Monitor) UpdateFeedMetric(metric FeedMetric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feedMetrics[metric.Source] = metric
}
