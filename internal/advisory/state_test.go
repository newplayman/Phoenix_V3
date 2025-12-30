package advisory

import (
"os"
"path/filepath"
"testing"
"time"
)

func TestAdvisoryStateHysteresis(t *testing.T) {
// Create temp directory for state file
tmpDir := t.TempDir()

// Test configuration with default hysteresis params
cfg := AdvisoryConfig{
:    3,
M:  5,
:    2,
M:  4,
}

t.Run("Case1_SingleSpikeNoUpgrade", func(t *testing.T) {
NewAdvisoryStateManager(tmpDir)
:= mgr.Load(); err != nil {
%v", err)
ow := time.Unix(1700000000, 0)
itial state should be NO_CHANGE
mgr.GetState()
tMode != SuggestionNoChange {
itial mode NO_CHANGE, got %s", state.CurrentMode)
gle HALT trigger
 := mgr.UpdateState(SuggestionHalt, cfg, now)
still be NO_CHANGE (need 3 consecutive)
!= SuggestionNoChange {
mode NO_CHANGE after 1 HALT, got %s", stableMode)
mgr.GetState()
secutiveTriggerCount[SuggestionHalt] != 1 {
t=1, got %d", state.ConsecutiveTriggerCount[SuggestionHalt])
PASSED: Single spike did not upgrade. Reason: %s", reason)
})

t.Run("Case2_ConsecutiveTriggersUpgrade", func(t *testing.T) {
NewAdvisoryStateManager(tmpDir)
:= mgr.Load(); err != nil {
%v", err)
ow := time.Unix(1700000000, 0)
HALT 3 times consecutively
string
:= 1; i <= 3; i++ {
ow = now.Add(time.Minute)
= mgr.UpdateState(SuggestionHalt, cfg, now)
< 3 {
!= SuggestionNoChange {
 %d: Expected NO_CHANGE, got %s", i, stableMode)
{
!= SuggestionHalt {
 %d: Expected HALT, got %s", i, stableMode)
:= mgr.GetState()
tMode != SuggestionHalt {
t mode HALT, got %s", state.CurrentMode)
PASSED: Consecutive triggers upgraded to HALT")
})

t.Run("Case3_DowngradeHarder", func(t *testing.T) {
NewAdvisoryStateManager(tmpDir)
:= mgr.Load(); err != nil {
%v", err)
ow := time.Unix(1700000000, 0)
get to HALT state
:= 1; i <= 3; i++ {
ow = now.Add(time.Minute)
Halt, cfg, now)
mgr.GetState()
tMode != SuggestionHalt {
not in HALT state")
ow trigger NO_CHANGE 4 times
string
:= 1; i <= 4; i++ {
ow = now.Add(time.Minute)
= mgr.UpdateState(SuggestionNoChange, cfg, now)
still be HALT (need 5 to downgrade)
!= SuggestionHalt {
 %d: Expected still HALT, got %s", i, stableMode)
time should allow downgrade
ow = now.Add(time.Minute)
 := mgr.UpdateState(SuggestionNoChange, cfg, now)
!= SuggestionNoChange {
NO_CHANGE: Expected downgrade to NO_CHANGE, got %s", stableMode)
PASSED: Downgrade required 5 consecutive. Reason: %s", reason)
})

t.Run("StatePersistence", func(t *testing.T) {
NewAdvisoryStateManager(tmpDir)
:= mgr.Load(); err != nil {
%v", err)
ow := time.Unix(1700000000, 0)
HALT twice
Halt, cfg, now)
Halt, cfg, now.Add(time.Minute))
state
:= mgr.Save(); err != nil {
%v", err)
 file exists
filepath.Join(tmpDir, "risk_advisory_state.json")
err := os.Stat(statePath); os.IsNotExist(err) {
not created at %s", statePath)
in new manager
NewAdvisoryStateManager(tmpDir)
:= mgr2.Load(); err != nil {
%v", err)
mgr2.GetState()
secutiveTriggerCount[SuggestionHalt] != 2 {
t=2 after reload, got %d", state.ConsecutiveTriggerCount[SuggestionHalt])
persistence PASSED: State file created and reloaded correctly")
})
}
