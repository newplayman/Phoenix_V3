package strategy

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
	"phoenix-v3/internal/engine"
)

type BasicStrategyConfig struct {
	PoolID          string
	ChainID         int64
	StrategyVersion string
	RiskMode        string
	MinSpreadTicks  int64
	MaxGasUSD       float64
	Token0Address   string
	Token1Address   string
	Fee             int
	PositionManager string
	Amount0Desired  string
	Amount1Desired  string
}

type BasicStrategy struct {
	eng engine.Engine

	mu  sync.RWMutex
	cfg BasicStrategyConfig
}

func NewBasicStrategy(cfg BasicStrategyConfig) *BasicStrategy {
	if cfg.RiskMode == "" {
		cfg.RiskMode = "normal"
	}
	return &BasicStrategy{
		eng: engine.NewStandardASMMEngine(),
		cfg: cfg,
	}
}

// Evaluate checks if我们需要生成 Intent（使用 contracts.EngineInput）
func (s *BasicStrategy) Evaluate(ctx context.Context, input contracts.EngineInput) ([]contracts.Intent, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	// Validate input prices - avoid rebalancing with invalid/zero prices
	if input.CexPrice <= 0.001 {
		return []contracts.Intent{}, nil // No intent, price is invalid
	}

	// 1. Run Engine Calculation
	target, err := s.eng.Calculate(input)
	if err != nil {
		return nil, err
	}

	var intents []contracts.Intent

	currentLower := input.Position.LowerTick
	currentUpper := input.Position.UpperTick

	diffLower := math.Abs(float64(target.TargetLowerTick - currentLower))
	diffUpper := math.Abs(float64(target.TargetUpperTick - currentUpper))

	minSpread := float64(cfg.MinSpreadTicks)
	if minSpread <= 0 {
		minSpread = 100
	}

	if diffLower > minSpread || diffUpper > minSpread {
		amount0 := cfg.Amount0Desired
		if amount0 == "" {
			amount0 = "1000000000000000"
		}
		amount1 := cfg.Amount1Desired
		if amount1 == "" {
			amount1 = "0"
		}
		metadata := map[string]string{
			"engine":           "basic_asmm",
			"token0":           cfg.Token0Address,
			"token1":           cfg.Token1Address,
			"fee":              strconv.Itoa(cfg.Fee),
			"tick_lower":       fmt.Sprintf("%d", target.TargetLowerTick-50),
			"tick_upper":       fmt.Sprintf("%d", target.TargetUpperTick+50),
			"amount0":          amount0,
			"amount1":          amount1,
			"deadline":         fmt.Sprintf("%d", time.Now().Add(10*time.Minute).Unix()),
			"position_manager": cfg.PositionManager,
		}
		intent := contracts.Intent{
			ID:              fmt.Sprintf("intent-%d", time.Now().UnixNano()),
			Type:            contracts.IntentRebalance,
			PoolID:          cfg.PoolID,
			ChainID:         cfg.ChainID,
			Urgency:         5,
			Deadline:        time.Now().Add(5 * time.Minute),
			ExpectedPnL:     0,
			StrategyVersion: cfg.StrategyVersion,
			RiskMode:        cfg.RiskMode,
			Metadata:        metadata,
		}
		intents = append(intents, intent)
	}

	return intents, nil
}

func (s *BasicStrategy) UpdateConfig(cfg BasicStrategyConfig) {
	if cfg.RiskMode == "" {
		cfg.RiskMode = "normal"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}
