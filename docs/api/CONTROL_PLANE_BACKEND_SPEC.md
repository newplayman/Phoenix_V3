# Phoenix V3 控制台后端开发规范（Control Plane + Data Plane）

面向对象：后端开发（单人使用场景，但按“生产级安全”实现）。

目标：为 `./web` 提供“可观测 + 可控制 + 可审计 + 可回放/归因”的 API 与数据能力；前端不持私钥、不直连 RPC；所有写操作都必须通过服务端风控与审计。

---

## 0. 约束与决策

- 数据落点：Postgres/Supabase（通过 `SUPABASE_DB_URL`）。
- 使用方式：单人使用（仍需要鉴权与二次确认，防误触）。
- 安全要求：所有写操作必须 `preview → confirm → execute`，支持幂等、冷却、熔断、审计。
- 默认关闭：写接口默认禁用；需显式开启 `api.control_plane_enabled: true`（或 `PHOENIX_CONTROL_PLANE_ENABLED=1`）。
- 广播交易默认禁用：除非同时满足 `strategy.dry_run=false` + `safety.kill_switch=false` + `safety.allow_tx_broadcast=true`。

---

## 1. 模块划分（后端要提供什么）

### 1.1 Data Plane（读数据 + 实时流）

- 池状态：tick/price/liquidity、当前 tokenId、当前区间、sigma/width、profile、风险模式。
- 执行记录：intent、plan、tx、receipt、revert reason、gas。
- 资金与 allowance：钱包余额、LP 内资产、关键合约 allowance（只读展示）。
- 系统健康：bot 心跳、RPC 延迟/超时率、队列长度、失败率。
- 实时推送：SSE（优先）或 WebSocket。

### 1.2 Control Plane（写操作：受控执行）

写操作必须满足：
- `preview`：只返回“将要执行的步骤序列 + 风险提示 + 估算 gas（可选）”，不发交易。
- `confirm`：二次确认（输入 `pool_id` + `CONFIRM`）+ 必填 reason。
- `execute`：进入队列，执行端（bot/执行器）按步骤发链上交易并回写结果。
- 幂等：同一 `idempotency_key` 不得重复执行。
- 审计：所有操作（含 preview 失败）写入 `operator_actions`。

---

## 2. 鉴权（单人也要做）

### 2.1 方案

- 使用 `ADMIN_TOKEN`（环境变量）进行 Bearer Token 鉴权。
- 所有写接口必须鉴权；读接口建议也鉴权（默认同一套即可）。

### 2.2 规范

- Header：`Authorization: Bearer <token>`
- 失败返回：`401` + `{ "error": { "code": "unauthorized" } }`

---

## 3. 数据库表（最小可用 + 可扩展）

使用 GORM migrate；表名建议 snake_case；关键字段加索引。

### 3.1 `bot_heartbeats`

- `id` (pk)
- `bot_id`（默认 `default`）
- `chain_id`
- `ts`
- `latest_block`
- `queue_depth`
- `risk_mode`
- `rpc_url_hash`（不要明文存 secrets）
- indexes：`(bot_id, ts desc)`

### 3.2 `pool_snapshots`（时间序列，给回放/图表用）

- `id` (pk)
- `pool_id`, `chain_id`, `ts`
- `dex_tick`, `dex_price`
- `liquidity`（字符串或 numeric）
- `position_token_id`
- `pos_tick_lower`, `pos_tick_upper`, `pos_liquidity`
- `sigma_daily`, `width_pct`, `profile`
- indexes：`(pool_id, ts desc)`

### 3.3 `intents`

- `id`（string，intent_id）
- `pool_id`, `chain_id`, `type`
- `status`：`generated|planned|running|succeeded|failed|canceled`
- `risk_mode`, `strategy_version`
- `metadata`（jsonb）
- `created_at`, `updated_at`
- indexes：`(pool_id, created_at desc)`, `(status)`

### 3.4 `intent_steps`（plan 的每一步）

- `id` (pk)
- `intent_id`（fk）
- `step_type`：`close|collect|burn|swap|mint|approve|other`
- `step_index`
- `status`：`pending|sent|mined|failed|skipped`
- `tx_hash`（nullable）
- `details`（jsonb：amounts、token、minOut、ticks…）
- indexes：`(intent_id, step_index)`

### 3.5 `tx_receipts`

- `id` (pk)
- `chain_id`, `tx_hash`（unique）
- `nonce`, `from_addr`, `to_addr`
- `status`（1/0）
- `gas_used`, `effective_gas_price`
- `revert_reason`（text）
- `mined_at`

### 3.6 `operator_actions`（控制台审计）

- `id` (pk)
- `ts`
- `actor`（固定 `admin` 或 `console`）
- `action_type`：`pause|resume|preview_rebalance|execute_rebalance|close|collect|set_risk_mode|kill_switch|apply_config`
- `pool_id`, `chain_id`
- `request`（jsonb：前端提交参数、reason、idempotency_key）
- `result`（jsonb：operation_id、intent_id、tx_hashes、错误信息）
- indexes：`(ts desc)`, `(pool_id, ts desc)`

---

## 4. 写操作的风控（服务端必须兜底）

### 4.1 硬拦截（直接拒绝）

- recipient 为 `0x0000…`/`0xdead…`
- 目标合约地址不在 allowlist（PositionManager、SwapHelper、Pool、ERC20）
- `max_daily_gas` 已超预算
- `consecutive_fails` 超阈值且当前非 `admin_override`

### 4.2 软拦截（需要强确认/降级）

- 预计会导致短时 `pool liquidity=0`
- 预计 churn：最近 10 分钟内 rebalances 超阈值
- RPC 健康差：超时率高/延迟高（建议进入 `caution`）

---

## 5. 与 bot 的协作方式（建议实现）

### 5.1 方式 A（推荐）：API Server 直接调用执行器

- 前端调用写接口 → API Server 生成 intent/plan → 将 intent 投递到 bot 内部 queue（内存/redis/file stream）→ 执行器消费。
- 优点：闭环最短，实时可追踪。

### 5.2 方式 B（可选）：DB/Stream 作为命令总线

- 写接口创建 `operations` 表记录 → bot 轮询/订阅 stream 执行 → 回写。

单人使用优先方式 A。

---

## 6. 验收用例（后端）

1) `preview_rebalance` 返回 plan（含 swap/close/mint 步骤，且不产生链上 tx）。
2) `execute_rebalance` 必须带 `reason` + `confirm_text=CONFIRM` + `pool_id`，否则 400。
3) 重复提交同一 `idempotency_key`，返回同一个 `operation_id`，不重复发交易。
4) 任意 tx revert：必须记录 receipt + revert_reason（尽可能 decode）并把 intent 标记 failed。
5) 任意写操作：`operator_actions` 必须有记录（成功/失败都要）。
