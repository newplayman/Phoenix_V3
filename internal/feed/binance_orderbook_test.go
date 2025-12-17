package feed

import (
	"context"
	"testing"
)

func TestBinanceOrderbookSync_SeqGapTriggersResyncSnapshot(t *testing.T) {
	ctx := context.Background()

	calls := 0
	fetch := func(ctx context.Context) (*BinanceDepthSnapshot, error) {
		_ = ctx
		calls++
		switch calls {
		case 1:
			return &BinanceDepthSnapshot{
				LastUpdateID: 10,
				Bids:         [][]string{{"100.0", "1"}},
				Asks:         [][]string{{"101.0", "1"}},
			}, nil
		default:
			return &BinanceDepthSnapshot{
				LastUpdateID: 200,
				Bids:         [][]string{{"200.0", "1"}},
				Asks:         [][]string{{"201.0", "1"}},
			}, nil
		}
	}

	s := NewBinanceOrderbookSync("ETHUSDT", fetch)
	if _, err := s.Snapshot(ctx, ResyncStart); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := s.LastUpdateID(); got != 10 {
		t.Fatalf("lastUpdateId got=%d want=10", got)
	}

	// Gap: want=11, but FirstU=50 -> should resync via REST snapshot.
	d := BinanceDepthDelta{
		EventType: "depthUpdate",
		EventTime: 123,
		Symbol:    "ETHUSDT",
		FirstU:    50,
		LastU:     50,
		Bids:      [][]string{{"100.5", "1"}},
		Asks:      [][]string{{"101.5", "1"}},
	}
	_, snapEv, err := s.ApplyDelta(ctx, d)
	if err != nil {
		t.Fatalf("apply delta: %v", err)
	}
	if snapEv == nil || snapEv.Type != OrderbookSnapshotType || snapEv.Reason != string(ResyncSeqGap) {
		t.Fatalf("expected resync snapshot event, got %#v", snapEv)
	}
	if got := s.LastUpdateID(); got != 200 {
		t.Fatalf("lastUpdateId after resync got=%d want=200", got)
	}
	top := s.Top()
	if top.BestBid != 200.0 || top.BestAsk != 201.0 {
		t.Fatalf("top after resync got bid=%v ask=%v", top.BestBid, top.BestAsk)
	}
}
