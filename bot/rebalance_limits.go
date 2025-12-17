package bot

import (
	"strings"
	"sync"
	"time"
)

// PerPoolDailyLimiter caps actions per pool per UTC day.
// Zero value is ready to use.
type PerPoolDailyLimiter struct {
	mu     sync.Mutex
	dayKey string
	counts map[string]int
}

func (l *PerPoolDailyLimiter) Allow(poolID string, limit int) bool {
	return l.AllowAt(poolID, limit, time.Now().UTC())
}

func (l *PerPoolDailyLimiter) AllowAt(poolID string, limit int, now time.Time) bool {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || limit <= 0 {
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	today := now.UTC().Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil || l.dayKey != today {
		l.dayKey = today
		l.counts = map[string]int{}
	}
	if l.counts[poolID] >= limit {
		return false
	}
	l.counts[poolID]++
	return true
}

// LastActionTracker stores last timestamp per pool.
// Zero value is ready to use.
type LastActionTracker struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *LastActionTracker) Set(poolID string, at time.Time) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	t.last[poolID] = at
}

func (t *LastActionTracker) Get(poolID string) (time.Time, bool) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return time.Time{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		return time.Time{}, false
	}
	at, ok := t.last[poolID]
	return at, ok
}
