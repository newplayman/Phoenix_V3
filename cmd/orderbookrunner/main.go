package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"phoenix-v3/internal/events"
	"phoenix-v3/internal/feed"
)

func main() {
	var (
		symbol    = flag.String("symbol", "ETHUSDT", "Binance symbol (e.g. ETHUSDT)")
		duration  = flag.Duration("duration", 120*time.Second, "how long to run")
		outPath   = flag.String("out", "logs/orderbook_raw.jsonl", "output raw log path (events jsonl)")
		logEvery  = flag.Duration("log-every", 10*time.Second, "print top-of-book periodically")
		startWith = flag.Bool("snapshot-first", true, "fetch REST snapshot before consuming WS deltas")
	)
	flag.Parse()

	sym := strings.ToUpper(strings.TrimSpace(*symbol))
	if sym == "" {
		log.Fatal("missing -symbol")
	}
	if *duration <= 0 {
		log.Fatal("duration must be > 0")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	stream, err := events.NewFileStream(*outPath)
	if err != nil {
		log.Fatalf("open filestream: %v", err)
	}
	defer stream.Close()

	obClient := feed.NewBinanceOrderbook(sym)
	sync := feed.NewBinanceOrderbookSync(sym, func(ctx context.Context) (*feed.BinanceDepthSnapshot, error) {
		return obClient.FetchSnapshot(ctx)
	})

	snapCount := 0
	deltaCount := 0
	resyncCount := 0
	staleCount := 0

	publish := func(ev feed.OrderbookRawEvent) {
		if err := stream.Publish(context.Background(), events.TopicOrderbookRaw, ev); err != nil {
			log.Printf("publish failed: %v", err)
		}
	}

	if *startWith {
		snap, err := sync.Snapshot(ctx, feed.ResyncStart)
		if err != nil {
			log.Fatalf("initial snapshot failed: %v", err)
		}
		publish(snap)
		snapCount++
	}

	wsCtx, wsCancel := context.WithTimeout(context.Background(), *duration)
	defer wsCancel()
	conn, err := obClient.DialWS(wsCtx)
	if err != nil {
		log.Fatalf("ws dial failed: %v", err)
	}
	defer conn.Close()

	durStr := (*duration).String()
	log.Printf("[orderbookrunner] started symbol=%s duration=%s out=%s", sym, durStr, *outPath)
	lastLog := time.Now()
	for {
		select {
		case <-ctx.Done():
			top := sync.Top()
			fmt.Printf("status=done symbol=%s duration=%s snapshots=%d deltas=%d resyncs=%d stale_deltas=%d last_update_id=%d %s out=%s\n",
				sym, durStr, snapCount, deltaCount, resyncCount, staleCount, sync.LastUpdateID(), feed.FormatTop(top), *outPath)
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Best-effort reconnect + resync.
			log.Printf("[orderbookrunner] ws read error: %v (resync+reconnect)", err)
			snap, sErr := sync.Snapshot(context.Background(), feed.ResyncReconnect)
			if sErr == nil {
				publish(snap)
				snapCount++
				resyncCount++
			}
			conn.Close()
			conn, err = obClient.DialWS(context.Background())
			if err != nil {
				log.Printf("[orderbookrunner] ws reconnect failed: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			continue
		}

		var d feed.BinanceDepthDelta
		if err := json.Unmarshal(msg, &d); err != nil {
			continue
		}
		ev, snapEv, err := sync.ApplyDelta(context.Background(), d)
		if err != nil {
			log.Printf("[orderbookrunner] apply delta failed: %v", err)
			continue
		}
		publish(ev)
		deltaCount++
		if ev.DiscardedAsStale {
			staleCount++
		}
		if snapEv != nil {
			publish(*snapEv)
			snapCount++
			resyncCount++
		}

		if *logEvery > 0 && time.Since(lastLog) >= *logEvery {
			lastLog = time.Now()
			top := sync.Top()
			fmt.Fprintf(os.Stdout, "status=running ts=%s symbol=%s last_update_id=%d %s\n", time.Now().UTC().Format(time.RFC3339), sym, sync.LastUpdateID(), feed.FormatTop(top))
		}
	}
}
