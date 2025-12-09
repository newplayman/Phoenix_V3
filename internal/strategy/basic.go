package strategy

import (
	"context"
	"fmt"
	"math"
	"time"

	"phoenix-v3/internal/engine"
)

type BasicStrategy struct {
	eng engine.Engine
}

func NewBasicStrategy() *BasicStrategy {
	return &BasicStrategy{
		eng: engine.NewStandardASMMEngine(),
	}
}

// Evaluate checks if we need to generate an Intent
// valid input: engine.EngineInput
func (s *BasicStrategy) Evaluate(ctx context.Context, input interface{}) ([]Intent, error) {
	in, ok := input.(engine.EngineInput)
	if !ok {
		return nil, fmt.Errorf("invalid input type")
	}

	// 1. Run Engine Calculation
	target, err := s.eng.Calculate(in)
	if err != nil {
		return nil, err
	}

	var intents []Intent

	// 2. Check Logic: Is the new range significantly different?
	// Heuristic: If target bounds differ from current by > 5%, move.
	// For Phase 2 demo, let's just generate an intent if ticks don't match exactly
	// (usually you'd have a buffer).

	currentLower := in.Position.LowerTick
	currentUpper := in.Position.UpperTick

	diffLower := math.Abs(float64(target.TargetLowerTick - currentLower))
	diffUpper := math.Abs(float64(target.TargetUpperTick - currentUpper))

	// Threshold: e.g. 100 ticks
	if diffLower > 100 || diffUpper > 100 {
		intent := Intent{
			ID:          fmt.Sprintf("intent-%d", time.Now().UnixNano()),
			Type:        IntentRebalance,
			PoolID:      "eth-usdc-05", // Context usually provides this
			Urgency:     5,
			Deadline:    time.Now().Add(5 * time.Minute),
			ExpectedPnL: 0, // Placeholder
		}
		intents = append(intents, intent)
	}

	return intents, nil
}
