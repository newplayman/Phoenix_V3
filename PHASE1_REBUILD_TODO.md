# Phoenix V3 · Phase 1 Rebuild TODO

面向目标：把 `PHASE1_LP_WITH_REBALANCER_SPEC.md` 与审计意见落地，实现 **测试链→主网小额实盘** 的可验证闭环。所有任务默认作用范围为 `Phoenix_V3` 目录。

## 0. 总体交付要求
- **环境矩阵**：Dev（本地/模拟链）→ Testnet（Sepolia/Base）→ Mainnet（小额试跑）。
- **流水线**：`lint → unit → integration (dry-run) → testnet rehearsal → release`.
- **共同 Review 点**：
  1. Context 传递与取消是否完整。
  2. 配置热更新后是否能安全回滚。
  3. 所有链上参数（tick、金额、滑点、gas）是否有日志与存证。
  4. Risk/PoolGuard 结果是否写入事件流和 storage。
- **共同测试要求**：
  - 单元测试覆盖：数学库、策略计算、配置解析。
  - 集成测试：`cmd/replay` 回放、`bot --cleanup`、`bot --dry-run`。
  - 测试链验证：swap + mint + collect 全流程。
  - 发布前 checklist：见每模块的发布步骤。

---

## 1. 配置与事件架构
### 技术规范
- `configs/config.yaml` 扩展：
  - `wallet.min_idle_pct`
  - `risk.max_utilization_pct`
  - `pools[].max_cap_pct`
  - `pools[].stable_tokens`（用于 Rebalancer）
  - `events.replay_retention`, `events.acks_required`
- `config.Manager` 支持 schema 版本校验与增量 diff 推送。
- 事件流：默认 Redis Streams，Memory 仅在 dev 使用；定义 `TopicStrategy`, `TopicRisk`, `TopicIntentExec`, `TopicAudit`.
### 运作逻辑
1. Manager 热重载 → 推送 diff → 各模块按需刷新。
2. 所有模块通过 `events.Stream` 交互，禁止直接调用。
### 关联性
- Strategy/Risk/Rebalancer 依赖 pool 配置；Gateway/Storage 订阅事件。
### Review 要点
- 配置新增字段均有默认值和验证。
- Redis driver 支持断线重连、ACK、回放光标。
### 测试要求
- `cmd/configcheck` 校验新 schema。
- Redis 集成测试：堆积、重放、ACK 超时。
### 发布步骤
1. 迁移配置文件；在 dev/stg 环境部署 Redis。
2. 通过 `cmd/configcheck` + `cmd/replay` 在 testnet 环境验证事件重放。

**状态**：✅ 已实现多池配置字段、钱包/风险参数、内存热更新同步与 Token 价格缓存（2025-01-30）。

---

## 2. Feed & DexState
### 技术规范
- Feed 支持多符号订阅（Binance、OKX、CoinGecko）。
- DexState 提供多池监听，输出 `PoolState{pool_id,sqrt_price_x96,liquidity,tick,updated_at}`。
### 运作逻辑
1. 每个池独立 goroutine 拉取 slot0/流动性。
2. Feed 输出统一 `PriceSnapshot`，由 Aggregator 合并，推送到 `TopicTicker`.
### 关联性
- Strategy/Rebalancer 订阅价格与池状态。
- Monitor 读 feed metrics。
### Review 要点
- 所有 RPC/WS 调用使用 context + retry + backoff。
- 价格与链上 tick 需要 decimal 对齐。
### 测试要求
- 单元：tick↔price 转换、Aggregator 权重。
- 集成：使用模拟池（go-ethereum dev node）验证多池订阅。
### 发布步骤
1. 接入新的 RPC/WS url 列表。
2. Testnet 环境验证多池数据稳定 ≥24 小时。

**状态**：✅ 已实现多池 slot0 监听与 Token 价格聚合（2025-01-30）。

---

## 3. Engine & Strategy
### 技术规范
- Engine 模块读取 `EngineInput{pool_id,cex_price,dex_price,volatility,position}`，输出 target range/size。
- BasicStrategy 支持多池、风险模式、动态 `TargetNotionalPct`。
- Strategy 输出 Intent 带 `pool_id`, `chain_id`, `token0/1`, `lower/upper_tick`, `target_notional_pct`.
### 运作逻辑
1. Engine 计算 fair price & spread。
2. Strategy 对每个池比较当前持仓与目标，根据风险档位决定 enqueue。
### 关联性
- IntentQueue 支持按池/链优先级；Risk 管理节流。
### Review 要点
- 多池循环不能共享状态；Intent struct 不得复用 pointer。
- 所有 Tick align 到 `fee` 对应 spacing。
### 测试要求
- 单元：Engine math，Intent 生成逻辑。
- 集成：模拟当前持仓变化，验证 Intent 去重/节流。
### 发布步骤
1. 在 dev 调整策略参数并跑 `cmd/replay`。
2. Testnet 运行 12h 监测 Intent 队列长度。

**当前进度**：✅ 已完成多池策略装配、TickSpacing 和 TargetNotionalPct 初始化；✅ 风险自适应策略与“阶梯利用率”已落地为 RiskMode→StrategyProfile 动态调参（TargetNotionalPct/MinSpreadTicks/EngineRiskFactor）+ 全局 `risk.max_utilization_pct` clamp，确保 `caution/frozen` 及时收缩/停止出单（2025-12-12）。

---

## 4. Rebalancer
### 技术规范
- 使用定点算法读取实时 `sqrtPriceX96`，调用 Uniswap SDK 计算所需 `amount0/1`.
- 输入：`RebalanceInput{intent, wallet_balances, prices, pool_config, risk_limits}`。
- 输出：`RebalancePlan{swaps[], final_lp, utilized_pct, reason}`。
- Swap path 支持多跳，使用 Quoter 估算 `MinAmountOut`，默认滑点 <=1%。
### 运作逻辑
1. 计算总权益与目标预算。
2. 检查 `wallet.min_idle_pct`、`pools[].max_cap_pct`。
3. 规划 Swap 顺序（先使用同池另一 token，再用 stable）。
4. 更新 Intent metadata 并发送 swap Intent。
### 关联性
- 需与 Risk.Manager 协同控制 swap 次数和额度。
- Adapter/Gateway 根据 plan 构造真实交易。
### Review 要点
- 禁止使用 float64 处理金额；至少使用 big.Int 或 fixed-point。
- 每次 swap 记录预期/实际金额，写入 storage。
### 测试要求
- 单元：tick math、liquidity calc、MinAmountOut 估算。
- 集成：在 Sepolia 搭建测试池跑完整流程（swap→mint→collect）。
### 发布步骤
1. 本地 dry-run 通过后，部署 testnet 观察 swap 成功率。
2. 小额主网测试前，确保 Risk 配置可控（max util, min idle）。

**当前进度**：✅ 已实现 Quoter/本地双路径 `MinAmountOut` 滑点保护、Swap 交易类型化、用回执等待替换固定 sleep、swap 前后余额/滑点落入 `TradeRecord.swap_details`（2025-12-12）；✅ Rebalancer 支持 stable→poolToken→目标 token 的多跳 path，并由 Router 构造 `exactInput` calldata（2025-12-12）。

---

## 5. Gateway & Adapter
### 技术规范
- Gateway 支持 per-chain 实例、nonce 管理、retry/backoff。
- `EnsureAllowance` 改为按需授权 exact amount，并可配置 `approval_multiplier`。
- Adapter 需输出 Mint/Burn/Collect/DecreaseLiquidity 的 calldata，并能解析回执。
### 运作逻辑
1. IntentExecutor 取 Intent → 调 Rebalancer → 检查 Risk → 构造 calldata → Gateway.Send。
2. Gateway 收到 receipt 后写 storage，并发送 `TopicIntentExec`.
### 关联性
- Storage 需要 `gas_used`, `effective_gas_price`, `fee_token`.
- Monitor 展示 pending/confirmed 数量。
### Review 要点
- 私钥只从 env/Vault 读取；敏感日志脱敏。
- Nonce 冲突需自动重新同步。
### 测试要求
- 单元：calldata 构造、nonce 同步。
- 集成：`bot --cleanup` 在 testnet 真实烧写，确保所有 tx 成功。
### 发布步骤
1. dry-run 模式验证 intent → calldata。
2. testnet 执行实际交易，记录 txhash，人工对比链上事件。

**状态**：✅ `EnsureAllowance` 改为按需授权 exact amount，并支持 `gateway.approval_multiplier` 配置（默认 1.05）（2025-12-12）；✅ 多链 Gateway nonce/gas retry/backoff 已落地（2025-12-12）。

---

## 6. Risk Manager & PoolGuard
### 技术规范
- RiskManager 增加 `RecordSwap`, `RecordPnL`, `RecordDrawdown`，从 storage 回写。
- PoolGuard 接入外部 API（GoPlus/Honeypot）+ 本地白名单；输出 `PoolCheckResult` 包含理由、score。
- Intent 执行前必须通过：
  - Balance Guard
  - PoolGuard
  - RiskManager.CanProceed + CanSwap
### 运作逻辑
1. 每笔交易登记 gas、swap 额度；swap 级别 PnL 估算写入 `TradeRecord.PnL` 与 `swap_details.pnl_usd`，为 Drawdown 统计提供初步数据（2025-12-12）。
2. 当 `risk.MaxDrawdown` 或 `MaxFails` 触发时进入 `ModeFrozen`，发事件通知。
### 关联性
- Monitor/Frontend 可切换 RiskMode。
- Storage 保存 Risk 日志以便审计。
### Review 要点
- Risk state 更新必须加锁；记录函数有上下文。
- PoolGuard 失败时提供详细 reason，便于调试。
### 测试要求
- 单元：Risk 状态转换、PoolGuard mock 响应。
- 集成：模拟连续失败/亏损，验证熔断与恢复。
### 发布步骤
1. Dry-run 环境对风险参数进行回归。
2. testnet 通过脚本故意触发 `MaxFails`，验证熔断通知。

**状态**：✅ PoolGuard 支持本地 allow/blacklist + 可选 GoPlus/Honeypot 远程 provider（配置关闭时不触网），并新增最基础链上 `totalSupply>0` 体检与 score 字段（2025-12-12）。

---

## 7. Storage & Monitoring
### 技术规范
- Storage 迁移：`trade_records` 增加 `pool_id`, `token0_amt`, `token1_amt`, `gas_used`, `gas_token`, `fee_usd`, `swap_details (JSONB)`, `risk_snapshot`.
- 提供分页查询、PnL 聚合、风险报表接口。
- Monitor：
  - `/healthz`, `/metrics`, `/status`.
  - 本地 + Prometheus 兼容。
### 运作逻辑
1. IntentExecutor 完成交易后写记录；ReceiptWatcher更新状态/费用。
2. Monitor 订阅事件/DB，生成 charts。
### 关联性
- Web 前端展示 storage 数据；RiskManager 读取历史 PnL。
### Review 要点
- 所有 DB 操作使用 context + timeout。
- SQLite/Postgres 兼容；迁移脚本具备回滚能力。
### 测试要求
- DAO 单元测试、迁移测试。
- 集成：运行 `cmd/bot` + `cmd/replay`，确保记录落盘。
### 发布步骤
1. 执行 DB migration。
2. 检查监控面板是否实时展示 feed/risk/gateway 状态。

**状态**：✅ Monitor 增加 `/status` 与 Prometheus `/metrics`，并注入 intents/risk/pools 摘要指标（2025-12-12）。

---

## 8. Web Dashboard
### 技术规范
- React/Vite 前端改造：
  - 展示 Feed、Pool 状态、Intent 队列、交易记录、PnL 曲线。
  - 控制面板：暂停/恢复、紧急撤摊、风险模式切换。
- API：`/api/status`, `/api/intents`, `/api/trades`, `/api/risk`.
### 运作逻辑
1. 定时拉取后端 API，或使用 WebSocket 推送。
2. 提供 testnet/mainnet 切换视图。
### 关联性
- Monitor/Storage 提供数据源。
- Risk/PoolGuard 的结果需要可视化。
### Review 要点
- 不泄露私钥/敏感参数；界面有错误提示。
- 控制操作需二次确认。
### 测试要求
- UI 单元测试（组件渲染）。
- E2E：使用 Cypress/Playwright 模拟操作。
### 发布步骤
1. 打包 `web`，部署到 dev/testnet，连后端 API。
2. 灰度发布给运营，收集反馈后上线。

**状态**：✅ Web Dashboard 增加暂停/恢复控制按钮（调用 `/api/control/pause|resume`），后端返回 `/api/status.control.paused`（2025-12-12）。

**状态**：✅ Web Dashboard 增加紧急撤摊（Cleanup）按钮（调用 `/api/control/cleanup`），后端复用 `bot --cleanup` 逻辑异步执行，并暴露 `/api/status.control.cleanup_in_progress`（2025-12-12）。

**状态**：✅ Web Dashboard 增加风险模式切换（调用 `/api/control/riskmode`）与手动 Rebalance Now（调用 `/api/control/rebalance` enqueue 手动意图）（2025-12-12）。

---

## 9. 测试与发布流程
1. **单元测试**：`go test ./...` + `npm test`.
2. **集成**：
   - `comprehensive_test.sh`：pull feeds, run strategy, ensure no panic。
   - `check_position_validity.sh`、`quick_check.sh`：验证池子状态。
3. **Testnet Dress Rehearsal**：
   - 使用真实钱包但小额资金，在 Sepolia/Base 上跑 24h。
   - 验证 swap/mint/burn/collect 全链路日志与 storage。
4. **Mainnet Pilot**：
   - 调整 `risk` 配置（低 util、高 idle）。
   - 限定 allowlist 池子 & 资金上限。
   - 运行监控/报警（Telegram/Email）。
5. **回滚策略**：
   - `bot --cleanup` 清仓。
   - 恢复到 dry-run 模式，确认没有 pending tx。

以上 TODO 完成后，即可实现从测试链到实盘的渐进式验证，并满足 Phase 1 规范中“自动 Rebalancer + 风控熔断”的要求。
