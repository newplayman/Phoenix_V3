package api

import (
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/strategy"
	contractv1 "shared/contracts/contract/v1"
)

type Server struct {
	queue   *strategy.IntentQueue
	lastCEX *feed.Ticker
	status  *SystemStatus
	pnl     *PnLSnapshot
	alerts  []Alert
	mux     *http.ServeMux

	market *feed.PriceAggregator

	decisionMu sync.RWMutex
	decision   DecisionStatus

	contractV1Mu      sync.RWMutex
	contractV1RunID   string
	contractV1Mode    contractv1.Mode
	contractV1Started time.Time
	lastIntentV1      *contractv1.IntentV1
	lastRiskV1        *contractv1.RiskDecisionV1
	lastExecV1        *contractv1.ExecutorResultV1
}

type SystemStatus struct {
	Healthy          bool      `json:"healthy"`
	LastUpdate       time.Time `json:"last_update"`
	EngineState      string    `json:"engine_state"`
	BinanceConnected bool      `json:"binance_connected"`
	PriceSource      string    `json:"price_source"`
}

type DecisionStatus struct {
	Enabled           bool                 `json:"enabled"`
	Blocked           bool                 `json:"blocked"`
	BlockReason       string               `json:"block_reason"`
	AutoEvalEnabled   bool                 `json:"auto_eval_enabled"`
	LastEvalAt        time.Time            `json:"last_eval_at"`
	LastEvalAction    string               `json:"last_eval_action"` // noop|mock_rebalance|blocked|...
	LastEvalReason    string               `json:"last_eval_reason"`
	LastIntentType    string               `json:"last_intent_type"`
	LastIntentSummary string               `json:"last_intent_summary"`
	LastIntentFields  map[string]any       `json:"last_intent_fields"`
	LastRiskDecision  *RiskDecisionSummary `json:"last_risk_decision,omitempty"`
	PositionSource    string               `json:"position_source"` // onchain|config_assumed|none
	PositionLower     int64                `json:"position_lower"`
	PositionUpper     int64                `json:"position_upper"`
	PositionTokenID   uint64               `json:"position_token_id"`
	PositionUpdatedAt time.Time            `json:"position_updated_at"`
	LastGate          GateStatus           `json:"last_gate"`
	StatusV1          *contractv1.StatusV1 `json:"status_v1,omitempty"`
}

type RiskDecisionSummary struct {
	At         time.Time `json:"at"`
	Verdict    string    `json:"verdict"` // APPROVE|MODIFY|REJECT
	RuleID     string    `json:"rule_id"`
	Reason     string    `json:"reason"`
	IntentID   string    `json:"intent_id"`
	IntentType string    `json:"intent_type"`
	ChainID    int64     `json:"chain_id"`
	PoolID     string    `json:"pool_id"`
}

type GateStatus struct {
	RiskMode   string `json:"risk_mode"`
	Reason     string `json:"reason"`
	StaleAgeMs int64  `json:"stale_age_ms"`
}

type PnLSnapshot struct {
	UpdatedAt        time.Time `json:"updated_at"`
	PortfolioUSD     float64   `json:"portfolio_usd"`
	BaselineUSD      float64   `json:"baseline_usd"`
	TotalBaselineUSD float64   `json:"total_baseline_usd"`
	DailyGasUSD      float64   `json:"daily_gas_usd"`
	TotalGasUSD      float64   `json:"total_gas_usd"`
	DailyPnLUSD      float64   `json:"daily_pnl_usd"`
	TotalPnLUSD      float64   `json:"total_pnl_usd"`
	TradesCount24h   int       `json:"trades_count_24h"`
}

type Alert struct {
	Key       string    `json:"key"`
	Severity  string    `json:"severity"` // info|warn|critical
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type ServerConfig struct {
	BinanceConnected bool
	PriceSource      string
}

func NewServerWithConfig(q *strategy.IntentQueue, cfg ServerConfig) *Server {
	return &Server{
		queue: q,
		status: &SystemStatus{
			Healthy:          true,
			LastUpdate:       time.Now(),
			EngineState:      "Running",
			BinanceConnected: cfg.BinanceConnected,
			PriceSource:      cfg.PriceSource,
		},
		decision:          DecisionStatus{Enabled: true},
		mux:               http.NewServeMux(),
		contractV1Mode:    contractv1.ModeDryRun,
		contractV1Started: time.Now(),
	}
}

func NewServer(q *strategy.IntentQueue) *Server {
	return NewServerWithConfig(q, ServerConfig{
		BinanceConnected: false,
		PriceSource:      "Fallback",
	})
}

// UpdateCEXPrice updates the internal state for serving
func (s *Server) UpdateCEXPrice(t feed.Ticker) {
	s.lastCEX = &t
	s.status.LastUpdate = time.Now()
}

func (s *Server) SetMarketAggregator(market *feed.PriceAggregator) {
	s.market = market
}

func (s *Server) UpdateDecision(decision DecisionStatus) {
	s.decisionMu.Lock()
	defer s.decisionMu.Unlock()
	if decision.LastRiskDecision == nil && s.decision.LastRiskDecision != nil {
		decision.LastRiskDecision = s.decision.LastRiskDecision
	}
	s.decision = decision
}

func (s *Server) UpdateLastRiskDecision(v RiskDecisionSummary) {
	s.decisionMu.Lock()
	defer s.decisionMu.Unlock()
	cp := v
	s.decision.LastRiskDecision = &cp
}

func (s *Server) SetContractV1RunID(runID string) {
	s.contractV1Mu.Lock()
	defer s.contractV1Mu.Unlock()
	s.contractV1RunID = runID
}

func (s *Server) SetContractV1Mode(mode contractv1.Mode) {
	s.contractV1Mu.Lock()
	defer s.contractV1Mu.Unlock()
	s.contractV1Mode = mode
}

func (s *Server) UpdateLastIntentV1(v contractv1.IntentV1) {
	s.contractV1Mu.Lock()
	defer s.contractV1Mu.Unlock()
	cp := v
	s.lastIntentV1 = &cp
}

func (s *Server) UpdateLastRiskV1(v contractv1.RiskDecisionV1) {
	s.contractV1Mu.Lock()
	defer s.contractV1Mu.Unlock()
	cp := v
	s.lastRiskV1 = &cp
}

func (s *Server) UpdateLastExecV1(v contractv1.ExecutorResultV1) {
	s.contractV1Mu.Lock()
	defer s.contractV1Mu.Unlock()
	cp := v
	s.lastExecV1 = &cp
}

func (s *Server) UpdatePnL(p PnLSnapshot) {
	s.pnl = &p
}

func (s *Server) UpdateAlerts(alerts []Alert) {
	s.alerts = alerts
}

func (s *Server) Start(port string) {
	// CORS middleware
	cors := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-Admin-Token")
			next(w, r)
		}
	}

	s.mux.HandleFunc("/api/status", cors(s.handleStatus))
	s.mux.HandleFunc("/api/intents", cors(s.handleIntents))
	s.mux.HandleFunc("/api/intents/enqueue", cors(s.handleEnqueueIntent))

	log.Printf("API Server starting on :%s", port)
	go func() {
		if err := http.ListenAndServe(":"+port, s.mux); err != nil {
			log.Printf("API Server failed: %v", err)
		}
	}()
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	price := 0.0
	if s.lastCEX != nil {
		price = s.lastCEX.Price
	}

	var market map[string]any
	var risk map[string]any
	decision := s.currentDecision()
	if s.market != nil {
		snap := s.market.Snapshot()
		// Keep legacy fields in `system` somewhat truthful.
		s.status.BinanceConnected = false
		s.status.PriceSource = "WS"
		for _, src := range snap.Sources {
			if src.Name == "binance" && src.Connected {
				s.status.BinanceConnected = true
			}
		}
		market = map[string]any{
			"agg_price":      snap.Aggregate.AggPrice,
			"agg_updated_at": snap.Aggregate.AggUpdatedAt,
			"stale":          snap.Aggregate.Stale,
			"stale_age_ms":   snap.Aggregate.StaleAgeMs,
			"confidence":     snap.Aggregate.Confidence,
			"divergence_pct": snap.Aggregate.DivergencePct,
			"sources":        snap.Sources,
			"symbol":         snap.Symbol,
		}
		risk = map[string]any{
			"mode":   snap.Risk.Mode,
			"reason": snap.Risk.Reason,
		}
	} else {
		market = map[string]any{
			"price":  price,
			"symbol": "ETH/USDT",
		}
		risk = map[string]any{
			"mode":   "unknown",
			"reason": "no_market_aggregator",
		}
	}

	resp := map[string]interface{}{
		"system":   s.status,
		"market":   market,
		"risk":     risk,
		"decision": decision,
		"pnl":      s.pnl,
		"alerts":   s.alerts,
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) currentDecision() DecisionStatus {
	s.decisionMu.RLock()
	out := s.decision
	s.decisionMu.RUnlock()

	enabled := os.Getenv("PHOENIX_MANUAL_ONLY") != "1"
	out.Enabled = enabled
	out.AutoEvalEnabled = os.Getenv("PHOENIX_AUTO_EVAL") == "1" && enabled

	if !enabled {
		out.Blocked = true
		out.BlockReason = "manual_only"
		out.StatusV1 = s.buildStatusV1(out)
		return out
	}
	if s.market == nil {
		out.Blocked = false
		out.BlockReason = ""
		out.StatusV1 = s.buildStatusV1(out)
		return out
	}

	snap := s.market.Snapshot()
	out.LastGate = GateStatus{
		RiskMode:   snap.Risk.Mode,
		Reason:     snap.Risk.Reason,
		StaleAgeMs: snap.Aggregate.StaleAgeMs,
	}
	if strings.ToLower(strings.TrimSpace(snap.Risk.Mode)) != "normal" {
		out.Blocked = true
		out.BlockReason = snap.Risk.Reason
		out.StatusV1 = s.buildStatusV1(out)
		return out
	}

	out.Blocked = false
	out.BlockReason = ""
	out.StatusV1 = s.buildStatusV1(out)
	return out
}

func (s *Server) ContractV1StatusSnapshot() *contractv1.StatusV1 {
	d := s.currentDecision()
	return d.StatusV1
}

func (s *Server) buildStatusV1(decision DecisionStatus) *contractv1.StatusV1 {
	s.contractV1Mu.RLock()
	runID := s.contractV1RunID
	mode := s.contractV1Mode
	startedAt := s.contractV1Started
	lastIntent := s.lastIntentV1
	lastRisk := s.lastRiskV1
	lastExec := s.lastExecV1
	s.contractV1Mu.RUnlock()

	state := contractv1.StateRunning
	if decision.Blocked {
		if decision.BlockReason == "manual_only" {
			state = contractv1.StatePaused
		} else {
			state = contractv1.StateSafeMode
		}
	}

	out := &contractv1.StatusV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		Mode:          mode,
		State:         state,
		UptimeSec:     int64(time.Since(startedAt).Seconds()),
	}
	if lastIntent != nil {
		out.LastIntent = &contractv1.StatusIntentV1{
			IntentType: lastIntent.IntentType,
			IntentID:   lastIntent.IntentID,
			TsLocalMS:  lastIntent.TsLocalMS,
			Summary:    lastIntent.Summary,
			Fields:     lastIntent.Fields,
		}
	}
	if lastRisk != nil {
		out.LastRisk = &contractv1.StatusRiskV1{
			Level:        lastRisk.Level,
			TsLocalMS:    lastRisk.TsLocalMS,
			ReasonsCount: len(lastRisk.Reasons),
		}
	}
	if lastExec != nil {
		out.LastExec = &contractv1.StatusExecV1{
			Status:    lastExec.Status,
			TsLocalMS: lastExec.TsLocalMS,
			ErrorKind: lastExec.ErrorKind,
		}
	}
	return out
}

func (s *Server) handleIntents(w http.ResponseWriter, r *http.Request) {
	// Copy queue logic (this is a bit hacky, normally we'd list them)
	// For now, return a placeholder count
	count := s.queue.Len()
	json.NewEncoder(w).Encode(map[string]int{
		"pending_count": count,
	})
}

type enqueueIntentRequest struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	PoolID          string            `json:"pool_id"`
	ChainID         int64             `json:"chain_id"`
	Urgency         int               `json:"urgency"`
	StrategyVersion string            `json:"strategy_version"`
	RiskMode        string            `json:"risk_mode"`
	Metadata        map[string]string `json:"metadata"`
}

func (s *Server) handleEnqueueIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		http.Error(w, "ADMIN_TOKEN not set on server", http.StatusForbidden)
		return
	}
	if tok := r.Header.Get("X-Admin-Token"); tok == "" || tok != adminToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req enqueueIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	intent, err := normalizeIntent(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.queue.Enqueue(intent)
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"intentId": intent.ID,
	})
}

func normalizeIntent(req enqueueIntentRequest) (strategy.Intent, error) {
	if req.ChainID == 0 {
		return strategy.Intent{}, errors.New("chain_id required")
	}
	if strings.TrimSpace(req.Type) == "" {
		return strategy.Intent{}, errors.New("type required")
	}
	if strings.TrimSpace(req.PoolID) == "" {
		return strategy.Intent{}, errors.New("pool_id required")
	}
	if req.Urgency == 0 {
		req.Urgency = 5
	}
	if req.Metadata == nil {
		req.Metadata = map[string]string{}
	}

	if strings.ToLower(strings.TrimSpace(req.Type)) == "swap" || strings.ToLower(strings.TrimSpace(req.Metadata["action"])) == "swap_exact_in" {
		// Minimal required metadata for swapExactInputSingleMinOut builder in cmd/bot.
		for _, k := range []string{"swap_token_in", "swap_token_out", "swap_amount_in", "swap_pool"} {
			if strings.TrimSpace(req.Metadata[k]) == "" {
				return strategy.Intent{}, errors.New("swap metadata missing: " + k)
			}
		}
		if err := validateSwapPolicy(req.Metadata); err != nil {
			return strategy.Intent{}, err
		}
		req.Metadata["action"] = "swap_exact_in"
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = "api-" + time.Now().UTC().Format("20060102T150405.000Z")
	}

	return strategy.Intent{
		ID:              id,
		Type:            strategy.IntentType(req.Type),
		PoolID:          req.PoolID,
		ChainID:         req.ChainID,
		Urgency:         req.Urgency,
		Deadline:        time.Now().Add(10 * time.Minute),
		StrategyVersion: req.StrategyVersion,
		RiskMode:        req.RiskMode,
		Metadata:        req.Metadata,
	}, nil
}

func parseAddressAllowlistEnv(envKey string) map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return nil
	}
	m := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		s := strings.ToLower(strings.TrimSpace(part))
		if s == "" {
			continue
		}
		// avoid bringing in go-ethereum/common just for this; accept 0x + 40 hex chars
		if len(s) != 42 || !strings.HasPrefix(s, "0x") {
			continue
		}
		ok := true
		for _, ch := range s[2:] {
			if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') {
				continue
			}
			ok = false
			break
		}
		if !ok {
			continue
		}
		// normalize casing: keep 0x + lower
		m["0x"+s[2:]] = struct{}{}
	}
	return m
}

func validateSwapPolicy(meta map[string]string) error {
	if meta == nil {
		return errors.New("swap metadata missing")
	}

	swapPool := strings.ToLower(strings.TrimSpace(meta["swap_pool"]))
	tokenIn := strings.ToLower(strings.TrimSpace(meta["swap_token_in"]))
	tokenOut := strings.ToLower(strings.TrimSpace(meta["swap_token_out"]))
	amtInStr := strings.TrimSpace(meta["swap_amount_in"])
	if swapPool == "" || tokenIn == "" || tokenOut == "" || amtInStr == "" {
		return errors.New("swap requires swap_pool, swap_token_in, swap_token_out, swap_amount_in")
	}

	amountIn := new(big.Int)
	if _, ok := amountIn.SetString(amtInStr, 10); !ok || amountIn.Sign() <= 0 {
		return errors.New("invalid swap_amount_in")
	}

	// Confirm gate (opt-in via env; runtime will also enforce on broadcast).
	if os.Getenv("PHOENIX_SWAP_FORCE_CONFIRM") == "1" {
		want := os.Getenv("PHOENIX_SWAP_CONFIRM_STRING")
		if want == "" {
			want = "I_UNDERSTAND_TESTNET_SWAP"
		}
		if strings.TrimSpace(meta["swap_confirm"]) != want {
			return errors.New("swap_confirm required (set metadata.swap_confirm=" + want + ")")
		}
	}

	if maxStr := strings.TrimSpace(os.Getenv("PHOENIX_SWAP_MAX_AMOUNT_IN")); maxStr != "" {
		maxAmt := new(big.Int)
		if _, ok := maxAmt.SetString(maxStr, 10); ok && maxAmt.Sign() > 0 {
			if amountIn.Cmp(maxAmt) > 0 {
				return errors.New("swap_amount_in exceeds PHOENIX_SWAP_MAX_AMOUNT_IN")
			}
		}
	}

	if maxSlippageStr := strings.TrimSpace(os.Getenv("PHOENIX_SWAP_MAX_SLIPPAGE_BPS")); maxSlippageStr != "" {
		if v, err := strconv.ParseUint(maxSlippageStr, 10, 32); err == nil {
			wantMax := uint32(v)
			if bpsStr := strings.TrimSpace(meta["swap_slippage_bps"]); bpsStr != "" {
				if cur, err := strconv.ParseUint(bpsStr, 10, 32); err == nil {
					if uint32(cur) > wantMax {
						return errors.New("swap_slippage_bps exceeds PHOENIX_SWAP_MAX_SLIPPAGE_BPS")
					}
				}
			}
		}
	}

	if pools := parseAddressAllowlistEnv("PHOENIX_SWAP_ALLOWLIST_POOLS"); pools != nil {
		if _, ok := pools[swapPool]; !ok {
			return errors.New("swap_pool not allowlisted")
		}
	}
	if toks := parseAddressAllowlistEnv("PHOENIX_SWAP_ALLOWLIST_TOKENS"); toks != nil {
		if _, ok := toks[tokenIn]; !ok {
			return errors.New("swap_token_in not allowlisted")
		}
		if _, ok := toks[tokenOut]; !ok {
			return errors.New("swap_token_out not allowlisted")
		}
	}

	return nil
}
