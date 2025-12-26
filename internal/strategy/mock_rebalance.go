package strategy

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

type MockRebalanceInput struct {
	AggPrice      float64
	DivergencePct float64
	RiskMode      string
	RiskReason    string
	StaleAgeMs    int64
}

type MockRebalanceStrategy struct {
	mu sync.Mutex

	movePct float64
	lastPx  float64
}

func NewMockRebalanceStrategyFromEnv() *MockRebalanceStrategy {
	// Default: 0.2% move triggers a mock intent.
	movePct := 0.002
	if v := strings.TrimSpace(os.Getenv("PHOENIX_MOCK_MOVE_PCT")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			movePct = f
		}
	}
	return &MockRebalanceStrategy{movePct: movePct}
}

// EvaluateMock emits a mock intent when price moves enough.
// It never blocks on risk itself: the caller must apply gate checks first.
func (s *MockRebalanceStrategy) EvaluateMock(in MockRebalanceInput) (action string, reason string, intents []contracts.Intent) {
	if in.AggPrice <= 0 {
		return "noop", "invalid_price", nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastPx <= 0 {
		s.lastPx = in.AggPrice
		return "noop", "init", nil
	}

	// pct move relative to last observed price
	pct := math.Abs(in.AggPrice-s.lastPx) / s.lastPx
	if pct < s.movePct {
		return "noop", "below_threshold", nil
	}

	s.lastPx = in.AggPrice

	intent := contracts.Intent{
		ID:              fmt.Sprintf("intent-%d", time.Now().UnixNano()),
		Type:            contracts.IntentMockRebalance,
		PoolID:          "mock",
		ChainID:         0,
		Urgency:         1,
		Deadline:        time.Now().Add(5 * time.Minute),
		ExpectedPnL:     0,
		StrategyVersion: "mock-rebalance-v1",
		RiskMode:        strings.TrimSpace(in.RiskMode),
		Metadata: map[string]string{
			"reason":         "price_move",
			"agg_price":      fmt.Sprintf("%.8f", in.AggPrice),
			"divergence_pct": fmt.Sprintf("%.8f", in.DivergencePct),
			"stale_age_ms":   fmt.Sprintf("%d", in.StaleAgeMs),
			"risk_reason":    strings.TrimSpace(in.RiskReason),
		},
	}
	return "mock_rebalance", "price_move", []contracts.Intent{intent}
}
