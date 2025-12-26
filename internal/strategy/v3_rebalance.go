package strategy

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/contracts"
)

type V3RebalanceConfig struct {
	Enabled         bool
	WidthTicks      int64
	EdgeBufferTicks int64
	Cooldown        time.Duration
	MinTickMove     int64
	TickSpacing     int64

	AssumedLowerTick int64
	AssumedUpperTick int64
}

func defaultV3RebalanceConfig() V3RebalanceConfig {
	return V3RebalanceConfig{
		Enabled:          false,
		WidthTicks:       600,
		EdgeBufferTicks:  60,
		Cooldown:         300 * time.Second,
		MinTickMove:      30,
		TickSpacing:      60,
		AssumedLowerTick: 0,
		AssumedUpperTick: 0,
	}
}

func LoadV3RebalanceConfig(cfg *config.AppConfig) V3RebalanceConfig {
	out := defaultV3RebalanceConfig()

	// First: config.yaml via strategy.params (optional).
	if cfg != nil {
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_ENABLED"); ok {
			out.Enabled = v == 1
		}
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_WIDTH_TICKS"); ok && v > 0 {
			out.WidthTicks = int64(v)
		}
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_EDGE_BUFFER_TICKS"); ok && v >= 0 {
			out.EdgeBufferTicks = int64(v)
		}
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_COOLDOWN_SEC"); ok && v >= 0 {
			out.Cooldown = time.Duration(v) * time.Second
		}
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_MIN_TICK_MOVE"); ok && v >= 0 {
			out.MinTickMove = int64(v)
		}
		if v, ok := getParamInt(cfg.Strategy.Params, "STRAT_V3_TICK_SPACING"); ok && v > 0 {
			out.TickSpacing = int64(v)
		}
		if v, ok := getParamInt64(cfg.Strategy.Params, "STRAT_V3_ASSUMED_LOWER_TICK"); ok {
			out.AssumedLowerTick = v
		}
		if v, ok := getParamInt64(cfg.Strategy.Params, "STRAT_V3_ASSUMED_UPPER_TICK"); ok {
			out.AssumedUpperTick = v
		}
	}

	// Env overrides (required by spec).
	if strings.TrimSpace(os.Getenv("STRAT_V3_ENABLED")) == "1" {
		out.Enabled = true
	}
	if strings.TrimSpace(os.Getenv("STRAT_V3_ENABLED")) == "0" {
		out.Enabled = false
	}
	if v := parseEnvInt("STRAT_V3_WIDTH_TICKS", int(out.WidthTicks)); v > 0 {
		out.WidthTicks = int64(v)
	}
	if v := parseEnvInt("STRAT_V3_EDGE_BUFFER_TICKS", int(out.EdgeBufferTicks)); v >= 0 {
		out.EdgeBufferTicks = int64(v)
	}
	if v := parseEnvInt("STRAT_V3_COOLDOWN_SEC", int(out.Cooldown/time.Second)); v >= 0 {
		out.Cooldown = time.Duration(v) * time.Second
	}
	if v := parseEnvInt("STRAT_V3_MIN_TICK_MOVE", int(out.MinTickMove)); v >= 0 {
		out.MinTickMove = int64(v)
	}
	if v := parseEnvInt("STRAT_V3_TICK_SPACING", int(out.TickSpacing)); v > 0 {
		out.TickSpacing = int64(v)
	}

	if v := strings.TrimSpace(os.Getenv("STRAT_V3_ASSUMED_LOWER_TICK")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out.AssumedLowerTick = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("STRAT_V3_ASSUMED_UPPER_TICK")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out.AssumedUpperTick = n
		}
	}

	if out.TickSpacing <= 0 {
		out.TickSpacing = 60
	}
	// Ensure width aligns to spacing and stays positive.
	out.WidthTicks = alignToSpacing(out.WidthTicks, out.TickSpacing)
	if out.WidthTicks <= 0 {
		out.WidthTicks = out.TickSpacing * 10
	}
	if out.EdgeBufferTicks < 0 {
		out.EdgeBufferTicks = 0
	}
	if out.MinTickMove < 0 {
		out.MinTickMove = 0
	}
	if out.Cooldown < 0 {
		out.Cooldown = 0
	}

	return out
}

type V3RebalanceInput struct {
	ObservedAt time.Time

	PoolTick int64

	// Current position range (if unknown, strategy may use assumed range from config/env).
	CurrentLowerTick int64
	CurrentUpperTick int64

	AggPrice      float64
	DivergencePct float64
	RiskMode      string
	RiskReason    string
	StaleAgeMs    int64
}

type V3RebalanceResult struct {
	Action string // noop|rebalance
	Reason string // out_of_range|near_edge|center_shift|cooldown|...

	CurrentTick int64
	CurLower    int64
	CurUpper    int64

	NewLower     int64
	NewUpper     int64
	NewCenter    int64
	WidthTicks   int64
	BufferTicks  int64
	CooldownLeft int64
}

type V3RebalanceStrategy struct {
	mu sync.Mutex

	lastIntentAt    time.Time
	lastCenterTick  int64
	lastIntentType  string
	lastIntentBrief string
}

func NewV3RebalanceStrategy() *V3RebalanceStrategy {
	return &V3RebalanceStrategy{}
}

func (s *V3RebalanceStrategy) EvaluateAt(cfg V3RebalanceConfig, now time.Time, in V3RebalanceInput) (V3RebalanceResult, *contracts.Intent) {
	if in.ObservedAt.IsZero() {
		in.ObservedAt = now
	}

	currentTick := in.PoolTick
	curLower := in.CurrentLowerTick
	curUpper := in.CurrentUpperTick
	if curLower == 0 && curUpper == 0 && cfg.AssumedLowerTick != 0 && cfg.AssumedUpperTick != 0 {
		curLower = cfg.AssumedLowerTick
		curUpper = cfg.AssumedUpperTick
	}
	// If still unknown, assume centered around current tick.
	if curLower == 0 && curUpper == 0 {
		half := cfg.WidthTicks / 2
		curLower = alignDownToSpacing(currentTick-half, cfg.TickSpacing)
		curUpper = curLower + cfg.WidthTicks
	}

	if curUpper <= curLower {
		return V3RebalanceResult{Action: "noop", Reason: "invalid_range", CurrentTick: currentTick, CurLower: curLower, CurUpper: curUpper}, nil
	}

	curCenter := (curLower + curUpper) / 2
	distToCenter := abs64(currentTick - curCenter)
	targetLower, targetUpper, targetCenter := computeCenteredRange(currentTick, cfg.WidthTicks, cfg.TickSpacing)

	reason := ""
	if currentTick < curLower || currentTick > curUpper {
		reason = "out_of_range"
	} else if currentTick <= curLower+cfg.EdgeBufferTicks || currentTick >= curUpper-cfg.EdgeBufferTicks {
		reason = "near_edge"
	} else if distToCenter >= cfg.MinTickMove {
		reason = "center_shift"
	} else {
		return V3RebalanceResult{
			Action:      "noop",
			Reason:      "in_range",
			CurrentTick: currentTick,
			CurLower:    curLower,
			CurUpper:    curUpper,
			NewLower:    targetLower,
			NewUpper:    targetUpper,
			NewCenter:   targetCenter,
			WidthTicks:  cfg.WidthTicks,
			BufferTicks: cfg.EdgeBufferTicks,
		}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Cooldown > 0 && !s.lastIntentAt.IsZero() {
		since := now.Sub(s.lastIntentAt)
		if since < cfg.Cooldown {
			left := cfg.Cooldown - since
			return V3RebalanceResult{
				Action:       "noop",
				Reason:       "cooldown",
				CurrentTick:  currentTick,
				CurLower:     curLower,
				CurUpper:     curUpper,
				NewLower:     targetLower,
				NewUpper:     targetUpper,
				NewCenter:    targetCenter,
				WidthTicks:   cfg.WidthTicks,
				BufferTicks:  cfg.EdgeBufferTicks,
				CooldownLeft: int64(left.Seconds()),
			}, nil
		}
	}

	// Compute new range centered at current tick.
	newLower, newUpper, newCenter := targetLower, targetUpper, targetCenter

	// Avoid emitting when change is negligible.
	if abs64(newCenter-curCenter) < cfg.MinTickMove && reason != "out_of_range" && reason != "near_edge" {
		return V3RebalanceResult{
			Action:      "noop",
			Reason:      "below_min_move",
			CurrentTick: currentTick,
			CurLower:    curLower,
			CurUpper:    curUpper,
			NewLower:    newLower,
			NewUpper:    newUpper,
			NewCenter:   newCenter,
			WidthTicks:  cfg.WidthTicks,
			BufferTicks: cfg.EdgeBufferTicks,
		}, nil
	}

	intent := contracts.Intent{
		ID:              fmt.Sprintf("intent-%d", now.UnixNano()),
		Type:            contracts.IntentRebalanceV3,
		PoolID:          "v3",
		ChainID:         0,
		Urgency:         3,
		Deadline:        now.Add(10 * time.Minute),
		ExpectedPnL:     0,
		StrategyVersion: "v3-rebalance-v1",
		RiskMode:        strings.TrimSpace(in.RiskMode),
		Metadata: map[string]string{
			"reason":                 reason,
			"observed_at":            in.ObservedAt.UTC().Format(time.RFC3339Nano),
			"agg_price":              fmt.Sprintf("%.8f", in.AggPrice),
			"divergence_pct":         fmt.Sprintf("%.8f", in.DivergencePct),
			"stale_age_ms":           fmt.Sprintf("%d", in.StaleAgeMs),
			"risk_mode":              strings.TrimSpace(in.RiskMode),
			"risk_reason":            strings.TrimSpace(in.RiskReason),
			"current_tick":           fmt.Sprintf("%d", currentTick),
			"current_lower":          fmt.Sprintf("%d", curLower),
			"current_upper":          fmt.Sprintf("%d", curUpper),
			"current_center_tick":    fmt.Sprintf("%d", curCenter),
			"new_lower":              fmt.Sprintf("%d", newLower),
			"new_upper":              fmt.Sprintf("%d", newUpper),
			"new_center_tick":        fmt.Sprintf("%d", newCenter),
			"width_ticks":            fmt.Sprintf("%d", cfg.WidthTicks),
			"edge_buffer_ticks":      fmt.Sprintf("%d", cfg.EdgeBufferTicks),
			"tick_spacing":           fmt.Sprintf("%d", cfg.TickSpacing),
			"cooldown_remaining_sec": "0",
		},
	}

	s.lastIntentAt = now
	s.lastCenterTick = newCenter
	s.lastIntentType = string(intent.Type)
	s.lastIntentBrief = fmt.Sprintf("%s cur=[%d,%d] tick=%d new=[%d,%d]", reason, curLower, curUpper, currentTick, newLower, newUpper)

	return V3RebalanceResult{
		Action:      "rebalance",
		Reason:      reason,
		CurrentTick: currentTick,
		CurLower:    curLower,
		CurUpper:    curUpper,
		NewLower:    newLower,
		NewUpper:    newUpper,
		NewCenter:   newCenter,
		WidthTicks:  cfg.WidthTicks,
		BufferTicks: cfg.EdgeBufferTicks,
	}, &intent
}

func (s *V3RebalanceStrategy) LastIntentSummary() (typ string, brief string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastIntentType, s.lastIntentBrief
}

func computeCenteredRange(currentTick, widthTicks, spacing int64) (lower, upper, center int64) {
	widthTicks = alignToSpacing(widthTicks, spacing)
	if widthTicks <= 0 {
		widthTicks = spacing * 10
	}
	half := widthTicks / 2
	lower = alignDownToSpacing(currentTick-half, spacing)
	upper = lower + widthTicks
	center = (lower + upper) / 2
	return lower, upper, center
}

func alignToSpacing(v, spacing int64) int64 {
	if spacing <= 0 {
		return v
	}
	if v < 0 {
		v = -v
	}
	return (v / spacing) * spacing
}

func alignDownToSpacing(tick, spacing int64) int64 {
	if spacing <= 0 {
		return tick
	}
	// floor division for negatives
	q := tick / spacing
	r := tick % spacing
	if r != 0 && tick < 0 {
		q--
	}
	return q * spacing
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func getParamInt(params map[string]interface{}, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(math.Round(t)), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	default:
		return 0, false
	}
}

func getParamInt64(params map[string]interface{}, key string) (int64, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(math.Round(t)), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func parseEnvInt(key string, def int) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
