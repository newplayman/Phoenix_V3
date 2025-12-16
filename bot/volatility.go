package bot

import (
	"math"
	"sync"
	"time"
)

type pricePoint struct {
	t     time.Time
	price float64
}

// VolatilityEstimator estimates daily realized volatility from recent log-returns.
// It is used as an orchestration-side helper: feed events in, sigma_daily out.
type VolatilityEstimator struct {
	mu     sync.Mutex
	window time.Duration
	points []pricePoint
}

func NewVolatilityEstimator(window time.Duration) *VolatilityEstimator {
	if window <= 0 {
		window = 6 * time.Hour
	}
	return &VolatilityEstimator{window: window}
}

func (v *VolatilityEstimator) SetWindow(window time.Duration) {
	if v == nil || window <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.window = window
}

func (v *VolatilityEstimator) Add(price float64, t time.Time) {
	if v == nil || price <= 0 {
		return
	}
	if t.IsZero() {
		t = time.Now()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.points = append(v.points, pricePoint{t: t, price: price})

	cutoff := t.Add(-v.window)
	drop := 0
	for _, p := range v.points {
		if p.t.After(cutoff) {
			break
		}
		drop++
	}
	if drop > 0 && drop < len(v.points) {
		v.points = append([]pricePoint(nil), v.points[drop:]...)
	}
}

// SigmaDaily estimates daily realized volatility from log-returns in the window.
func (v *VolatilityEstimator) SigmaDaily() float64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	points := append([]pricePoint(nil), v.points...)
	v.mu.Unlock()

	if len(points) < 3 {
		return 0
	}

	rets := make([]float64, 0, len(points)-1)
	var totalDt float64
	for i := 1; i < len(points); i++ {
		p0 := points[i-1].price
		p1 := points[i].price
		if p0 <= 0 || p1 <= 0 {
			continue
		}
		dt := points[i].t.Sub(points[i-1].t).Seconds()
		if dt <= 0 {
			continue
		}
		r := math.Log(p1 / p0)
		rets = append(rets, r)
		totalDt += dt
	}
	if len(rets) < 2 || totalDt <= 0 {
		return 0
	}

	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))

	varSum := 0.0
	for _, r := range rets {
		d := r - mean
		varSum += d * d
	}
	sigmaSample := math.Sqrt(varSum / float64(len(rets)-1))

	avgInterval := totalDt / float64(len(rets))
	if avgInterval <= 0 {
		return 0
	}
	samplesPerDay := 86400.0 / avgInterval
	if samplesPerDay <= 0 {
		return 0
	}
	return sigmaSample * math.Sqrt(samplesPerDay)
}
