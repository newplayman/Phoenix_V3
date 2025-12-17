# Orderbook Raw Log Spec (Step 2-2)

用途：用于离线回放并重建 Top-of-Book（`best_bid/best_ask/spread`），不属于 SSE / HTTP API。

相关工具：
- 生产者：`cmd/orderbookrunner`（写入 JSONL）
- 消费者：`cmd/orderbookreplay`（读取 JSONL 并回放统计）

格式：
- JSONL：一行一个 JSON 对象（行顺序即事件顺序）
- 所有数值字段保持“无损”优先：价格/数量用字符串数组 `["price","qty"]`

事件类型（必须出现）：
- `ORDERBOOK_SNAPSHOT`：快照（来自 REST snapshot / resync）
- `ORDERBOOK_DELTA`：增量（来自 WS depth update）

## 1) `ORDERBOOK_SNAPSHOT`

语义：替换本地 orderbook 状态（重置 + 应用快照）。

字段（最小集）：
- `type`: `"ORDERBOOK_SNAPSHOT"`
- `exchange`: `"binance"`（或其他交易所名）
- `symbol`: `"ETHUSDT"`（交易对）
- `last_update_id`: REST snapshot 的 `lastUpdateId`
- `reason`: `"start" | "seq_gap" | "reconnect"`
- `prev_last_update_id`: 可选；仅用于诊断（resync 前 last_update_id）
- `bids`, `asks`: `[][]string`，形如 `[["price","qty"], ...]`

示例：
```json
{
  "type": "ORDERBOOK_SNAPSHOT",
  "exchange": "binance",
  "symbol": "ETHUSDT",
  "last_update_id": 67282411999,
  "reason": "start",
  "prev_last_update_id": 0,
  "bids": [["2922.74", "1.23"]],
  "asks": [["2922.75", "0.56"]]
}
```

## 2) `ORDERBOOK_DELTA`

语义：在已存在 snapshot 的前提下，按价格档位更新（qty=0 表示删除该档位）。

字段（最小集）：
- `type`: `"ORDERBOOK_DELTA"`
- `exchange`, `symbol`
- `event_time_ms`: 可选；WS 事件时间（毫秒）
- `seq_start`, `seq_end`: WS delta 序列范围（例如 Binance depthUpdate 的 `U/u`）
- `bids`, `asks`: 变更档位
- `discarded_as_stale`: 可选；用于诊断（被判定为 stale 的 delta）

示例：
```json
{
  "type": "ORDERBOOK_DELTA",
  "exchange": "binance",
  "symbol": "ETHUSDT",
  "event_time_ms": 1734441400123,
  "seq_start": 67282412000,
  "seq_end": 67282412010,
  "bids": [["2922.74", "0"]],
  "asks": [["2922.75", "0.44"]],
  "discarded_as_stale": false
}
```

## 3) 回放正确性要求（硬性）

- 必须先收到 `ORDERBOOK_SNAPSHOT`，才能应用后续 `ORDERBOOK_DELTA`
- 检测到 seq gap 时必须触发 resync（REST snapshot），并写入新的 `ORDERBOOK_SNAPSHOT`（`reason=seq_gap`）
- 回放后必须可重建并输出 Top-of-Book：
  - `best_bid`（最高 bid 价）
  - `best_ask`（最低 ask 价）
  - `spread = best_ask - best_bid`
