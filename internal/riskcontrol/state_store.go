package riskcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"phoenix-v3/internal/contracts"
)

type PersistedRiskState struct {
	UpdatedAtMS int64 `json:"updated_at_ms"`

	LastIntentTsMSByKey       map[string]int64  `json:"last_intent_ts_ms_by_key"`
	ConsecutiveFailCountByKey map[string]int    `json:"consecutive_fail_count_by_key"`
	CooldownUntilTsMSByKey    map[string]int64  `json:"cooldown_until_ts_ms_by_key"`
	LastRejectReasonByKey     map[string]string `json:"last_reject_reason_by_key"`
}

type RiskStateStore struct {
	mu   sync.Mutex
	path string

	lastModTime time.Time
	lastSize    int64
	lastExists  bool

	state PersistedRiskState
}

func NewRiskStateStore(path string) *RiskStateStore {
	s := &RiskStateStore{
		path: path,
	}
	s.state = defaultPersistedRiskState()
	return s
}

func defaultPersistedRiskState() PersistedRiskState {
	return PersistedRiskState{
		UpdatedAtMS:               0,
		LastIntentTsMSByKey:       map[string]int64{},
		ConsecutiveFailCountByKey: map[string]int{},
		CooldownUntilTsMSByKey:    map[string]int64{},
		LastRejectReasonByKey:     map[string]string{},
	}
}

func (s *RiskStateStore) LoadIfChanged(now time.Time) (PersistedRiskState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadIfChangedLocked(now)
}

func (s *RiskStateStore) loadIfChangedLocked(now time.Time) (PersistedRiskState, bool, error) {
	if s == nil {
		return defaultPersistedRiskState(), false, nil
	}

	fi, statErr := os.Stat(s.path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			if s.lastExists {
				s.lastExists = false
				s.lastModTime = time.Time{}
				s.lastSize = 0
				s.state = defaultPersistedRiskState()
				return s.state, true, nil
			}
			return s.state, false, nil
		}
		return s.state, false, statErr
	}

	mod := fi.ModTime()
	size := fi.Size()
	if s.lastExists && mod.Equal(s.lastModTime) && size == s.lastSize {
		return s.state, false, nil
	}

	b, readErr := os.ReadFile(s.path)
	if readErr != nil {
		return s.state, false, readErr
	}

	var next PersistedRiskState
	if err := json.Unmarshal(b, &next); err != nil {
		return s.state, false, err
	}
	normalizePersistedRiskState(&next)

	changed := !s.lastExists || !persistedRiskStateEqual(s.state, next)
	s.lastExists = true
	s.lastModTime = mod
	s.lastSize = size
	s.state = next
	return s.state, changed, nil
}

func (s *RiskStateStore) Save(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(now)
}

func (s *RiskStateStore) saveLocked(now time.Time) error {
	if s == nil {
		return nil
	}
	normalizePersistedRiskState(&s.state)
	s.state.UpdatedAtMS = now.UnixMilli()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if fi, err := os.Stat(s.path); err == nil {
		s.lastExists = true
		s.lastModTime = fi.ModTime()
		s.lastSize = fi.Size()
	}
	return nil
}

func normalizePersistedRiskState(st *PersistedRiskState) {
	if st.LastIntentTsMSByKey == nil {
		st.LastIntentTsMSByKey = map[string]int64{}
	}
	if st.ConsecutiveFailCountByKey == nil {
		st.ConsecutiveFailCountByKey = map[string]int{}
	}
	if st.CooldownUntilTsMSByKey == nil {
		st.CooldownUntilTsMSByKey = map[string]int64{}
	}
	if st.LastRejectReasonByKey == nil {
		st.LastRejectReasonByKey = map[string]string{}
	}
}

func persistedRiskStateEqual(a, b PersistedRiskState) bool {
	// Good enough for reload detection; if it changes, we re-cache anyway.
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func CooldownKey(target string, intent contracts.Intent) string {
	t := strings.TrimSpace(target)
	if t == "" {
		t = "phoenix"
	}
	parts := []string{
		t,
		string(intent.Type),
		fmt.Sprintf("chain=%d", intent.ChainID),
	}
	if strings.TrimSpace(intent.PoolID) != "" {
		parts = append(parts, "pool="+strings.TrimSpace(intent.PoolID))
	}
	return strings.Join(parts, "|")
}

type CooldownEntry struct {
	Key             string `json:"key"`
	CooldownUntilMS int64  `json:"cooldown_until_ts_ms"`
}

func (s *RiskStateStore) SnapshotTopCooldowns(now time.Time, n int) []CooldownEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)

	type kv struct {
		key   string
		until int64
	}
	items := make([]kv, 0, len(s.state.CooldownUntilTsMSByKey))
	for k, until := range s.state.CooldownUntilTsMSByKey {
		if until <= 0 {
			continue
		}
		items = append(items, kv{key: k, until: until})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].until > items[j].until })
	if n <= 0 || n > len(items) {
		n = len(items)
	}
	out := make([]CooldownEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, CooldownEntry{Key: items[i].key, CooldownUntilMS: items[i].until})
	}
	return out
}

func (s *RiskStateStore) GetCooldownUntilMS(now time.Time, key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	return s.state.CooldownUntilTsMSByKey[key]
}

func (s *RiskStateStore) GetConsecutiveFails(now time.Time, key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	return s.state.ConsecutiveFailCountByKey[key]
}

func (s *RiskStateStore) GetLastIntentMS(now time.Time, key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	return s.state.LastIntentTsMSByKey[key]
}

func (s *RiskStateStore) SetLastIntentMS(now time.Time, key string, tsMS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	s.state.LastIntentTsMSByKey[key] = tsMS
	return s.saveLocked(now)
}

func (s *RiskStateStore) SetLastRejectReason(now time.Time, key, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	s.state.LastRejectReasonByKey[key] = reason
	return s.saveLocked(now)
}

func (s *RiskStateStore) RecordSuccess(now time.Time, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)
	s.state.ConsecutiveFailCountByKey[key] = 0
	return s.saveLocked(now)
}

func (s *RiskStateStore) RecordFailure(now time.Time, key string, failureThreshold int, cooldownFor time.Duration) (cooldownUntilMS int64, failCount int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _ = s.loadIfChangedLocked(now)

	failCount = s.state.ConsecutiveFailCountByKey[key] + 1
	s.state.ConsecutiveFailCountByKey[key] = failCount
	if failureThreshold > 0 && failCount >= failureThreshold && cooldownFor > 0 {
		until := now.Add(cooldownFor).UnixMilli()
		s.state.CooldownUntilTsMSByKey[key] = until
		cooldownUntilMS = until
	}
	err = s.saveLocked(now)
	return cooldownUntilMS, failCount, err
}
