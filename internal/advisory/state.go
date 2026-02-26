package advisory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AdvisoryState tracks the current advisory mode and hysteresis counters.
type AdvisoryState struct {
	UpdatedAtMS             int64          `json:"updated_at_ms"`
	CurrentMode             string         `json:"current_mode"`
	StateSinceMs            int64          `json:"state_since_ts_ms"`
	ConsecutiveTriggerCount map[string]int `json:"consecutive_trigger_count"`
}

// AdvisoryStateManager manages the persistent advisory state.
type AdvisoryStateManager struct {
	mu       sync.Mutex
	filePath string
	state    AdvisoryState
}

// NewAdvisoryStateManager creates a new state manager.
func NewAdvisoryStateManager(stateDir string) *AdvisoryStateManager {
	if stringsTrimSpace(stateDir) == "" {
		stateDir = "var"
	}
	return &AdvisoryStateManager{
		filePath: filepath.Join(stateDir, "risk_advisory_state.json"),
		state: AdvisoryState{
			CurrentMode: SuggestionNoChange,
			ConsecutiveTriggerCount: map[string]int{
				SuggestionHalt:     0,
				SuggestionSafe:     0,
				SuggestionNoChange: 0,
			},
		},
	}
}

// Load loads the state from disk, or initializes if missing.
func (m *AdvisoryStateManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			now := time.Now().UTC().UnixMilli()
			m.state = AdvisoryState{
				UpdatedAtMS:  now,
				CurrentMode:  SuggestionNoChange,
				StateSinceMs: now,
				ConsecutiveTriggerCount: map[string]int{
					SuggestionHalt:     0,
					SuggestionSafe:     0,
					SuggestionNoChange: 0,
				},
			}
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if m.state.ConsecutiveTriggerCount == nil {
		m.state.ConsecutiveTriggerCount = map[string]int{
			SuggestionHalt:     0,
			SuggestionSafe:     0,
			SuggestionNoChange: 0,
		}
	}
	if m.state.CurrentMode == "" {
		m.state.CurrentMode = SuggestionNoChange
	}
	return nil
}

// Save atomically saves the state to disk.
func (m *AdvisoryStateManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(m.filePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := m.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write tmp state: %w", err)
	}
	if err := os.Rename(tmpPath, m.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// UpdateState applies hysteresis logic and updates the state.
func (m *AdvisoryStateManager) UpdateState(instantaneousMode string, config AdvisoryConfig, now time.Time) (outputMode string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	nowMs := now.UTC().UnixMilli()
	m.state.UpdatedAtMS = nowMs

	if m.state.ConsecutiveTriggerCount == nil {
		m.state.ConsecutiveTriggerCount = map[string]int{}
	}
	for _, mode := range []string{SuggestionHalt, SuggestionSafe, SuggestionNoChange} {
		if mode == instantaneousMode {
			m.state.ConsecutiveTriggerCount[mode]++
		} else {
			m.state.ConsecutiveTriggerCount[mode] = 0
		}
	}

	currentMode := m.state.CurrentMode
	haltCount := m.state.ConsecutiveTriggerCount[SuggestionHalt]
	safeCount := m.state.ConsecutiveTriggerCount[SuggestionSafe]
	noChangeCount := m.state.ConsecutiveTriggerCount[SuggestionNoChange]

	oldMode := currentMode
	switched := false

	switch instantaneousMode {
	case SuggestionHalt:
		if currentMode != SuggestionHalt && haltCount >= config.HaltUpN {
			currentMode = SuggestionHalt
			m.state.StateSinceMs = nowMs
			switched = true
		}
	case SuggestionSafe:
		if currentMode == SuggestionHalt {
			nonHaltCount := safeCount + noChangeCount
			if nonHaltCount >= config.HaltDownM {
				currentMode = SuggestionSafe
				m.state.StateSinceMs = nowMs
				switched = true
			}
		} else if currentMode != SuggestionSafe && safeCount >= config.SafeUpN {
			currentMode = SuggestionSafe
			m.state.StateSinceMs = nowMs
			switched = true
		}
	case SuggestionNoChange:
		if currentMode == SuggestionHalt {
			nonHaltCount := safeCount + noChangeCount
			if nonHaltCount >= config.HaltDownM {
				currentMode = SuggestionNoChange
				m.state.StateSinceMs = nowMs
				switched = true
			}
		} else if currentMode == SuggestionSafe && noChangeCount >= config.SafeDownM {
			currentMode = SuggestionNoChange
			m.state.StateSinceMs = nowMs
			switched = true
		}
	}

	m.state.CurrentMode = currentMode

	reason = fmt.Sprintf(
		"hysteresis: instantaneous=%s current=%s halt_up_n=%d halt_down_m=%d safe_up_n=%d safe_down_m=%d counts(H=%d,S=%d,N=%d)",
		instantaneousMode, currentMode,
		config.HaltUpN, config.HaltDownM,
		config.SafeUpN, config.SafeDownM,
		haltCount, safeCount, noChangeCount,
	)
	if switched {
		reason += fmt.Sprintf(" [SWITCHED: %s→%s]", oldMode, currentMode)
	}
	return currentMode, reason
}

// GetState returns a copy of the current state.
func (m *AdvisoryStateManager) GetState() AdvisoryState {
	m.mu.Lock()
	defer m.mu.Unlock()

	counts := make(map[string]int, len(m.state.ConsecutiveTriggerCount))
	for k, v := range m.state.ConsecutiveTriggerCount {
		counts[k] = v
	}
	return AdvisoryState{
		UpdatedAtMS:             m.state.UpdatedAtMS,
		CurrentMode:             m.state.CurrentMode,
		StateSinceMs:            m.state.StateSinceMs,
		ConsecutiveTriggerCount: counts,
	}
}

func stringsTrimSpace(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\n' || s[j-1] == '\r' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
