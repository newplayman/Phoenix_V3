package bot

import (
	"math"
	"testing"
	"time"
)

func TestVolatilityEstimator_ConstantPriceZero(t *testing.T) {
	v := NewVolatilityEstimator(10 * time.Minute)
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 10; i++ {
		v.Add(100.0, base.Add(time.Duration(i)*time.Minute))
	}
	if got := v.SigmaDaily(); got != 0 {
		t.Fatalf("expected sigma=0, got %f", got)
	}
}

func TestVolatilityEstimator_AlternatingReturns(t *testing.T) {
	v := NewVolatilityEstimator(20 * time.Minute)
	base := time.Unix(1_700_000_000, 0).UTC()

	eps := 0.001
	p0 := 100.0
	for i := 0; i < 20; i++ {
		p := p0
		if i%2 == 1 {
			p = p0 * (1.0 + eps)
		}
		v.Add(p, base.Add(time.Duration(i)*time.Minute))
	}

	wantR := math.Log(1.0 + eps)
	// For symmetric +/-r, sample sigma equals |r|. Scaled to daily by sqrt(samplesPerDay).
	want := wantR * math.Sqrt(86400.0/60.0)

	got := v.SigmaDaily()
	if got <= 0 {
		t.Fatalf("expected sigma>0, got %f", got)
	}
	if math.Abs(got-want) > want*0.10 { // allow 10% tolerance due to sample variance edge effects
		t.Fatalf("sigma mismatch: got=%f want~=%f", got, want)
	}
}

func TestVolatilityEstimator_PrunesOldPoints(t *testing.T) {
	v := NewVolatilityEstimator(3 * time.Minute)
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 10; i++ {
		v.Add(100.0+float64(i), base.Add(time.Duration(i)*time.Minute))
	}
	v.mu.Lock()
	n := len(v.points)
	v.mu.Unlock()
	if n > 5 { // last ~3m plus boundary points
		t.Fatalf("expected pruning, points=%d", n)
	}
}
