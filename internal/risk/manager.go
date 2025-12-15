package risk

import (
	"errors"
	"sync"
	"time"

	"phoenix-v3/internal/storage"
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

	// Swap Limits
	DailySwapVolUSD float64
	MaxDailySwapVol float64
	DailySwapCount  int
	MaxDailySwaps   int

	lastIntent time.Time
}

func ParseMode(mode string) (RiskMode, error) {
	switch RiskMode(mode) {
	case ModeNormal, ModeCaution, ModeFrozen:
		return RiskMode(mode), nil
	default:
		return "", errors.New("unknown risk mode")
	}
}

type Snapshot struct {
	Mode            RiskMode `json:"mode"`
	DailyGasUsed    float64  `json:"dailyGasUsed"`
	MaxDailyGas     float64  `json:"maxDailyGas"`
	DailySwapVolUSD float64  `json:"dailySwapVolUsd"`
	MaxDailySwapVol float64  `json:"maxDailySwapVol"`
	DailySwapCount  int      `json:"dailySwapCount"`
	MaxDailySwaps   int      `json:"maxDailySwaps"`
	ConsecutiveFails int     `json:"consecutiveFails"`
	MaxFails        int      `json:"maxFails"`
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Snapshot{
		Mode:             m.CurrentMode,
		DailyGasUsed:     m.DailyGasUsed,
		MaxDailyGas:      m.MaxDailyGas,
		DailySwapVolUSD:  m.DailySwapVolUSD,
		MaxDailySwapVol:  m.MaxDailySwapVol,
		DailySwapCount:   m.DailySwapCount,
		MaxDailySwaps:    m.MaxDailySwaps,
		ConsecutiveFails: m.ConsecutiveFails,
		MaxFails:         m.MaxFails,
	}
}

// UpdateDrawdownFromTrades computes drawdown from historical trade PnL.
// It treats PnL as realized USD changes in chronological order.
func (m *Manager) UpdateDrawdownFromTrades(trades []storage.TradeRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(trades) == 0 {
		m.Drawdown = 0
		return
	}
	equity := 0.0
	peak := 0.0
	maxDD := 0.0
	for _, t := range trades {
		equity += t.PnL
		if equity > peak {
			peak = equity
		}
		if peak > 0 {
			d := (peak - equity) / peak
			if d > maxDD {
				maxDD = d
			}
		}
	}
	m.Drawdown = maxDD
	if m.Drawdown >= m.MaxDrawdown {
		m.CurrentMode = ModeFrozen
	}
}

func NewManager(maxGas float64, maxFails int, maxDrawdown float64) *Manager {
	return &Manager{
		CurrentMode: ModeNormal,
		MaxDailyGas: maxGas,
		MaxFails:    maxFails,
		MaxDrawdown: maxDrawdown,
		// Defaults
		MaxDailySwapVol: 100000,
		MaxDailySwaps:   50,
	}
}

// CanSwap checks if a swap is allowed
func (m *Manager) CanSwap(amountUSD float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CurrentMode == ModeFrozen {
		return errors.New("risk manager: system frozen")
	}
	if m.DailySwapCount >= m.MaxDailySwaps {
		return errors.New("risk manager: max daily swap count exceeded")
	}
	if m.DailySwapVolUSD+amountUSD > m.MaxDailySwapVol {
		return errors.New("risk manager: max daily swap volume exceeded")
	}
	return nil
}

// RecordSwap updates swap metrics
func (m *Manager) RecordSwap(amountUSD float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DailySwapVolUSD += amountUSD
	m.DailySwapCount++
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
	m.CurrentMode = ModeNormal
}

// ShouldThrottle enforces a minimum interval between intents
func (m *Manager) ShouldThrottle(minInterval time.Duration) bool {
	if minInterval <= 0 {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if !m.lastIntent.IsZero() && now.Sub(m.lastIntent) < minInterval {
		return true
	}
	m.lastIntent = now
	return false
}

// UpdateLimits refreshes runtime thresholds when config hot reloads
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

// UpdateSwapLimits allows updating swap limits
func (m *Manager) UpdateSwapLimits(maxVol float64, maxCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxVol > 0 {
		m.MaxDailySwapVol = maxVol
	}
	if maxCount > 0 {
		m.MaxDailySwaps = maxCount
	}
}

// SetMode force-switches risk mode (manual control).
func (m *Manager) SetMode(mode RiskMode) error {
	if mode != ModeNormal && mode != ModeCaution && mode != ModeFrozen {
		return errors.New("risk manager: invalid mode")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CurrentMode = mode
	return nil
}
