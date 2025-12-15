package strategy

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"sync"
	"time"

	"phoenix-v3/internal/engine"
)

type BasicStrategy struct {
	eng engine.Engine

	mu  sync.RWMutex
	cfg BasicStrategyConfig
}

type BasicStrategyConfig struct {
	PoolID            string
	ChainID           int64
	Token0Address     string
	Token1Address     string
	Token0Decimals    int
	Token1Decimals    int
	Fee               int
	PositionManager   string
	Amount0Desired    string
	Amount1Desired    string
	StrategyVersion   string
	RiskMode          string
	MinSpreadTicks    int
	TargetNotionalPct float64
	MaxCapPct         float64
	TickSpacing       int64
	EngineRiskFactor  float64
}

func NewBasicStrategy(cfg BasicStrategyConfig) *BasicStrategy {
	strat := &BasicStrategy{
		eng: engine.NewStandardASMMEngine(),
	}
	strat.UpdateConfig(cfg)
	return strat
}

func (s *BasicStrategy) UpdateConfig(cfg BasicStrategyConfig) {
	if cfg.MinSpreadTicks <= 0 {
		cfg.MinSpreadTicks = 100
	}
	if cfg.RiskMode == "" {
		cfg.RiskMode = "normal"
	}
	if cfg.TargetNotionalPct <= 0 && cfg.RiskMode != "frozen" {
		cfg.TargetNotionalPct = 0.05
	}
	if cfg.EngineRiskFactor <= 0 && cfg.RiskMode != "frozen" {
		cfg.EngineRiskFactor = 1.0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

func (s *BasicStrategy) Evaluate(ctx context.Context, input engine.EngineInput) ([]Intent, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if cfg.PoolID == "" {
		return nil, nil
	}

	target, err := s.eng.Calculate(input)
	if err != nil {
		return nil, err
	}

	diffLower := math.Abs(float64(target.TargetLowerTick - input.Position.LowerTick))
	diffUpper := math.Abs(float64(target.TargetUpperTick - input.Position.UpperTick))
	if diffLower < float64(cfg.MinSpreadTicks) && diffUpper < float64(cfg.MinSpreadTicks) {
		return nil, nil
	}

	// Engine now returns ticks already adjusted for decimals
	// We only need to align to tick spacing
	rawLower := target.TargetLowerTick
	rawUpper := target.TargetUpperTick

	spacing := cfg.TickSpacing
	if spacing <= 0 {
		spacing = TickSpacingForFee(cfg.Fee)
	}
	if spacing <= 0 {
		spacing = 10
	}
	rawLower = (rawLower / spacing) * spacing
	rawUpper = (rawUpper / spacing) * spacing
	if rawLower == rawUpper {
		rawUpper += spacing
	}

	meta := map[string]string{
		"token0":           cfg.Token0Address,
		"token1":           cfg.Token1Address,
		"amount0":          cfg.Amount0Desired,
		"amount1":          cfg.Amount1Desired,
		"position_manager": cfg.PositionManager,
		"fee":              strconv.Itoa(cfg.Fee),
		"lower_tick":       strconv.FormatInt(rawLower, 10),
		"upper_tick":       strconv.FormatInt(rawUpper, 10),
		"cex_price":        fmt.Sprintf("%.4f", input.CexPrice),
		"dex_price":        fmt.Sprintf("%.4f", input.DexPrice),
		"max_cap_pct":      fmt.Sprintf("%.4f", cfg.MaxCapPct),
	}

	if v := estimateNotionalUSD(cfg, input); v > 0 {
		meta["notional_usd"] = fmt.Sprintf("%.2f", v)
	}

	targetPct := cfg.TargetNotionalPct
	if targetPct <= 0 && cfg.RiskMode != "frozen" {
		targetPct = 0.05
	}
	if targetPct <= 0 {
		return nil, nil
	}

	intent := Intent{
		ID:                fmt.Sprintf("intent-%d", time.Now().UnixNano()),
		Type:              IntentRebalance,
		PoolID:            cfg.PoolID,
		ChainID:           cfg.ChainID,
		Urgency:           5,
		Deadline:          time.Now().Add(5 * time.Minute),
		ExpectedPnL:       0,
		TargetNotionalPct: targetPct,
		StrategyVersion:   cfg.StrategyVersion,
		RiskMode:          cfg.RiskMode,
		Metadata:          meta,
	}
	return []Intent{intent}, nil
}

func estimateNotionalUSD(cfg BasicStrategyConfig, input engine.EngineInput) float64 {
	token0 := parseAmount(cfg.Amount0Desired, cfg.Token0Decimals)
	token1 := parseAmount(cfg.Amount1Desired, cfg.Token1Decimals)
	price := input.CexPrice
	if price <= 0 {
		price = input.DexPrice
	}
	if price <= 0 {
		return 0
	}
	return token0 + token1*price
}

func parseAmount(raw string, decimals int) float64 {
	if raw == "" || decimals < 0 {
		return 0
	}
	val, ok := new(big.Float).SetString(raw)
	if !ok {
		return 0
	}
	scale := new(big.Float).SetFloat64(math.Pow10(decimals))
	val.Quo(val, scale)
	f, _ := val.Float64()
	return f
}

func TickSpacingForFee(fee int) int64 {
	switch fee {
	case 100:
		return 1
	case 500:
		return 10
	case 3000:
		return 60
	case 10000:
		return 200
	default:
		return 10
	}
}
