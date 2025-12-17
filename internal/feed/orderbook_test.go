package feed

import "testing"

func TestOrderbookTop_SnapshotAndDelta(t *testing.T) {
	ob := NewOrderbook()
	ob.ApplySnapshot(
		[][]string{{"100.0", "1.0"}, {"99.5", "2.0"}},
		[][]string{{"101.0", "1.5"}, {"102.0", "3.0"}},
	)
	top := ob.Top()
	if top.BestBid != 100.0 {
		t.Fatalf("bestBid got=%v want=100.0", top.BestBid)
	}
	if top.BestAsk != 101.0 {
		t.Fatalf("bestAsk got=%v want=101.0", top.BestAsk)
	}
	if top.Spread != 1.0 {
		t.Fatalf("spread got=%v want=1.0", top.Spread)
	}

	// Delta: remove best ask, improve best bid.
	ob.ApplyDelta(
		[][]string{{"100.2", "0.5"}},
		[][]string{{"101.0", "0"}},
	)
	top = ob.Top()
	if top.BestBid != 100.2 {
		t.Fatalf("bestBid after delta got=%v want=100.2", top.BestBid)
	}
	if top.BestAsk != 102.0 {
		t.Fatalf("bestAsk after delta got=%v want=102.0", top.BestAsk)
	}
	if top.Spread != 1.7999999999999972 && top.Spread != 1.8 {
		t.Fatalf("spread after delta got=%v want~1.8", top.Spread)
	}
}
