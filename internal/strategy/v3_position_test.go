package strategy

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeOnchainFetcher struct {
	lower int64
	upper int64
	err   error
}

func (f fakeOnchainFetcher) Fetch(_ context.Context, now time.Time, _ string, _ string, _ uint64) (int64, int64, time.Time, error) {
	if f.err != nil {
		return 0, 0, time.Time{}, f.err
	}
	return f.lower, f.upper, now, nil
}

func TestPositionFallbackOnchainThenAssumedThenNone(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	cfg := defaultV3RebalanceConfig()
	cfg.PositionTokenID = 1
	cfg.NPMAddress = "0xC36442b4a4522E871399CD717aBDD847Ab11FE88"
	cfg.ChainRPCURL = "http://example.invalid"
	cfg.HasAssumedRange = true
	cfg.AssumedLowerTick = 0
	cfg.AssumedUpperTick = 600

	// 1) onchain wins
	r := &V3PositionResolver{fetcher: fakeOnchainFetcher{lower: -120, upper: 120}}
	st := r.Resolve(context.Background(), now, cfg)
	if st.Source != PositionSourceOnchain || st.LowerTick != -120 || st.UpperTick != 120 || st.TokenID != 1 {
		t.Fatalf("expected onchain, got %+v", st)
	}

	// 2) onchain missing -> assumed
	r = &V3PositionResolver{fetcher: fakeOnchainFetcher{err: errors.New("rpc down")}}
	st = r.Resolve(context.Background(), now, cfg)
	if st.Source != PositionSourceConfigAssumed || st.LowerTick != 0 || st.UpperTick != 600 {
		t.Fatalf("expected assumed, got %+v", st)
	}

	// 3) none when no assumed
	cfg.HasAssumedRange = false
	st = r.Resolve(context.Background(), now, cfg)
	if st.Source != PositionSourceNone {
		t.Fatalf("expected none, got %+v", st)
	}
}
