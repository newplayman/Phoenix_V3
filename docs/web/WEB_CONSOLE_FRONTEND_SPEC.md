# Phoenix V3 控制台前端开发规范（Web Console）

面向对象：前端开发（交易控制台，不追求花哨，追求“防呆 + 可审计 + 可控执行”）。

目标：将 `./web` 改造为交易员/管理者控制台：能看（实时）、能管（受控写操作）、能追（审计/回放）、能算（归因/成本）。

状态（必须明确）：
- Feature status: `experimental`
- 当前实现：`web/` 仅提供 **read-only 最小面板**（mock + `/api/v1` Read APIs 轮询），不包含写操作 UI。
  - 已包含“执行追踪（只读）”：展示 intents 列表与 intent steps（tx hash 链接仅用于对照链上执行）。
- 本文档：描述控制台的目标形态；任何后续新增/修改 API 必须以 `docs/api/*` 为契约源并补测试。

---

## 0. 关键约束（必须遵守）

1) 前端不持私钥、不直连 RPC、不拼 calldata；所有写操作只能调用后端受控 API。
2) 默认只读；写操作需要显式“解锁”（输入 token + 二次确认）。
3) 所有操作必须先 preview（展示步骤/风险/预估 gas）再 execute。
4) 在 `experimental` 阶段，`web/` 不得调用任何 control-plane 端点（例如 `/api/v1/operations/*`、`/api/v1/pools/{pool_id}/pause|resume`）；仅允许 Read APIs。
   - 任何“写操作 UI/流程”只能在 `beta/stable` 阶段引入，并且必须满足：后端默认禁用 + 鉴权 + 审计 + kill-switch + 明确的人工确认。

补充（当前实现允许的只读观测）：
- intents：`GET /api/v1/intents`、`GET /api/v1/intents/{intent_id}`（展示 steps + tx_hash，用于对照链上执行）
- tx：`GET /api/v1/tx`
- audit：`GET /api/v1/audit`
- SSE：`GET /api/v1/stream`（仅展示连接状态与最近事件；不触发写操作）

---

## 1. 推荐技术栈（尽量贴近现有 React）

- React + TypeScript
- UI：`shadcn/ui + Tailwind`（或 Mantine/AntD 二选一，优先 shadcn）
- 数据请求：TanStack Query（轮询 + 缓存）+ SSE（实时事件）
- 表格：TanStack Table
- 图表：ECharts（tick/price/区间）或 TradingView Lightweight Charts（价格带展示更强）
- 校验：Zod（对后端响应做 runtime 校验，避免脏数据导致误判）

---

## 2. 信息架构（页面清单）

### 2.1 总览（/）

- KPI：净收益、gas、成功率、失败原因 TopN、风险模式、bot 心跳
- 全局告警：连续失败、超预算、RPC 超时率升高

### 2.2 池子工作台（/pools/:poolId）

必需模块：
- 价格/区间：DEX tick/price、CEX price（若有）、偏离 bps、当前价在区间内/外、距边界百分比
- 当前头寸：tokenId、tickLower/Upper、liquidity、未领取费用、已领取费用
- 再平衡历史：最近 N 次（原因、宽度、profile、耗 gas）
- 受控操作区（默认隐藏，解锁后显示）：
  - Pause/Resume
  - Force Rebalance（preview → execute）
  - Close Position
  - Collect Fees

### 2.3 执行与交易（/execution）

- intents 列表：状态机（generated/planned/running/succeeded/failed）
- 单条 intent 详情：plan steps 瀑布图 + tx hash + receipt + revert reason

### 2.4 风险与风控（/risk）

- 当前风险模式、阈值、触发原因
- 今日 gas 预算使用曲线
- 连续失败计数 + 熔断状态

### 2.5 审计（/audit）

- 操作日志：谁/何时/对哪个池/做了什么/原因/结果
- 支持导出（CSV/JSON）

### 2.6 回放（/replay）

- 选择池子与时间范围
- 展示 pool_snapshots + intents + 失败原因聚合
- （后端支持后）what-if：同段行情不同参数对比

---

## 3. 组件与交互规范（防呆重点）

### 3.1 环境标签

页面顶部固定显示：
- `ENV: arbitrum-sepolia/mainnet`
- `BOT: online/offline`
- `RISK MODE: normal/caution/frozen`

### 3.2 写操作解锁（`beta/stable` 预留）

注意：本节为目标形态（`beta/stable`）预留；`experimental` 的 `web/` **不得实现/触发任何写操作**。

解锁流程建议（`beta/stable`）：
- 顶栏 “Unlock Actions”
- 输入 Admin Token（本地保存到 `sessionStorage`，默认不持久化）
- 解锁有效期 10 分钟（倒计时），到期自动锁

### 3.3 Preview → Execute 两段式（`beta/stable` 预留）

注意：本节为目标形态（`beta/stable`）预留；`experimental` 的 `web/` 只能使用 Read APIs（不调用以下端点）。

任何写操作按钮点击后（`beta/stable`）：
1) 调用 `POST /api/v1/operations/preview`
2) 打开 `PreviewModal`：
   - 显示步骤序列（close/collect/burn/swap/mint/approve）
   - 显示风险提示（如“可能短时 liquidity=0”“可能触发 cooldown”）
   - 显示预估 gas（若后端提供）
3) 用户必须输入：
   - `pool_id`（或再次选择）
   - `CONFIRM`
   - `reason`（必填）
4) 调用 `POST /api/v1/operations/execute`

### 3.4 实时追踪

- 执行后跳转到 Operation/Intent 详情页，使用 SSE 订阅更新状态。
- 状态更新必须可回放：UI 不仅显示“成功/失败”，还要显示每一步的 txHash 和 mined 状态。

---

## 4. 数据展示单位与约定（避免误读）

- 价格：统一显示 `stable per WETH`（例如 TUSD/WETH），同时标注 token0/token1 排序
- tick：显示原始 tick，并提供换算到价格的 tooltip
- gas：ETH + USD（若后端能给 USD，不能则先只给 ETH）
- 数量：同时给 raw（wei）与 human（带 decimals）至少在详情页提供

---

## 5. 前端验收用例

1) 无 token：只能看不能点；所有写按钮隐藏或 disabled。
2) preview 必须成功才能 execute；preview 返回的 plan 必须完整展示。
3) execute 后能在 5 秒内看到状态变为 running（SSE 或轮询）。
4) 任一 tx revert：UI 能显示失败在哪一步、revert reason、gas 损耗。
5) 审计页能查到每次操作的 reason 与结果。
