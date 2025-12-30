package advisory

import (
"encoding/json"
"fmt"
"os"
"path/filepath"
"sync"
"time"
)

// AdvisoryState tracks the current advisory mode and hysteresis counters
type AdvisoryState struct {
UpdatedAtMS            int64            `json:"updated_at_ms"`
CurrentMode            string           `json:"current_mode"`
StateSinceMs           int64            `json:"state_since_ts_ms"`
ConsecutiveTriggerCount map[string]int  `json:"consecutive_trigger_count"`
}

// AdvisoryStateManager manages the persistent advisory state
type AdvisoryStateManager struct {
mu       sync.Mutex
filePath string
state    AdvisoryState
}

// NewAdvisoryStateManager creates a new state manager
func NewAdvisoryStateManager(stateDir string) *AdvisoryStateManager {
if stateDir == "" {
"var"
}
return &AdvisoryStateManager{
(stateDir, "risk_advisory_state.json"),
State{
tMode: SuggestionNoChange,
secutiveTriggerCount: map[string]int{
Halt:     0,
Safe:     0,
NoChange: 0,
Load loads the state from disk, or initializes if missing
func (m *AdvisoryStateManager) Load() error {
m.mu.Lock()
defer m.mu.Unlock()

data, err := os.ReadFile(m.filePath)
if err != nil {
otExist(err) {
itialize new state
ow := time.Now().UnixMilli()
AdvisoryState{
now,
tMode:  SuggestionNoChange,
ceMs: now,
secutiveTriggerCount: map[string]int{
Halt:     0,
Safe:     0,
NoChange: 0,
 nil
 fmt.Errorf("read state: %w", err)
}

if err := json.Unmarshal(data, &m.state); err != nil {
 fmt.Errorf("parse state: %w", err)
}

return nil
}

// Save atomically saves the state to disk
func (m *AdvisoryStateManager) Save() error {
m.mu.Lock()
defer m.mu.Unlock()

data, err := json.MarshalIndent(m.state, "", "  ")
if err != nil {
 fmt.Errorf("marshal state: %w", err)
}

// Atomic write: tmp + rename
tmpPath := m.filePath + ".tmp"
if err := os.WriteFile(tmpPath, data, 0644); err != nil {
 fmt.Errorf("write tmp state: %w", err)
}

if err := os.Rename(tmpPath, m.filePath); err != nil {
 fmt.Errorf("rename state: %w", err)
}

return nil
}

// UpdateState applies hysteresis logic and updates the state
func (m *AdvisoryStateManager) UpdateState(instantaneousMode string, config AdvisoryConfig, now time.Time) (outputMode string, reason string) {
m.mu.Lock()
defer m.mu.Unlock()

nowMs := now.UnixMilli()
m.state.UpdatedAtMS = nowMs

// Reset all counters except the instantaneous mode
for mode := range m.state.ConsecutiveTriggerCount {
== instantaneousMode {
secutiveTriggerCount[mode]++
{
secutiveTriggerCount[mode] = 0
tMode := m.state.CurrentMode
haltCount := m.state.ConsecutiveTriggerCount[SuggestionHalt]
safeCount := m.state.ConsecutiveTriggerCount[SuggestionSafe]
noChangeCount := m.state.ConsecutiveTriggerCount[SuggestionNoChange]

// Hysteresis logic
switched := false
oldMode := currentMode

switch instantaneousMode {
case SuggestionHalt:
tMode != SuggestionHalt && haltCount >= config.HaltUpN {
tMode = SuggestionHalt
ceMs = nowMs
true
Safe:
HALT: need HALT_DOWN_M consecutive non-HALT to allow downgrade
tMode == SuggestionHalt {
onHaltCount := safeCount + noChangeCount
onHaltCount >= config.HaltDownM {
tMode = SuggestionSafe
ceMs = nowMs
true
if currentMode != SuggestionSafe && safeCount >= config.SafeUpN {
tMode = SuggestionSafe
ceMs = nowMs
true
NoChange:
HALT: need HALT_DOWN_M consecutive non-HALT
tMode == SuggestionHalt {
onHaltCount := safeCount + noChangeCount
onHaltCount >= config.HaltDownM {
tMode = SuggestionNoChange
ceMs = nowMs
true
if currentMode == SuggestionSafe && noChangeCount >= config.SafeDownM {
tMode = SuggestionNoChange
ceMs = nowMs
true
tMode = currentMode

// Generate hysteresis reason
reason = fmt.Sprintf("hysteresis: instantaneous=%s current=%s halt_up_n=%d halt_down_m=%d safe_up_n=%d safe_down_m=%d counts(H=%d,S=%d,N=%d)",
stantaneousMode, currentMode,
fig.HaltUpN, config.HaltDownM,
fig.SafeUpN, config.SafeDownM,
t, safeCount, noChangeCount)

if switched {
 += fmt.Sprintf(" [SWITCHED: %s→%s]", oldMode, currentMode)
}

return currentMode, reason
}

// GetState returns a copy of the current state
func (m *AdvisoryStateManager) GetState() AdvisoryState {
m.mu.Lock()
defer m.mu.Unlock()

// Deep copy
counts := make(map[string]int)
for k, v := range m.state.ConsecutiveTriggerCount {
ts[k] = v
}

return AdvisoryState{
           m.state.UpdatedAtMS,
tMode:             m.state.CurrentMode,
ceMs:            m.state.StateSinceMs,
secutiveTriggerCount: counts,
}
}
