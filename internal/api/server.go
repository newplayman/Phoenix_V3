package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

type ServerConfig struct {
	BinanceConnected bool
	PriceSource      string
}

type SystemStatus struct {
	Healthy          bool      `json:"healthy"`
	LastUpdate       time.Time `json:"last_update"`
	EngineState      string    `json:"engine_state"`
	BinanceConnected bool      `json:"binance_connected"`
	PriceSource      string    `json:"price_source"`
}

type Server struct {
	queue   *strategy.IntentQueue
	lastCEX *feed.Ticker
	store interface {
		GetRecentTrades(limit int) ([]storage.TradeRecord, error)
	}
	pnlStore interface {
		GetDailyPnL(days int) ([]storage.DailyPnL, error)
	}
	riskMgr interface {
		Snapshot() risk.Snapshot
	}
	pauseCtl interface {
		SetPaused(bool)
		Paused() bool
	}
	cleanupCtl interface {
		TriggerCleanup() error
		InProgress() bool
	}
	rebalanceCtl interface {
		TriggerRebalance(poolID string) error
	}
	poolGuard interface {
		Snapshot() map[string]poolguard.PoolCheckResult
	}
	poolsProvider func() []PoolStatus

	mu     sync.RWMutex
	status *SystemStatus
}

func NewServer(q *strategy.IntentQueue, store interface{ GetRecentTrades(limit int) ([]storage.TradeRecord, error) }, riskMgr interface{ Snapshot() risk.Snapshot }, poolGuard interface{ Snapshot() map[string]poolguard.PoolCheckResult }, poolsProvider func() []PoolStatus) *Server {
	return NewServerWithConfig(q, store, riskMgr, poolGuard, poolsProvider, ServerConfig{})
}

func NewServerWithConfig(q *strategy.IntentQueue, store interface{ GetRecentTrades(limit int) ([]storage.TradeRecord, error) }, riskMgr interface{ Snapshot() risk.Snapshot }, poolGuard interface{ Snapshot() map[string]poolguard.PoolCheckResult }, poolsProvider func() []PoolStatus, cfg ServerConfig) *Server {
	return &Server{
		queue: q,
		store: store,
		pnlStore: nil,
		riskMgr: riskMgr,
		poolGuard: poolGuard,
		poolsProvider: poolsProvider,
		status: &SystemStatus{
			Healthy:          true,
			LastUpdate:       time.Now(),
			EngineState:      "Running",
			BinanceConnected: cfg.BinanceConnected,
			PriceSource:      cfg.PriceSource,
		},
	}
}

// AttachPnLStore injects a store that supports PnL aggregation.
func (s *Server) AttachPnLStore(pnlStore interface{ GetDailyPnL(days int) ([]storage.DailyPnL, error) }) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pnlStore = pnlStore
}

func (s *Server) AttachPauseController(pauseCtl interface{ SetPaused(bool); Paused() bool }) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pauseCtl = pauseCtl
}

func (s *Server) AttachCleanupController(cleanupCtl interface{ TriggerCleanup() error; InProgress() bool }) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCtl = cleanupCtl
}

func (s *Server) AttachRebalanceController(rebalanceCtl interface{ TriggerRebalance(poolID string) error }) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebalanceCtl = rebalanceCtl
}

func (s *Server) UpdateCEXPrice(t feed.Ticker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCEX = &t
	s.status.LastUpdate = time.Now()
	if t.Symbol != "" && s.status.PriceSource == "" {
		s.status.PriceSource = t.Symbol
	}
}

func (s *Server) UpdateFeedStatus(status feed.FeedStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status.Source == "binance" {
		s.status.BinanceConnected = status.Healthy
		if status.Healthy {
			s.status.PriceSource = "Binance"
		} else if strings.HasPrefix(strings.ToLower(s.status.PriceSource), "binance") {
			s.status.PriceSource = "Fallback"
		}
	} else if !s.status.BinanceConnected && status.Source != "" && status.Healthy {
		s.status.PriceSource = prettySource(status.Source)
	}
	if !status.LastUpdateAt.IsZero() {
		s.status.LastUpdate = status.LastUpdateAt
	}
}

func (s *Server) Start(port string) {
	cors := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next(w, r)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", cors(s.handleStatus))
	mux.HandleFunc("/api/intents", cors(s.handleIntents))
	mux.HandleFunc("/api/trades", cors(s.handleTrades))
	mux.HandleFunc("/api/risk", cors(s.handleRisk))
	mux.HandleFunc("/api/intents/detail", cors(s.handleIntentDetails))
	mux.HandleFunc("/api/pools", cors(s.handlePools))
	mux.HandleFunc("/api/pnl", cors(s.handlePnL))
	mux.HandleFunc("/api/control/pause", cors(s.handlePause))
	mux.HandleFunc("/api/control/resume", cors(s.handleResume))
	mux.HandleFunc("/api/control/riskmode", cors(s.handleRiskMode))
	mux.HandleFunc("/api/control/cleanup", cors(s.handleCleanup))
	mux.HandleFunc("/api/control/rebalance", cors(s.handleRebalance))

	go func() {
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Printf("[API] server exited: %v", err)
		}
	}()
}

// Control handlers are optional; if not wired, return 501.
func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.pauseCtl != nil {
		s.pauseCtl.SetPaused(true)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "paused"})
		return
	}
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.pauseCtl != nil {
		s.pauseCtl.SetPaused(false)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "running"})
		return
	}
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) handleRiskMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body := struct{ Mode string `json:"mode"` }{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Mode == "" {
		body.Mode = r.URL.Query().Get("mode")
	}
	if setter, ok := s.riskMgr.(interface{ SetMode(risk.RiskMode) error }); ok {
		mode, err := risk.ParseMode(body.Mode)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if err := setter.SetMode(mode); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"mode": string(mode)})
		return
	}
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.cleanupCtl == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if s.cleanupCtl.InProgress() {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "cleanup already in progress"})
		return
	}
	if err := s.cleanupCtl.TriggerCleanup(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleanup started"})
}

func (s *Server) handleRebalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.rebalanceCtl == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	body := struct{ PoolID string `json:"pool_id"` }{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.PoolID == "" {
		body.PoolID = r.URL.Query().Get("pool_id")
	}
	if err := s.rebalanceCtl.TriggerRebalance(body.PoolID); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "rebalance triggered"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	price := 0.0
	if s.lastCEX != nil {
		price = s.lastCEX.Price
	}

	resp := map[string]interface{}{
		"system": s.status,
		"market": map[string]interface{}{
			"price":  price,
			"symbol": "ETH/USDT",
		},
		"control": map[string]interface{}{
			"paused": func() bool {
				if s.pauseCtl == nil {
					return false
				}
				return s.pauseCtl.Paused()
			}(),
			"cleanup_in_progress": func() bool {
				if s.cleanupCtl == nil {
					return false
				}
				return s.cleanupCtl.InProgress()
			}(),
		},
		"risk": s.getRiskSnapshot(),
		"pools": s.getPoolsSnapshot(),
		"poolguard": s.getPoolGuardSnapshot(),
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleIntents(w http.ResponseWriter, r *http.Request) {
	count := s.queue.Len()
	_ = json.NewEncoder(w).Encode(map[string]int{
		"pending_count": count,
	})
}

func (s *Server) handleIntentDetails(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"intents": []strategy.Intent{}})
		return
	}
	intents := s.queue.Snapshot(50)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"intents": intents})
}

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "storage not configured"})
		return
	}
	trades, err := s.store.GetRecentTrades(50)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"trades": trades})
}

func (s *Server) handleRisk(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"risk": s.getRiskSnapshot(),
	})
}

func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"pools": s.getPoolsSnapshot(),
	})
}

func (s *Server) handlePnL(w http.ResponseWriter, r *http.Request) {
	if s.pnlStore == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "storage not configured"})
		return
	}
	series, err := s.pnlStore.GetDailyPnL(30)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"series": series})
}

func (s *Server) getRiskSnapshot() risk.Snapshot {
	if s.riskMgr == nil {
		return risk.Snapshot{}
	}
	return s.riskMgr.Snapshot()
}

func (s *Server) getPoolsSnapshot() []PoolStatus {
	if s.poolsProvider == nil {
		return nil
	}
	return s.poolsProvider()
}

func (s *Server) getPoolGuardSnapshot() map[string]poolguard.PoolCheckResult {
	if s.poolGuard == nil {
		return nil
	}
	return s.poolGuard.Snapshot()
}

type PoolStatus struct {
	PoolID       string  `json:"pool_id"`
	ChainID      int64   `json:"chain_id"`
	DexPrice     float64 `json:"dex_price"`
	CurrentTick  int64   `json:"current_tick"`
	SqrtPriceX96 string  `json:"sqrt_price_x96"`
	Liquidity    string  `json:"liquidity"`
}

func prettySource(src string) string {
	if src == "" {
		return ""
	}
	src = strings.ToLower(src)
	return strings.ToUpper(src[:1]) + src[1:]
}
