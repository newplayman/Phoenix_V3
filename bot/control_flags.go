package bot

import "sync/atomic"

// ControlFlags holds in-memory switches for orchestration behavior.
// It is owned by cmd/bot wiring and passed to controllers/loops; it is not global state.
type ControlFlags struct {
	paused            atomic.Bool
	cleanupInProgress atomic.Bool
}

func (f *ControlFlags) SetPaused(v bool) {
	if f == nil {
		return
	}
	f.paused.Store(v)
}

func (f *ControlFlags) Paused() bool {
	if f == nil {
		return false
	}
	return f.paused.Load()
}

func (f *ControlFlags) CleanupInProgress() bool {
	if f == nil {
		return false
	}
	return f.cleanupInProgress.Load()
}

func (f *ControlFlags) TryStartCleanup() bool {
	if f == nil {
		return false
	}
	return f.cleanupInProgress.CompareAndSwap(false, true)
}

func (f *ControlFlags) EndCleanup() {
	if f == nil {
		return
	}
	f.cleanupInProgress.Store(false)
}
