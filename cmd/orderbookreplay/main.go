package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"phoenix-v3/internal/events"
	"phoenix-v3/internal/feed"
)

type fileEvent struct {
	Topic     events.Topic    `json:"topic"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func main() {
	var (
		path   = flag.String("path", "logs/orderbook_raw.jsonl", "events jsonl file path")
		symbol = flag.String("symbol", "", "optional symbol filter (e.g. ETHUSDT)")
	)
	flag.Parse()

	f, err := os.Open(*path)
	if err != nil {
		log.Fatalf("open %s failed: %v", *path, err)
	}
	defer f.Close()

	wantSym := strings.ToUpper(strings.TrimSpace(*symbol))

	book := feed.NewOrderbook()
	haveSnapshot := false
	lastUpdateID := int64(0)
	seenDelta := false

	typeCounts := map[string]int{}
	snapshotCount := 0
	deltaCount := 0
	appliedDelta := 0
	staleDelta := 0
	gapCount := 0

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var fe fileEvent
		if err := json.Unmarshal(line, &fe); err != nil {
			continue
		}
		if fe.Topic != events.TopicOrderbookRaw {
			continue
		}
		var ev feed.OrderbookRawEvent
		if err := json.Unmarshal(fe.Payload, &ev); err != nil {
			continue
		}
		if wantSym != "" && strings.ToUpper(ev.Symbol) != wantSym {
			continue
		}
		typeCounts[ev.Type]++
		switch ev.Type {
		case feed.OrderbookSnapshotType:
			book.ApplySnapshot(ev.Bids, ev.Asks)
			haveSnapshot = true
			seenDelta = false
			lastUpdateID = ev.LastUpdateID
			snapshotCount++
		case feed.OrderbookDeltaType:
			deltaCount++
			if !haveSnapshot {
				continue
			}
			if ev.SeqEnd <= lastUpdateID {
				staleDelta++
				continue
			}
			want := lastUpdateID + 1
			okSeq := false
			if !seenDelta {
				okSeq = ev.SeqStart <= want && want <= ev.SeqEnd
			} else {
				okSeq = ev.SeqStart == want
			}
			if !okSeq {
				gapCount++
				haveSnapshot = false
				continue
			}
			book.ApplyDelta(ev.Bids, ev.Asks)
			lastUpdateID = ev.SeqEnd
			seenDelta = true
			appliedDelta++
		default:
			// ignore unknown
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan failed: %v", err)
	}

	top := book.Top()
	if snapshotCount == 0 || deltaCount == 0 {
		fmt.Printf("status=error path=%s symbol=%s types=%v snapshots=%d deltas=%d applied_deltas=%d stale_deltas=%d gaps=%d last_update_id=%d %s\n",
			*path, wantSym, typeCounts, snapshotCount, deltaCount, appliedDelta, staleDelta, gapCount, lastUpdateID, feed.FormatTop(top))
		os.Exit(2)
	}
	fmt.Printf("status=ok path=%s symbol=%s types=%v snapshots=%d deltas=%d applied_deltas=%d stale_deltas=%d gaps=%d last_update_id=%d %s\n",
		*path, wantSym, typeCounts, snapshotCount, deltaCount, appliedDelta, staleDelta, gapCount, lastUpdateID, feed.FormatTop(top))
}
