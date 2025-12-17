package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/events"
	"phoenix-v3/internal/rebalancer"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

// BalanceReader is the minimal gateway interface needed for preview-time balance reads.
type BalanceReader interface {
	BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error)
	WalletAddress() common.Address
}

type PoolStateSnapshot struct {
	PoolID          string
	ChainID         int64
	PoolAddress     string
	Token0          string
	Token1          string
	Token0Decimals  int
	Token1Decimals  int
	Fee             int
	PositionTokenID string
	PosTickLower    int64
	PosTickUpper    int64
	PosLiquidity    string

	DexTick       int64
	DexPrice      float64
	SqrtPriceX96  string
	PoolLiquidity string

	CexPrice    float64
	SigmaDaily  float64
	WidthPct    float64
	VolWindow   string
	Profile     string
	MinInterval string
}

type opPreviewRequest struct {
	ActionType     string                 `json:"action_type"`
	PoolID         string                 `json:"pool_id"`
	ChainID        int64                  `json:"chain_id"`
	Params         map[string]interface{} `json:"params"`
	IdempotencyKey string                 `json:"idempotency_key"`
}

type opExecuteRequest struct {
	OperationID    string `json:"operation_id"`
	ConfirmText    string `json:"confirm_text"`
	PoolID         string `json:"pool_id"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type apiError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code string, message string, details interface{}) {
	writeJSON(w, status, map[string]interface{}{"error": apiError{Code: code, Message: message, Details: details}})
}

func (s *Server) controlPlaneEnabled() bool {
	if strings.TrimSpace(os.Getenv("PHOENIX_CONTROL_PLANE_ENABLED")) == "1" {
		return true
	}
	if s.cfgProvider == nil {
		return false
	}
	cfg := s.cfgProvider()
	if cfg == nil {
		return false
	}
	return cfg.API.ControlPlaneEnabled
}

type operationPreviewPayload struct {
	ActionType string                   `json:"action_type"`
	PoolID     string                   `json:"pool_id"`
	ChainID    int64                    `json:"chain_id"`
	LowerTick  int64                    `json:"lower_tick"`
	UpperTick  int64                    `json:"upper_tick"`
	WidthPct   float64                  `json:"width_pct"`
	SigmaDaily float64                  `json:"sigma_daily"`
	VolWindow  string                   `json:"vol_window"`
	Plan       []map[string]interface{} `json:"plan"`
}

func (s *Server) handleV1Health(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := (*config.AppConfig)(nil)
	if s.cfgProvider != nil {
		cfg = s.cfgProvider()
	}
	safety := config.SafetyFromConfig(cfg)

	riskSnap := s.getRiskSnapshot()
	resp := map[string]interface{}{
		"bot": map[string]interface{}{
			"online":            true,
			"manual_only":       s.manualOnly,
			"last_heartbeat_ts": s.status.LastUpdate.UTC().Format(time.RFC3339),
			"latest_block":      0,
			"queue_depth": func() int {
				if s.queue == nil {
					return 0
				}
				return s.queue.Len()
			}(),
		},
		"rpc": map[string]interface{}{"ok": true, "timeout_rate_5m": 0.0, "p95_latency_ms": 0},
		"safety": map[string]interface{}{
			"dry_run":            safety.DryRun,
			"kill_switch":        safety.KillSwitch,
			"allow_tx_broadcast": safety.AllowTxBroadcast,
			"effective_dry_run":  safety.EffectiveDryRun,
		},
		"risk": map[string]interface{}{"mode": string(riskSnap.Mode), "consecutive_fails": riskSnap.ConsecutiveFails, "daily_gas_used_eth": riskSnap.DailyGasUsed, "daily_gas_limit_eth": riskSnap.MaxDailyGas},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleV1Pools(w http.ResponseWriter, r *http.Request) {
	cfg := (*config.AppConfig)(nil)
	if s.cfgProvider != nil {
		cfg = s.cfgProvider()
	}
	if cfg == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cfgProvider not wired", nil)
		return
	}
	type token struct {
		Address  string `json:"address"`
		Symbol   string `json:"symbol"`
		Decimals int    `json:"decimals"`
	}
	out := make([]map[string]interface{}, 0, len(cfg.Pools))
	for _, p := range cfg.Pools {
		out = append(out, map[string]interface{}{
			"pool_id":      p.ID,
			"chain_id":     p.ChainID,
			"pool_address": p.Address,
			"token0":       token{Address: p.Token0, Symbol: "", Decimals: p.Token0Decimals},
			"token1":       token{Address: p.Token1, Symbol: "", Decimals: p.Token1Decimals},
			"fee":          p.Fee,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"pools": out})
}

func (s *Server) handleV1PoolSubroutes(w http.ResponseWriter, r *http.Request) {
	// /api/v1/pools/{pool_id}/state
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/pools/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "not_found", "unknown subroute", nil)
		return
	}
	poolID := parts[0]
	switch parts[1] {
	case "state":
		s.handleV1PoolState(w, r, poolID)
	case "pause":
		s.handleV1PoolPauseResume(w, r, poolID, true)
	case "resume":
		s.handleV1PoolPauseResume(w, r, poolID, false)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown subroute", nil)
	}
}

func (s *Server) handleV1PoolState(w http.ResponseWriter, r *http.Request, poolID string) {
	if s.poolStateProvider == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "poolStateProvider not wired", nil)
		return
	}
	state, ok := s.poolStateProvider(poolID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown pool", nil)
		return
	}
	width := float64(state.PosTickUpper - state.PosTickLower)
	dLower := 0.0
	dUpper := 0.0
	if state.PositionTokenID != "" && width > 0 {
		dLower = float64(state.DexTick-state.PosTickLower) / width
		dUpper = float64(state.PosTickUpper-state.DexTick) / width
	}
	resp := map[string]interface{}{
		"pool_id":  state.PoolID,
		"chain_id": state.ChainID,
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"dex": map[string]interface{}{
			"tick":                  state.DexTick,
			"price_stable_per_weth": state.DexPrice,
			"liquidity":             state.PoolLiquidity,
		},
		"cex": map[string]interface{}{
			"price_stable_per_weth": state.CexPrice,
			"source":                s.status.PriceSource,
		},
		"position": map[string]interface{}{
			"token_id":              state.PositionTokenID,
			"tick_lower":            state.PosTickLower,
			"tick_upper":            state.PosTickUpper,
			"liquidity":             state.PosLiquidity,
			"in_range":              state.PositionTokenID != "" && state.DexTick >= state.PosTickLower && state.DexTick <= state.PosTickUpper,
			"distance_to_lower_pct": dLower,
			"distance_to_upper_pct": dUpper,
		},
		"strategy": map[string]interface{}{
			"profile":         state.Profile,
			"sigma_daily":     state.SigmaDaily,
			"width_pct":       state.WidthPct,
			"vol_window":      state.VolWindow,
			"cooldown_active": false,
			"min_interval":    state.MinInterval,
		},
		"risk": map[string]interface{}{
			"mode":               string(s.getRiskSnapshot().Mode),
			"consecutive_fails":  s.getRiskSnapshot().ConsecutiveFails,
			"rebalances_last_1h": 0,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleV1Intents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store not configured", nil)
		return
	}
	poolID := r.URL.Query().Get("pool_id")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursorNum, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	intents, next, err := s.store.ListIntents(poolID, status, limit, uint(cursorNum))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
		return
	}
	out := make([]map[string]interface{}, 0, len(intents))
	for _, rec := range intents {
		out = append(out, map[string]interface{}{
			"intent_id":  rec.IntentID,
			"pool_id":    rec.PoolID,
			"chain_id":   rec.ChainID,
			"type":       rec.Type,
			"status":     rec.Status,
			"created_at": rec.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": rec.UpdatedAt.UTC().Format(time.RFC3339),
			"metadata":   json.RawMessage(rec.Metadata),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"intents": out, "next_cursor": fmt.Sprintf("%d", next)})
}

func (s *Server) handleV1IntentByID(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store not configured", nil)
		return
	}
	intentID := strings.TrimPrefix(r.URL.Path, "/api/v1/intents/")
	intentID = strings.Trim(intentID, "/")
	rec, steps, err := s.store.GetIntentWithSteps(intentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "not_found", "intent not found", nil)
		return
	}
	intentObj := map[string]interface{}{
		"intent_id":  rec.IntentID,
		"pool_id":    rec.PoolID,
		"chain_id":   rec.ChainID,
		"type":       rec.Type,
		"status":     rec.Status,
		"created_at": rec.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": rec.UpdatedAt.UTC().Format(time.RFC3339),
		"metadata":   json.RawMessage(rec.Metadata),
	}
	stepObjs := make([]map[string]interface{}, 0, len(steps))
	for _, st := range steps {
		stepObjs = append(stepObjs, map[string]interface{}{
			"step_index": st.StepIndex,
			"step_type":  st.StepType,
			"status":     st.Status,
			"tx_hash":    st.TxHash,
			"details":    json.RawMessage(st.Details),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"intent": intentObj, "steps": stepObjs})
}

func (s *Server) handleV1Tx(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store not configured", nil)
		return
	}
	poolID := r.URL.Query().Get("pool_id")
	intentID := r.URL.Query().Get("intent_id")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursorNum, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)

	trades, next, err := s.store.ListTrades(poolID, intentID, status, limit, uint(cursorNum))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(trades))
	for _, tr := range trades {
		receipt, _ := s.store.GetTxReceipt(tr.ChainID, tr.TxHash)
		var receiptObj interface{} = nil
		if receipt != nil {
			receiptObj = map[string]interface{}{
				"chain_id":            receipt.ChainID,
				"tx_hash":             receipt.TxHash,
				"nonce":               receipt.Nonce,
				"from_addr":           receipt.FromAddr,
				"to_addr":             receipt.ToAddr,
				"status":              receipt.Status,
				"gas_used":            receipt.GasUsed,
				"effective_gas_price": receipt.EffectiveGasPrice,
				"revert_reason":       receipt.RevertReason,
				"mined_at":            receipt.MinedAt.UTC().Format(time.RFC3339),
			}
		}
		out = append(out, map[string]interface{}{
			"chain_id":  tr.ChainID,
			"tx_hash":   tr.TxHash,
			"status":    tr.Status,
			"intent_id": tr.IntentID,
			"pool_id":   tr.PoolID,
			"receipt":   receiptObj,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tx": out, "next_cursor": fmt.Sprintf("%d", next)})
}

type pauseResumeRequest struct {
	ConfirmText    string `json:"confirm_text"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) handleV1PoolPauseResume(w http.ResponseWriter, r *http.Request, poolID string, pause bool) {
	if !s.controlPlaneEnabled() {
		writeError(w, http.StatusServiceUnavailable, "control_plane_disabled", "control plane disabled", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.pauseCtl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "pause controller not wired", nil)
		return
	}
	var req pauseResumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json", nil)
		return
	}
	req.ConfirmText = strings.TrimSpace(req.ConfirmText)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ConfirmText != "CONFIRM" || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "confirm_text=CONFIRM and reason required", nil)
		return
	}

	s.pauseCtl.SetPaused(pause)

	action := "resume_pool"
	if pause {
		action = "pause_pool"
	}
	if s.store != nil && s.cfgProvider != nil {
		cfg := s.cfgProvider()
		chainID := int64(0)
		if cfg != nil {
			if pCfg, ok := findPoolCfg(cfg, poolID); ok {
				chainID = pCfg.ChainID
			}
		}
		_ = s.store.CreateOperatorAction(&storage.OperatorAction{
			TS:         time.Now(),
			Actor:      "admin",
			ActionType: action,
			PoolID:     poolID,
			ChainID:    chainID,
			Request:    datatypes.JSON(mustJSON(req)),
			Result:     datatypes.JSON(mustJSON(map[string]interface{}{"paused": pause})),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"pool_id": poolID, "paused": pause})
}

func (s *Server) handleV1Audit(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store not configured", nil)
		return
	}
	poolID := r.URL.Query().Get("pool_id")
	actionType := r.URL.Query().Get("action_type")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursorNum, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	rows, next, err := s.store.ListOperatorActions(poolID, actionType, limit, uint(cursorNum))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
		return
	}
	out := make([]map[string]interface{}, 0, len(rows))
	for _, a := range rows {
		out = append(out, map[string]interface{}{
			"ts":          a.TS.UTC().Format(time.RFC3339),
			"actor":       a.Actor,
			"action_type": a.ActionType,
			"pool_id":     a.PoolID,
			"chain_id":    a.ChainID,
			"request":     json.RawMessage(a.Request),
			"result":      json.RawMessage(a.Result),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"actions": out, "next_cursor": fmt.Sprintf("%d", next)})
}

func (s *Server) handleV1OperationPreview(w http.ResponseWriter, r *http.Request) {
	if !s.controlPlaneEnabled() {
		writeError(w, http.StatusServiceUnavailable, "control_plane_disabled", "control plane disabled", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil || s.queue == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store/queue not configured", nil)
		return
	}
	if s.cfgProvider == nil || s.poolStateProvider == nil || s.balanceProvider == nil || s.reb == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cfg/poolstate/balance/rebalancer not wired", nil)
		return
	}
	var req opPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json", nil)
		return
	}
	req.ActionType = strings.TrimSpace(req.ActionType)
	req.PoolID = strings.TrimSpace(req.PoolID)
	if req.ActionType == "" || req.PoolID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "action_type and pool_id required", nil)
		return
	}
	if req.ActionType != "force_rebalance" {
		writeError(w, http.StatusBadRequest, "unsupported", "only force_rebalance implemented", nil)
		return
	}

	cfg := s.cfgProvider()
	poolCfg, ok := findPoolCfg(cfg, req.PoolID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown pool", nil)
		return
	}
	state, ok := s.poolStateProvider(req.PoolID)
	if !ok || state.DexPrice <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "pool state not ready", nil)
		return
	}

	widthPct := state.WidthPct
	if widthPct <= 0 {
		widthPct = cfg.Strategy.Range.MinWidthPct
	}
	if widthPct <= 0 {
		widthPct = 0.02
	}

	// Compute target ticks around current DEX price using engine.
	stableIsToken0 := false
	if len(poolCfg.StableTokens) > 0 && strings.EqualFold(poolCfg.StableTokens[0], poolCfg.Token0) {
		stableIsToken0 = true
	}
	cexPrice := state.CexPrice
	if cexPrice <= 0 && s.lastCEX != nil {
		cexPrice = s.lastCEX.Price
	}
	eng := engine.NewStandardASMMEngine()
	out, err := eng.Calculate(engine.EngineInput{
		CexPrice:       cexPrice,
		DexPrice:       state.DexPrice,
		Volatility:     widthPct,
		Token0Decimals: poolCfg.Token0Decimals,
		Token1Decimals: poolCfg.Token1Decimals,
		StableIsToken0: stableIsToken0,
		Params:         engine.StrategyParams{RiskFactor: 1.0, MinSpreadPct: cfg.Strategy.Range.MinWidthPct, MaxSpreadPct: cfg.Strategy.Range.MaxWidthPct},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	lowerTick := out.TargetLowerTick
	upperTick := out.TargetUpperTick
	spacing := strategy.TickSpacingForFee(poolCfg.Fee)
	if spacing <= 0 {
		spacing = 10
	}
	lowerTick = (lowerTick / spacing) * spacing
	upperTick = (upperTick / spacing) * spacing
	if lowerTick >= upperTick {
		upperTick = lowerTick + spacing
	}

	intent := strategy.Intent{
		ID:                "PREVIEW",
		Type:              strategy.IntentRebalance,
		PoolID:            poolCfg.ID,
		ChainID:           poolCfg.ChainID,
		TargetNotionalPct: poolCfg.MaxCapPct,
		Metadata: map[string]string{
			"token0":     poolCfg.Token0,
			"token1":     poolCfg.Token1,
			"fee":        strconv.Itoa(poolCfg.Fee),
			"lower_tick": strconv.FormatInt(lowerTick, 10),
			"upper_tick": strconv.FormatInt(upperTick, 10),
		},
	}

	br := s.balanceProvider(poolCfg.ChainID)
	if br == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "no balance reader", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	balances := map[string]*big.Int{}
	readBal := func(addr string) {
		a := common.HexToAddress(addr)
		if a == (common.Address{}) {
			return
		}
		b, err := br.BalanceOfERC20(ctx, a)
		if err == nil && b != nil {
			balances[addr] = b
		}
	}
	readBal(poolCfg.Token0)
	readBal(poolCfg.Token1)
	for _, st := range poolCfg.StableTokens {
		readBal(st)
	}

	prices := map[string]float64{}
	for _, st := range poolCfg.StableTokens {
		prices[st] = 1.0
	}
	if poolCfg.CEXPriceToken != "" {
		prices[poolCfg.CEXPriceToken] = cexPrice
	}
	// If stable side is token1, price should be 1.0; otherwise set token1 to cex if it is the priced token.
	if _, ok := prices[poolCfg.Token0]; !ok && strings.EqualFold(poolCfg.Token0, poolCfg.CEXPriceToken) {
		prices[poolCfg.Token0] = cexPrice
	}
	if _, ok := prices[poolCfg.Token1]; !ok && strings.EqualFold(poolCfg.Token1, poolCfg.CEXPriceToken) {
		prices[poolCfg.Token1] = cexPrice
	}
	if _, ok := prices[poolCfg.Token0]; !ok && len(poolCfg.StableTokens) > 0 && strings.EqualFold(poolCfg.Token0, poolCfg.StableTokens[0]) {
		prices[poolCfg.Token0] = 1.0
	}
	if _, ok := prices[poolCfg.Token1]; !ok && len(poolCfg.StableTokens) > 0 && strings.EqualFold(poolCfg.Token1, poolCfg.StableTokens[0]) {
		prices[poolCfg.Token1] = 1.0
	}

	var sqrtP *big.Int
	if state.SqrtPriceX96 != "" {
		if v, ok := new(big.Int).SetString(state.SqrtPriceX96, 10); ok {
			sqrtP = v
		}
	}
	plan, err := s.reb.Rebalance(ctx, rebalancer.RebalanceInput{
		Intent:        intent,
		WalletBalance: balances,
		Prices:        prices,
		PoolConfig: rebalancer.PoolConfig{
			PoolID:         poolCfg.ID,
			Token0:         poolCfg.Token0,
			Token1:         poolCfg.Token1,
			Token0Decimals: poolCfg.Token0Decimals,
			Token1Decimals: poolCfg.Token1Decimals,
			Fee:            poolCfg.Fee,
			MaxCapPct:      poolCfg.MaxCapPct,
			StableTokens:   poolCfg.StableTokens,
		},
		RiskLimits: rebalancer.RiskLimits{
			MinIdleCashPct:     cfg.Wallet.MinIdlePct,
			MaxSwapSlippagePct: cfg.Risk.MaxSwapSlippagePct,
		},
		State: rebalancer.PoolStateSnapshot{CurrentTick: state.DexTick, SqrtPriceX96: sqrtP},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "plan_failed", err.Error(), nil)
		return
	}

	steps := make([]map[string]interface{}, 0, 4+len(plan.Swaps))
	if state.PositionTokenID != "" {
		steps = append(steps, map[string]interface{}{"step_index": len(steps), "step_type": "close", "summary": fmt.Sprintf("close existing tokenId=%s", state.PositionTokenID)})
	}
	for _, sw := range plan.Swaps {
		steps = append(steps, map[string]interface{}{
			"step_index":   len(steps),
			"step_type":    "swap",
			"summary":      fmt.Sprintf("%s -> %s amountIn=%s fee=%d", sw.FromToken.Hex(), sw.ToToken.Hex(), sw.AmountIn.String(), sw.Fee),
			"slippage_pct": sw.SlippagePct,
		})
	}
	steps = append(steps, map[string]interface{}{
		"step_index": len(steps),
		"step_type":  "mint",
		"summary":    fmt.Sprintf("mint ticks=[%d,%d] amount0=%s amount1=%s", lowerTick, upperTick, plan.FinalLP.Amount0.String(), plan.FinalLP.Amount1.String()),
	})

	warnings := []string{}
	if strings.TrimSpace(state.PoolLiquidity) == "0" && len(plan.Swaps) > 0 {
		warnings = append(warnings, "pool liquidity is 0; swaps may revert unless liquidity is kept/minted first")
	}

	payload := operationPreviewPayload{
		ActionType: req.ActionType,
		PoolID:     poolCfg.ID,
		ChainID:    poolCfg.ChainID,
		LowerTick:  lowerTick,
		UpperTick:  upperTick,
		WidthPct:   widthPct,
		SigmaDaily: state.SigmaDaily,
		VolWindow:  state.VolWindow,
		Plan:       steps,
	}
	previewJSON, _ := json.Marshal(payload)
	warnJSON, _ := json.Marshal(warnings)

	op := &storage.Operation{
		OperationID:    "op_" + uuid.NewString(),
		Actor:          "admin",
		ActionType:     req.ActionType,
		PoolID:         poolCfg.ID,
		ChainID:        poolCfg.ChainID,
		Status:         "previewed",
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		Preview:        datatypes.JSON(previewJSON),
		Warnings:       datatypes.JSON(warnJSON),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	}

	saved, err := s.store.UpsertOperationPreview(op)
	if err != nil || saved == nil {
		writeError(w, http.StatusInternalServerError, "db_error", "failed to persist preview", nil)
		return
	}
	_ = s.store.CreateOperatorAction(&storage.OperatorAction{
		TS:         time.Now(),
		Actor:      "admin",
		ActionType: "preview_rebalance",
		PoolID:     poolCfg.ID,
		ChainID:    poolCfg.ChainID,
		Request:    datatypes.JSON(mustJSON(map[string]interface{}{"action_type": req.ActionType, "pool_id": req.PoolID, "idempotency_key": req.IdempotencyKey})),
		Result:     datatypes.JSON(mustJSON(map[string]interface{}{"operation_id": saved.OperationID})),
	})

	resp := map[string]interface{}{
		"operation_id":   saved.OperationID,
		"action_type":    req.ActionType,
		"pool_id":        poolCfg.ID,
		"warnings":       warnings,
		"estimated_gas":  nil,
		"plan":           payload.Plan,
		"expires_in_sec": int(time.Until(saved.ExpiresAt).Seconds()),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleV1OperationExecute(w http.ResponseWriter, r *http.Request) {
	if !s.controlPlaneEnabled() {
		writeError(w, http.StatusServiceUnavailable, "control_plane_disabled", "control plane disabled", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil || s.queue == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "store/queue not configured", nil)
		return
	}
	var req opExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json", nil)
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.PoolID = strings.TrimSpace(req.PoolID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.OperationID == "" || req.PoolID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "operation_id and pool_id required", nil)
		return
	}
	if req.ConfirmText != "CONFIRM" || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "confirm_text=CONFIRM and reason required", nil)
		return
	}

	op, err := s.store.GetOperationByOperationID(req.OperationID)
	if err != nil || op == nil {
		writeError(w, http.StatusNotFound, "not_found", "operation not found", nil)
		return
	}
	if !strings.EqualFold(op.PoolID, req.PoolID) {
		writeError(w, http.StatusBadRequest, "bad_request", "pool_id mismatch", nil)
		return
	}
	if !op.ExpiresAt.IsZero() && time.Now().After(op.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "expired", "preview expired, please preview again", nil)
		return
	}
	if op.IntentID != "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"operation_id": op.OperationID, "status": op.Status, "intent_id": op.IntentID, "links": map[string]string{"intent": "/api/v1/intents/" + op.IntentID}})
		return
	}

	var preview operationPreviewPayload
	if len(op.Preview) == 0 || json.Unmarshal(op.Preview, &preview) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid operation preview payload", nil)
		return
	}

	if s.cfgProvider == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cfg provider not wired", nil)
		return
	}
	cfg := s.cfgProvider()
	if cfg == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cfg provider returned nil", nil)
		return
	}
	poolCfg, ok := findPoolCfg(cfg, req.PoolID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown pool", nil)
		return
	}

	intentID := "intent-" + uuid.NewString()
	intent := strategy.Intent{
		ID:                intentID,
		Type:              strategy.IntentRebalance,
		PoolID:            poolCfg.ID,
		ChainID:           poolCfg.ChainID,
		TargetNotionalPct: poolCfg.MaxCapPct,
		StrategyVersion:   cfg.StrategyVersion,
		RiskMode:          string(s.getRiskSnapshot().Mode),
		Metadata: map[string]string{
			"token0":       poolCfg.Token0,
			"token1":       poolCfg.Token1,
			"fee":          strconv.Itoa(poolCfg.Fee),
			"lower_tick":   strconv.FormatInt(preview.LowerTick, 10),
			"upper_tick":   strconv.FormatInt(preview.UpperTick, 10),
			"operation_id": op.OperationID,
			"reason":       req.Reason,
		},
	}

	// Persist intent record before enqueue (for auditability).
	metaJSON, _ := json.Marshal(intent.Metadata)
	_ = s.store.UpsertIntentRecord(&storage.IntentRecord{
		IntentID:        intentID,
		PoolID:          intent.PoolID,
		ChainID:         intent.ChainID,
		Type:            string(intent.Type),
		Status:          "generated",
		RiskMode:        intent.RiskMode,
		StrategyVersion: intent.StrategyVersion,
		Metadata:        datatypes.JSON(metaJSON),
	})

	// Update operation + audit action.
	_ = s.store.UpdateOperationExecute(op.OperationID, "queued", req.Reason, intentID)
	_ = s.store.CreateOperatorAction(&storage.OperatorAction{
		TS:         time.Now(),
		Actor:      "admin",
		ActionType: "execute_rebalance",
		PoolID:     poolCfg.ID,
		ChainID:    poolCfg.ChainID,
		Request:    datatypes.JSON(mustJSON(req)),
		Result:     datatypes.JSON(mustJSON(map[string]interface{}{"operation_id": op.OperationID, "intent_id": intentID})),
	})

	s.queue.Enqueue(intent)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"operation_id": op.OperationID,
		"status":       "queued",
		"intent_id":    intentID,
		"links":        map[string]string{"intent": "/api/v1/intents/" + intentID},
	})
}

func (s *Server) handleV1Stream(w http.ResponseWriter, r *http.Request) {
	if s.eventStream == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "event stream not configured", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	type sub struct {
		ch    <-chan events.Event
		unsub func()
	}
	topics := []events.Topic{events.TopicTicker, events.TopicPoolState, events.TopicIntentExec, events.TopicStrategy, events.TopicRisk, events.TopicAudit}
	subs := make([]sub, 0, len(topics))
	for _, t := range topics {
		ch, unsub, err := s.eventStream.Subscribe(t)
		if err != nil {
			continue
		}
		subs = append(subs, sub{ch: ch, unsub: unsub})
	}
	defer func() {
		for _, ss := range subs {
			if ss.unsub != nil {
				ss.unsub()
			}
		}
	}()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			_, _ = fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
			flusher.Flush()
		default:
		}

		// multiplex: simple round-robin poll with short sleep (low volume expected).
		delivered := false
		for _, ss := range subs {
			select {
			case ev := <-ss.ch:
				eventType := string(ev.Topic)
				ts := ev.Timestamp
				if ts.IsZero() {
					ts = time.Now()
				}

				var out interface{}
				if m, ok := ev.Payload.(map[string]interface{}); ok && m != nil {
					cp := make(map[string]interface{}, len(m)+2)
					for k, v := range m {
						cp[k] = v
					}
					cp["type"] = eventType
					cp["ts"] = ts.UTC().Format(time.RFC3339)
					out = cp
				} else {
					out = map[string]interface{}{
						"type": eventType,
						"ts":   ts.UTC().Format(time.RFC3339),
						"data": ev.Payload,
					}
				}

				payload, _ := json.Marshal(out)
				_, _ = fmt.Fprintf(w, "event: %s\n", eventType)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
				delivered = true
			default:
			}
		}
		if !delivered {
			time.Sleep(150 * time.Millisecond)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func findPoolCfg(cfg *config.AppConfig, poolID string) (config.PoolConfig, bool) {
	if cfg == nil {
		return config.PoolConfig{}, false
	}
	for _, p := range cfg.Pools {
		if strings.EqualFold(p.ID, poolID) {
			return p, true
		}
	}
	return config.PoolConfig{}, false
}

func hashRPCURL(url string) string {
	if strings.TrimSpace(url) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}
