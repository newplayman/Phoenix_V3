package monitor

import (
	"fmt"
	"net/http"
	"phoenix-v3/internal/config"
)

type Monitor struct {
	cfg config.MonitoringConfig
}

func NewMonitor(cfg config.MonitoringConfig) *Monitor {
	return &Monitor{cfg: cfg}
}

func (m *Monitor) Start() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", m.cfg.Port)
	fmt.Printf("Monitor server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Printf("Monitor server failed: %v\n", err)
	}
}
