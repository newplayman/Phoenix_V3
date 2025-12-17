# Orderbook Raw Log + Replay (Step 2-2)

目标：
- raw log 必须同时包含 `ORDERBOOK_SNAPSHOT` 与 `ORDERBOOK_DELTA`
- replay 必须可重建 `bestBid/bestAsk/spread`
- `seq gap` 必须触发 resync（REST snapshot）

本仓库实现方式：
- raw log topic：`orderbook_raw`（见 `internal/events/stream.go:1`）
- Binance 深度：
  - REST snapshot：`GET /api/v3/depth`（limit=1000）
  - WS delta：`@depth@100ms`
- Raw log 契约（source of truth）：`docs/marketdata/ORDERBOOK_RAW_SPEC.md:1`

## 1) 生成 raw log（runner）

运行 120s（默认），输出 events jsonl：

```bash
go run ./cmd/orderbookrunner -symbol ETHUSDT -duration 120s -out /tmp/orderbook_raw_120s.jsonl
```

期望：
- 输出末尾包含 `snapshots>0` 且 `deltas>0`
- 若发生 seq gap，会看到 `resyncs>=1`（不保证每次都触发；但逻辑有单测覆盖）

一键（runner + replay，默认 120s）：

```bash
make rehearsal-orderbook-120s
```

示例输出（节选）：

```text
status=done symbol=ETHUSDT duration=2m0s snapshots=2 deltas=1188 resyncs=1 stale_deltas=2 ... out=/tmp/orderbook_raw_120s.jsonl
status=ok path=/tmp/orderbook_raw_120s.jsonl symbol=ETHUSDT types=map[ORDERBOOK_DELTA:1188 ORDERBOOK_SNAPSHOT:2] ... gaps=1 ... best_bid=... best_ask=... spread=...
```

## 2) Replay + 统计 + 最终 top-of-book

```bash
go run ./cmd/orderbookreplay -path /tmp/orderbook_raw_120s.jsonl -symbol ETHUSDT
```

期望输出字段：
- `types=map[ORDERBOOK_DELTA:... ORDERBOOK_SNAPSHOT:...]`
- `best_bid / best_ask / spread`
- `gaps`（若 raw log 中存在 seq gap delta）

## 3) 测试（必须）

```bash
go test ./...
```

覆盖：
- snapshot+delta 重建 top-of-book（`internal/feed/orderbook_test.go:1`）
- seq gap 触发 resync snapshot（`internal/feed/binance_orderbook_test.go:1`）
