package feed

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"
)

type PriceAggregator struct {
	cfg PriceAggregatorConfig
	sym SymbolSpec

	mu      sync.RWMutex
	sources map[string]*sourceState

	agg  AggregateState
	risk RiskState

	updates chan PriceUpdate
	events  chan SourceEvent
	out     chan Ticker

	cancel context.CancelFunc
	done   chan struct{}

	lastLoggedAt time.Time
	lastStale    bool
	lastFrozen   bool
	lastMode     string
}

type sourceState struct {
	PriceSourceState
	errTimes []time.Time
}

func NewPriceAggregator(cfg PriceAggregatorConfig) *PriceAggregator {
	if cfg.StaleWindow <= 0 {
		cfg.StaleWindow = 5 * time.Second
	}
	if cfg.FreezeWindow <= 0 {
		cfg.FreezeWindow = 20 * time.Second
	}
	if cfg.LogInterval <= 0 {
		cfg.LogInterval = 7 * time.Second
	}
	if cfg.DivergencePct <= 0 {
		cfg.DivergencePct = 0.003
	}
	if cfg.Symbol == "" {
		cfg.Symbol = "ETH/USDT"
	}

	sym := NormalizeSymbol(cfg.Symbol)

	a := &PriceAggregator{
		cfg:     cfg,
		sym:     sym,
		sources: make(map[string]*sourceState),
		updates: make(chan PriceUpdate, 256),
		events:  make(chan SourceEvent, 256),
		out:     make(chan Ticker, 64),
		done:    make(chan struct{}),
	}
	a.risk = RiskState{Mode: "degraded", Reason: "starting"}
	return a
}

func (a *PriceAggregator) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel

	go NewBinanceBookTickerWS().Run(ctx, a.sym, a.updates, a.events)
	go NewOKXTickersWS().Run(ctx, a.sym, a.updates, a.events)

	go a.loop(ctx)
}

func (a *PriceAggregator) Close() {
	if a.cancel == nil {
		// Start() was never called; make Close() non-blocking.
		select {
		case <-a.done:
		default:
			close(a.out)
			close(a.done)
		}
		return
	}
	a.cancel()
	<-a.done
}

func (a *PriceAggregator) Output() <-chan Ticker {
	return a.out
}

func (a *PriceAggregator) Snapshot() MarketSnapshot {
	now := time.Now()
	a.mu.RLock()
	defer a.mu.RUnlock()

	agg := a.agg
	age := now.Sub(agg.AggUpdatedAt)
	if agg.AggUpdatedAt.IsZero() {
		age = 0
	}
	agg.StaleAgeMs = durationMs(age)
	agg.Stale = agg.AggUpdatedAt.IsZero() || age > a.cfg.StaleWindow

	outSources := make([]PriceSourceState, 0, len(a.sources))
	for _, st := range a.sources {
		outSources = append(outSources, st.PriceSourceState)
	}
	sort.Slice(outSources, func(i, j int) bool { return outSources[i].Name < outSources[j].Name })

	return MarketSnapshot{
		Symbol:    a.sym.Display,
		Aggregate: agg,
		Sources:   outSources,
		Risk:      a.risk,
	}
}

func (a *PriceAggregator) GetRisk() RiskState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.risk
}

func (a *PriceAggregator) GetGate(now time.Time) (blocked bool, reason string, age time.Duration, mode string) {
	a.mu.RLock()
	agg := a.agg
	risk := a.risk
	a.mu.RUnlock()

	if agg.AggUpdatedAt.IsZero() {
		return true, "price_stale", 0, risk.Mode
	}
	age = now.Sub(agg.AggUpdatedAt)
	if age > a.cfg.FreezeWindow {
		return true, "price_frozen", age, "frozen"
	}
	if age > a.cfg.StaleWindow {
		return true, "price_stale", age, risk.Mode
	}
	return false, "", age, risk.Mode
}

func (a *PriceAggregator) loop(ctx context.Context) {
	defer close(a.out)
	defer close(a.done)

	staleTick := time.NewTicker(1 * time.Second)
	defer staleTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case u := <-a.updates:
			a.onPriceUpdate(u)
		case ev := <-a.events:
			a.onSourceEvent(ev)
		case <-staleTick.C:
			a.onTimeTick()
		}
	}
}

func (a *PriceAggregator) onPriceUpdate(u PriceUpdate) {
	if u.Source == "" || u.Price <= 0 || u.ReceivedAt.IsZero() {
		return
	}
	a.mu.Lock()
	st := a.getOrCreateSourceLocked(u.Source)
	st.Connected = true
	st.LastPrice = u.Price
	st.LastUpdateAt = u.ReceivedAt
	if !u.EventTime.IsZero() {
		d := u.ReceivedAt.Sub(u.EventTime)
		if d < 0 {
			d = 0
		}
		st.LatencyMs = durationMs(d)
	}

	updatedAgg := a.recomputeLocked(u.ReceivedAt)
	out := Ticker{
		Symbol:    stringsToLegacyTickerSymbol(a.sym),
		Price:     a.agg.AggPrice,
		Timestamp: a.agg.AggUpdatedAt,
	}
	a.mu.Unlock()

	if updatedAgg && out.Price > 0 && !out.Timestamp.IsZero() {
		select {
		case a.out <- out:
		default:
		}
	}
}

func (a *PriceAggregator) onSourceEvent(ev SourceEvent) {
	if ev.Source == "" {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}

	a.mu.Lock()
	st := a.getOrCreateSourceLocked(ev.Source)
	st.Connected = ev.Connected
	if ev.Err != nil {
		st.LastErr = ev.Err.Error()
		st.LastErrAt = at
		st.errTimes = append(st.errTimes, at)
		st.errTimes = pruneTimes(st.errTimes, at.Add(-1*time.Minute))
		st.Err1m = len(st.errTimes)
	}
	_ = a.recomputeLocked(at)
	a.mu.Unlock()
}

func (a *PriceAggregator) onTimeTick() {
	now := time.Now()
	a.mu.Lock()
	_ = a.recomputeLocked(now)
	a.maybeLogLocked(now)
	a.mu.Unlock()
}

func (a *PriceAggregator) getOrCreateSourceLocked(name string) *sourceState {
	if st, ok := a.sources[name]; ok {
		return st
	}
	st := &sourceState{PriceSourceState: PriceSourceState{Name: name}}
	a.sources[name] = st
	return st
}

func (a *PriceAggregator) recomputeLocked(now time.Time) (aggUpdated bool) {
	// determine freshness
	type fresh struct {
		name string
		p    float64
		t    time.Time
	}
	var freshes []fresh
	for _, st := range a.sources {
		if st.LastUpdateAt.IsZero() || st.LastPrice <= 0 {
			continue
		}
		if now.Sub(st.LastUpdateAt) <= a.cfg.StaleWindow {
			freshes = append(freshes, fresh{name: st.Name, p: st.LastPrice, t: st.LastUpdateAt})
		}
	}

	prevUpdatedAt := a.agg.AggUpdatedAt
	prevPrice := a.agg.AggPrice
	prevDiv := a.agg.DivergencePct
	prevConf := a.agg.Confidence

	var (
		aggPrice     float64
		aggUpdatedAt time.Time
		divPct       float64
		conf         float64
		mode         = "normal"
		reason       = ""
	)

	if len(freshes) >= 2 {
		// Use exactly 2 sources for now: sort by name for determinism.
		sort.Slice(freshes, func(i, j int) bool { return freshes[i].name < freshes[j].name })
		p1, p2 := freshes[0].p, freshes[1].p
		aggPrice = (p1 + p2) / 2.0
		aggUpdatedAt = maxTime(freshes[0].t, freshes[1].t)
		mean := (p1 + p2) / 2.0
		if mean > 0 {
			divPct = math.Abs(p1-p2) / mean
		}
		conf = 1.0
		if divPct > a.cfg.DivergencePct {
			conf = 0.6
			mode = "degraded"
			reason = "divergence"
		}
	} else if len(freshes) == 1 {
		aggPrice = freshes[0].p
		aggUpdatedAt = freshes[0].t
		divPct = 0
		conf = 0.7
		mode = "degraded"
		reason = "single_source"
	} else {
		// No fresh source: keep last aggregate; staleness will handle gate/risk.
		aggPrice = a.agg.AggPrice
		aggUpdatedAt = a.agg.AggUpdatedAt
		divPct = a.agg.DivergencePct
		conf = 0
		mode = "degraded"
		reason = "no_fresh_source"
	}

	age := time.Duration(0)
	if !aggUpdatedAt.IsZero() {
		age = now.Sub(aggUpdatedAt)
	}
	stale := aggUpdatedAt.IsZero() || age > a.cfg.StaleWindow
	frozen := aggUpdatedAt.IsZero() || age > a.cfg.FreezeWindow
	if frozen {
		mode = "frozen"
		reason = "price_frozen"
		conf = 0
	} else if stale {
		mode = "degraded"
		reason = "price_stale"
		conf = 0
	}

	a.agg.AggPrice = aggPrice
	a.agg.AggUpdatedAt = aggUpdatedAt
	a.agg.DivergencePct = divPct
	a.agg.Confidence = conf
	a.agg.Stale = stale
	a.agg.StaleAgeMs = durationMs(age)

	a.risk.Mode = mode
	a.risk.Reason = reason

	aggUpdated = !prevUpdatedAt.Equal(a.agg.AggUpdatedAt) || prevPrice != a.agg.AggPrice || prevDiv != a.agg.DivergencePct || prevConf != a.agg.Confidence
	a.maybeEmitTransitionsLocked(now, stale, frozen, mode)
	return aggUpdated
}

func (a *PriceAggregator) maybeEmitTransitionsLocked(now time.Time, stale, frozen bool, mode string) {
	if stale != a.lastStale {
		a.lastStale = stale
		if stale {
			log.Printf("[Market] state=stale age_ms=%d", a.agg.StaleAgeMs)
		} else {
			log.Printf("[Market] state=recovered age_ms=%d", a.agg.StaleAgeMs)
		}
	}
	if frozen != a.lastFrozen {
		a.lastFrozen = frozen
		if frozen {
			log.Printf("[Market] state=frozen age_ms=%d", a.agg.StaleAgeMs)
		} else {
			log.Printf("[Market] state=unfrozen age_ms=%d", a.agg.StaleAgeMs)
		}
	}
	if mode != a.lastMode {
		a.lastMode = mode
		log.Printf("[Market] risk.mode=%s reason=%s", a.risk.Mode, a.risk.Reason)
	}
}

func (a *PriceAggregator) maybeLogLocked(now time.Time) {
	if a.cfg.LogInterval <= 0 {
		return
	}
	if !a.lastLoggedAt.IsZero() && now.Sub(a.lastLoggedAt) < a.cfg.LogInterval {
		return
	}
	a.lastLoggedAt = now

	srcs := make([]string, 0, len(a.sources))
	for _, st := range a.sources {
		srcs = append(srcs, fmt.Sprintf("%s{conn=%v age_ms=%d err_1m=%d}", st.Name, st.Connected, durationMs(now.Sub(st.LastUpdateAt)), st.Err1m))
	}
	sort.Strings(srcs)

	log.Printf("[Market] agg=%.4f age_ms=%d stale=%v conf=%.2f mode=%s div_pct=%.4f src=%v",
		a.agg.AggPrice,
		a.agg.StaleAgeMs,
		a.agg.Stale,
		a.agg.Confidence,
		a.risk.Mode,
		a.agg.DivergencePct,
		srcs,
	)
}

func pruneTimes(xs []time.Time, cutoff time.Time) []time.Time {
	n := 0
	for _, t := range xs {
		if t.After(cutoff) {
			xs[n] = t
			n++
		}
	}
	return xs[:n]
}

func durationMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func maxTime(a1, a2 time.Time) time.Time {
	if a1.After(a2) {
		return a1
	}
	return a2
}

func stringsToLegacyTickerSymbol(sym SymbolSpec) string {
	// Existing code expects Binance-style symbol (e.g., ETHUSDT) in events.
	return stringsToUpperNoSep(sym.Display)
}

func stringsToUpperNoSep(display string) string {
	out := make([]rune, 0, len(display))
	for _, r := range display {
		if r == '/' || r == '-' || r == '_' || r == ' ' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			out = append(out, r-('a'-'A'))
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}
