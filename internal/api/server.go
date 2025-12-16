package api

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/events"
	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/poolguard"
	"phoenix-v3/internal/rebalancer"
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
	queue    *strategy.IntentQueue
	lastCEX  *feed.Ticker
	store    *storage.Store
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
	poolsProvider     func() []PoolStatus
	cfgProvider       func() *config.AppConfig
	poolStateProvider func(string) (PoolStateSnapshot, bool)
	eventStream       events.Stream
	reb               rebalancer.Rebalancer
	balanceProvider   func(int64) BalanceReader
	manualOnly        bool

	mu     sync.RWMutex
	status *SystemStatus

	adminToken string

	rlMu sync.Mutex
	rl   map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func NewServer(q *strategy.IntentQueue, store *storage.Store, riskMgr interface{ Snapshot() risk.Snapshot }, poolGuard interface {
	Snapshot() map[string]poolguard.PoolCheckResult
}, poolsProvider func() []PoolStatus) *Server {
	return NewServerWithConfig(q, store, riskMgr, poolGuard, poolsProvider, ServerConfig{})
}

func NewServerWithConfig(q *strategy.IntentQueue, store *storage.Store, riskMgr interface{ Snapshot() risk.Snapshot }, poolGuard interface {
	Snapshot() map[string]poolguard.PoolCheckResult
}, poolsProvider func() []PoolStatus, cfg ServerConfig) *Server {
	return &Server{
		queue:         q,
		store:         store,
		pnlStore:      nil,
		riskMgr:       riskMgr,
		poolGuard:     poolGuard,
		poolsProvider: poolsProvider,
		status: &SystemStatus{
			Healthy:          true,
			LastUpdate:       time.Now(),
			EngineState:      "Running",
			BinanceConnected: cfg.BinanceConnected,
			PriceSource:      cfg.PriceSource,
		},
		adminToken: strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
		rl:         map[string]*rateBucket{},
	}
}

func (s *Server) allowRequest(r *http.Request) bool {
	const burst = 20.0
	const refillPerSec = 10.0
	const pruneMinEntries = 2048
	const pruneIdle = 30 * time.Minute

	host := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if host == "" {
		host = "unknown"
	}

	now := time.Now()
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	// Best-effort pruning to avoid unbounded growth if many distinct hosts hit the API.
	// Rate limiting is a safety layer; it must not become a memory leak.
	if len(s.rl) >= pruneMinEntries {
		for k, v := range s.rl {
			if v == nil || now.Sub(v.last) > pruneIdle {
				delete(s.rl, k)
			}
		}
	}
	b, ok := s.rl[host]
	if !ok || b == nil {
		s.rl[host] = &rateBucket{tokens: burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * refillPerSec
		if b.tokens > burst {
			b.tokens = burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens -= 1
	return true
}

// AttachPnLStore injects a store that supports PnL aggregation.
func (s *Server) AttachPnLStore(pnlStore interface {
	GetDailyPnL(days int) ([]storage.DailyPnL, error)
}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pnlStore = pnlStore
}

func (s *Server) AttachPauseController(pauseCtl interface {
	SetPaused(bool)
	Paused() bool
}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pauseCtl = pauseCtl
}

func (s *Server) AttachCleanupController(cleanupCtl interface {
	TriggerCleanup() error
	InProgress() bool
}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCtl = cleanupCtl
}

func (s *Server) AttachRebalanceController(rebalanceCtl interface{ TriggerRebalance(poolID string) error }) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebalanceCtl = rebalanceCtl
}

// SetManualOnly marks the bot as running in "manual-only" mode (no automatic strategy evaluation loop).
func (s *Server) SetManualOnly(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manualOnly = v
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
	go func() {
		if err := http.ListenAndServe(":"+port, s.Handler()); err != nil {
			log.Printf("[API] server exited: %v", err)
		}
	}()
}

func (s *Server) Handler() http.Handler {
	cors := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				allowed := false
				if s.cfgProvider != nil {
					if cfg := s.cfgProvider(); cfg != nil {
						for _, o := range cfg.API.CORSAllowedOrigins {
							if strings.EqualFold(strings.TrimSpace(o), origin) {
								allowed = true
								break
							}
						}
					}
				}
				if allowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
				// If origin is present but not allowed, do not set allow-origin; browsers will block.
			}
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			if r.Method == http.MethodOptions {
				if origin != "" && w.Header().Get("Access-Control-Allow-Origin") == "" {
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": "cors_forbidden", "message": "origin not allowed"}})
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if !s.allowRequest(r) {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": "rate_limited", "message": "too many requests"}})
				return
			}
			next(w, r)
		}
	}

	mux := http.NewServeMux()

	// Legacy endpoints are disabled by default; they also require v1 auth when enabled.
	// Phoenix console must use /api/v1/* as the contract source of truth (docs/).
	if s.legacyEnabled() {
		mux.HandleFunc("/api/status", cors(s.authV1(s.handleStatus)))
		mux.HandleFunc("/api/intents", cors(s.authV1(s.handleIntents)))
		mux.HandleFunc("/api/trades", cors(s.authV1(s.handleTrades)))
		mux.HandleFunc("/api/risk", cors(s.authV1(s.handleRisk)))
		mux.HandleFunc("/api/intents/detail", cors(s.authV1(s.handleIntentDetails)))
		mux.HandleFunc("/api/pools", cors(s.authV1(s.handlePools)))
		mux.HandleFunc("/api/pnl", cors(s.authV1(s.handlePnL)))
		mux.HandleFunc("/api/control/pause", cors(s.authV1(s.handlePause)))
		mux.HandleFunc("/api/control/resume", cors(s.authV1(s.handleResume)))
		mux.HandleFunc("/api/control/riskmode", cors(s.authV1(s.handleRiskMode)))
		mux.HandleFunc("/api/control/cleanup", cors(s.authV1(s.handleCleanup)))
		mux.HandleFunc("/api/control/rebalance", cors(s.authV1(s.handleRebalance)))
	}

	// v1 console API
	mux.HandleFunc("/api/v1/health", cors(s.authV1(s.handleV1Health)))
	mux.HandleFunc("/api/v1/pools", cors(s.authV1(s.handleV1Pools)))
	mux.HandleFunc("/api/v1/pools/", cors(s.authV1(s.handleV1PoolSubroutes)))
	mux.HandleFunc("/api/v1/intents", cors(s.authV1(s.handleV1Intents)))
	mux.HandleFunc("/api/v1/intents/", cors(s.authV1(s.handleV1IntentByID)))
	mux.HandleFunc("/api/v1/tx", cors(s.authV1(s.handleV1Tx)))
	mux.HandleFunc("/api/v1/audit", cors(s.authV1(s.handleV1Audit)))
	mux.HandleFunc("/api/v1/operations/preview", cors(s.authV1(s.handleV1OperationPreview)))
	mux.HandleFunc("/api/v1/operations/execute", cors(s.authV1(s.handleV1OperationExecute)))
	mux.HandleFunc("/api/v1/stream", cors(s.authV1(s.handleV1Stream)))

	return mux
}

func (s *Server) legacyEnabled() bool {
	// Hard gate via env for safety.
	if strings.TrimSpace(os.Getenv("PHOENIX_ENABLE_LEGACY_API")) == "1" {
		return true
	}
	if s.cfgProvider == nil {
		return false
	}
	cfg := s.cfgProvider()
	if cfg == nil {
		return false
	}
	return cfg.API.EnableLegacy
}

func (s *Server) authV1(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": "auth_unconfigured", "message": "ADMIN_TOKEN not set"}})
			return
		}
		h := strings.TrimSpace(r.Header.Get("Authorization"))
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) || strings.TrimSpace(strings.TrimPrefix(h, prefix)) != s.adminToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": "unauthorized", "message": "invalid token"}})
			return
		}
		next(w, r)
	}
}

func (s *Server) AttachConfigProvider(p func() *config.AppConfig) {
	s.cfgProvider = p
}

func (s *Server) AttachPoolStateProvider(p func(string) (PoolStateSnapshot, bool)) {
	s.poolStateProvider = p
}

func (s *Server) AttachEventStream(stream events.Stream) {
	s.eventStream = stream
}

func (s *Server) AttachRebalancer(r rebalancer.Rebalancer) {
	s.reb = r
}

func (s *Server) AttachBalanceProvider(p func(int64) BalanceReader) {
	s.balanceProvider = p
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
	body := struct {
		Mode string `json:"mode"`
	}{}
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
	body := struct {
		PoolID string `json:"pool_id"`
	}{}
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
		"risk":      s.getRiskSnapshot(),
		"pools":     s.getPoolsSnapshot(),
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
