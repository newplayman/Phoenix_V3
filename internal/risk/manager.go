package risk

import (
	"errors"
	"sync"
	"time"
)

type RiskMode string

const (
	ModeNormal  RiskMode = "normal"
	ModeCaution RiskMode = "caution"
	ModeFrozen  RiskMode = "frozen"
)

type Manager struct {
	mu          sync.Mutex
	CurrentMode RiskMode

	DailyGasUsed float64
	MaxDailyGas  float64

	ConsecutiveFails int
	MaxFails         int

	Drawdown    float64
	MaxDrawdown float64 // e.g. 0.10 for 10%

	lastIntentAt time.Time
}

func NewManager(maxGas float64, maxFails int, maxDrawdown float64) *Manager {
	return &Manager{
		CurrentMode: ModeNormal,
		MaxDailyGas: maxGas,
		MaxFails:    maxFails,
		MaxDrawdown: maxDrawdown,
	}
}

// CanProceed checks if a new action is allowed
func (m *Manager) CanProceed() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CurrentMode == ModeFrozen {
		return errors.New("risk manager: system is FROZEN")
	}

	if m.DailyGasUsed >= m.MaxDailyGas {
		m.CurrentMode = ModeCaution
		return errors.New("risk manager: daily gas limit exceeded")
	}

	if m.ConsecutiveFails >= m.MaxFails {
		m.CurrentMode = ModeFrozen
		return errors.New("risk manager: too many consecutive failures")
	}

	if m.Drawdown >= m.MaxDrawdown {
		m.CurrentMode = ModeFrozen
		return errors.New("risk manager: maximum drawdown exceeded")
	}

	return nil
}

// RecordGas updates gas usage
func (m *Manager) RecordGas(cost float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DailyGasUsed += cost
}

// RecordFailure increments failure count
func (m *Manager) RecordFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ConsecutiveFails++
	if m.ConsecutiveFails >= m.MaxFails {
		m.CurrentMode = ModeFrozen
	}
}

// RecordSuccess resets failure count
func (m *Manager) RecordSuccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ConsecutiveFails = 0
	m.lastIntentAt = time.Now()
}

func (m *Manager) UpdateLimits(maxGas float64, maxFails int, maxDrawdown float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxGas > 0 {
		m.MaxDailyGas = maxGas
	}
	if maxFails > 0 {
		m.MaxFails = maxFails
	}
	if maxDrawdown > 0 && maxDrawdown < 1 {
		m.MaxDrawdown = maxDrawdown
	}
}

func (m *Manager) ShouldThrottle(minInterval time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if minInterval <= 0 {
		return false
	}
	return time.Since(m.lastIntentAt) < minInterval
}
