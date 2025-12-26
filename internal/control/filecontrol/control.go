package filecontrol

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

type ControlState struct {
	DesiredState string `json:"desired_state"`
	ForceDryRun  bool   `json:"force_dry_run"`
	RiskMode     string `json:"risk_mode"`
	Reason       string `json:"reason,omitempty"`
}

func Default() ControlState {
	return ControlState{DesiredState: "RUNNING"}
}

type Loader struct {
	path string

	lastCheckAt time.Time
	lastModTime time.Time
	lastSize    int64
	lastExists  bool
	lastState   ControlState
}

func NewLoader(path string) *Loader {
	return &Loader{
		path:      path,
		lastState: Default(),
	}
}

func (l *Loader) LoadIfChanged(now time.Time) (state ControlState, changed bool, err error) {
	if l == nil {
		return Default(), false, nil
	}
	if !l.lastCheckAt.IsZero() && now.Sub(l.lastCheckAt) < time.Second {
		return l.lastState, false, nil
	}
	l.lastCheckAt = now

	fi, statErr := os.Stat(l.path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			// Missing control file means default state.
			state = Default()
			if l.lastExists {
				l.lastExists = false
				l.lastModTime = time.Time{}
				l.lastSize = 0
				l.lastState = state
				return state, true, nil
			}
			l.lastState = state
			return state, false, nil
		}
		return l.lastState, false, statErr
	}

	mod := fi.ModTime()
	size := fi.Size()
	if l.lastExists && mod.Equal(l.lastModTime) && size == l.lastSize {
		return l.lastState, false, nil
	}

	b, readErr := os.ReadFile(l.path)
	if readErr != nil {
		return l.lastState, false, readErr
	}

	var next ControlState
	if err := json.Unmarshal(b, &next); err != nil {
		return l.lastState, false, err
	}
	next.DesiredState = strings.TrimSpace(next.DesiredState)
	if next.DesiredState == "" {
		next.DesiredState = "RUNNING"
	}
	next.RiskMode = strings.TrimSpace(next.RiskMode)
	next.Reason = strings.TrimSpace(next.Reason)

	changed = !controlEqual(l.lastState, next) || !l.lastExists
	l.lastExists = true
	l.lastModTime = mod
	l.lastSize = size
	l.lastState = next
	return next, changed, nil
}

func controlEqual(a, b ControlState) bool {
	return a.DesiredState == b.DesiredState && a.ForceDryRun == b.ForceDryRun && a.RiskMode == b.RiskMode && a.Reason == b.Reason
}
