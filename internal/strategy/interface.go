package strategy

import (
	"context"

	"phoenix-v3/internal/contracts"
)

type (
	IntentType = contracts.IntentType
	Intent     = contracts.Intent
)

type Strategy interface {
	// Evaluate decides whether to生成 Intent
	Evaluate(ctx context.Context, input contracts.EngineInput) ([]contracts.Intent, error)
}
