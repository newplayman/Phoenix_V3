# Phoenix V3 API 与事件规范（供前后端对接）

面向对象：前端/后端共同遵循的接口与事件标准。

统一约定：
- Base URL：`/api/v1`
- 认证：`Authorization: Bearer <ADMIN_TOKEN>`（单人场景，读写统一走鉴权）
- 控制面开关：写接口默认关闭；需显式开启 `api.control_plane_enabled: true`（或 `PHOENIX_CONTROL_PLANE_ENABLED=1`）。
- 时间：ISO8601（UTC），字段名 `ts/created_at/updated_at`
- 金额：尽量同时提供 `raw`（字符串）与 `human`（字符串/number），并明确 `decimals`
- 错误格式统一：

```json
{ "error": { "code": "bad_request", "message": "..." , "details": { } } }
```

---

## 1) Read APIs（数据面）

### 1.1 Health

`GET /api/v1/health`

Response:
```json
{
  "bot": { "online": true, "manual_only": false, "last_heartbeat_ts": "2025-12-15T13:00:00Z", "latest_block": 0, "queue_depth": 0 },
  "rpc": { "ok": true, "timeout_rate_5m": 0.01, "p95_latency_ms": 350 },
  "safety": { "dry_run": true, "kill_switch": true, "allow_tx_broadcast": false, "effective_dry_run": true },
  "risk": { "mode": "normal", "consecutive_fails": 0, "daily_gas_used_eth": 0.0, "daily_gas_limit_eth": 0.05 }
}
```

### 1.2 Pools 列表

`GET /api/v1/pools`

Response:
```json
{
  "pools": [
    {
      "pool_id": "tusd-weth-005",
      "chain_id": 421614,
      "pool_address": "0x...",
      "token0": { "address": "0x...", "symbol": "WETH", "decimals": 18 },
      "token1": { "address": "0x...", "symbol": "TUSD2", "decimals": 6 },
      "fee": 500
    }
  ]
}
```

### 1.3 单池实时状态（工作台主接口）

`GET /api/v1/pools/{pool_id}/state`

Response:
```json
{
  "pool_id": "tusd-weth-005",
  "chain_id": 421614,
  "ts": "2025-12-15T13:00:00Z",

  "dex": {
    "tick": -195490,
    "price_stable_per_weth": 3238.93,
    "liquidity": "3515194078760787"
  },

  "cex": {
    "price_stable_per_weth": 2040.12,
    "source": "offline|binance|..."
  },

  "position": {
    "token_id": "221907",
    "tick_lower": -195560,
    "tick_upper": -195450,
    "liquidity": "3515194078760787",
    "in_range": true,
    "distance_to_lower_pct": 0.0,
    "distance_to_upper_pct": 0.0
  },

  "strategy": {
    "profile": "normal",
    "sigma_daily": 0.55,
    "width_pct": 0.002,
    "vol_window": "1m",
    "cooldown_active": false,
    "min_interval": "10s"
  },

  "risk": {
    "mode": "normal",
    "rebalances_last_1h": 3,
    "consecutive_fails": 0
  }
}
```

### 1.4 Intent 列表与详情

`GET /api/v1/intents?pool_id=&status=&limit=&cursor=`

`GET /api/v1/intents/{intent_id}`

Intent detail must include plan steps:
```json
{
  "intent": { "intent_id": "intent-...", "pool_id": "...", "status": "running", "created_at": "...", "metadata": {} },
  "steps": [
    { "step_index": 0, "step_type": "swap", "status": "mined", "tx_hash": "0x...", "details": { "from": "0x..", "to": "0x..", "amount_in_raw": "..." } }
  ]
}
```

### 1.5 Trades/Tx

`GET /api/v1/tx?pool_id=&intent_id=&status=&limit=&cursor=`

Response:
```json
{
  "tx": [
    {
      "chain_id": 421614,
      "tx_hash": "0x...",
      "status": "pending|mined|reverted|...",
      "intent_id": "intent-...",
      "pool_id": "tusd-weth-005",
      "receipt": {
        "chain_id": 421614,
        "tx_hash": "0x...",
        "nonce": 1,
        "from_addr": "0x..",
        "to_addr": "0x..",
        "status": 1,
        "gas_used": 12345,
        "effective_gas_price": "123",
        "revert_reason": "",
        "mined_at": "2025-12-15T13:00:00Z"
      }
    }
  ],
  "next_cursor": "0"
}
```

### 1.6 Audit

`GET /api/v1/audit?action_type=&pool_id=&limit=&cursor=`

Response:
```json
{
  "actions": [
    {
      "ts": "2025-12-15T13:00:00Z",
      "actor": "admin",
      "action_type": "preview_rebalance|execute_rebalance|pause_pool|resume_pool",
      "pool_id": "tusd-weth-005",
      "chain_id": 421614,
      "request": {},
      "result": {}
    }
  ],
  "next_cursor": "0"
}
```

---

## 2) Write APIs（控制面）

所有写接口必须：
- Bearer token
- `reason` 必填
- `confirm_text` 必须为 `CONFIRM`
- 支持 `idempotency_key`

### 2.1 Preview Operation（通用）

`POST /api/v1/operations/preview`

Request:
```json
{
  "action_type": "force_rebalance",
  "pool_id": "tusd-weth-005",
  "chain_id": 421614,
  "params": { },
  "idempotency_key": "optional-client-guid"
}
```

Notes:
- 当前 `operations/preview` 仅实现 `force_rebalance`；其他 `action_type` 预留，后端会返回 `400`（`error.code=unsupported`）。
- `pause/resume` 使用独立端点（见 2.3），不走 `operations/*`。

Response:
```json
{
  "operation_id": "op_...",
  "action_type": "force_rebalance",
  "pool_id": "tusd-weth-005",
  "warnings": ["may temporarily reduce pool liquidity", "cooldown active may skip"],
  "estimated_gas": { "eth": 0.0021 },
  "plan": [
    { "step_index": 0, "step_type": "close", "summary": "withdraw+collect+burn tokenId=..." },
    { "step_index": 1, "step_type": "swap", "summary": "TUSD2 -> WETH amount_in=...", "slippage_pct": 2.0 },
    { "step_index": 2, "step_type": "mint", "summary": "ticks=[...,...] amount0=... amount1=..." }
  ],
  "expires_in_sec": 300
}
```

### 2.2 Execute Operation

`POST /api/v1/operations/execute`

Request:
```json
{
  "operation_id": "op_...",
  "confirm_text": "CONFIRM",
  "pool_id": "tusd-weth-005",
  "reason": "why we do this",
  "idempotency_key": "optional-client-guid"
}
```

Response:
```json
{
  "operation_id": "op_...",
  "status": "queued",
  "intent_id": "intent-...",
  "links": { "intent": "/api/v1/intents/intent-..." }
}
```

### 2.3 Pause/Resume（可直接做成 action_type，也可单独端点）

`POST /api/v1/pools/{pool_id}/pause`
`POST /api/v1/pools/{pool_id}/resume`

Request:
```json
{ "confirm_text": "CONFIRM", "reason": "why", "idempotency_key": "optional-client-guid" }
```

---

## 3) 实时事件（SSE）

`GET /api/v1/stream`

SSE event format：
- event: `<type>`
- data: JSON string

Keepalive:
- The server also sends periodic keepalive comment lines: `: ping <unix>`

事件类型建议（最小集）：
- `heartbeat`
- `pool_state`
- `strategy_eval`
- `intent_update`
- `step_update`
- `tx_receipt`
- `risk_update`
- `alert`
- `operator_action`

### 3.1 `pool_state`

```json
{
  "type": "pool_state",
  "ts": "2025-12-15T13:00:00Z",
  "pool_id": "tusd-weth-005",
  "chain_id": 421614,
  "tick": -195490,
  "liquidity": "3515...",
  "dex_price_stable_per_weth": 3238.93
}
```

### 3.2 `step_update`

```json
{
  "type": "step_update",
  "ts": "2025-12-15T13:00:00Z",
  "intent_id": "intent-...",
  "step_index": 1,
  "step_type": "swap",
  "status": "sent|mined|failed|skipped",
  "tx_hash": "0x...",
  "details": { "from": "0x..", "to": "0x..", "amount_in_raw": "..." }
}
```

---

## 4) 必须的服务端校验清单（与前端无关）

写操作执行前必须校验：
- recipient 非零地址、非黑洞地址
- 目标合约在 allowlist
- 幂等键去重
- gas 预算/连续失败熔断
- pool liquidity=0 时的 swap 处理（测试网 keep-liquidity 或先 mint 后 swap）
