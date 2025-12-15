# Phoenix V3 任务分配清单 (TODO List)

本文档基于《Phoenix V3：跨域流动性指挥官》将项目开发任务按角色进行分解。

## 1. 系统架构师 (System Architect)

- [x] **Phase 0:** 定义项目整体 Go 目录结构 (cmd, internal, pkg 等) 和模块边界。
- [x] **Phase 1:** 定义 `Feed` 接口 (用于标准化不同 CEX 数据) 和 `DexState` 接口。
- [x] **Phase 3:** 设计 `Intent` (交易意图) 结构体字段和 `Strategy` 模块的输入输出接口。
- [x] **Phase 4:** 定义 `Gateway` (网关) 和 `Adapter` (适配器) 的通用接口，确保支持多链/多 DEX。
- [x] **Phase 6:** 制定 `PoolGuard` (防蜜獾) 的检测标准和 `Risk` (风控) 的熔断规则。
- [x] **Phase 6+:** 建立 `internal/contracts/` 数据契约骨架，让 Feed/DexState/Engine/Strategy/Gateway 使用统一结构。
- [x] **Phase 6+:** 实现 `internal/events` 内存事件流 (MemoryStream)，串联 feed/dexstate/strategy。
- [x] **Phase 6+:** 设计事件流（Redis Streams/NATS）规范及持久化方案，确保跨模块通信可重放（`internal/events/REDIS_STREAMS_SPEC.md`，2025-12-12）。
- [x] **Phase 6+:** 在配置中加入 `schema_version/strategy_version` 字段并在 Bot 中读取。
- [x] **Phase 6+:** 实现本地 `config.Manager`（fsnotify 热重载）与 `ValidateConfig`，支持运行时订阅配置变更。
- [ ] **Phase 6+:** 定义远程配置中心/策略版本治理方案（含热更新与 Schema 校验流程）。
- [ ] **Phase 6+:** 设计“实时缓存 + 事务库 + 历史仓库”的三层存储与审计模型。
- [x] **Phase 6+:** 定义 `RiskMode` ↔ `StrategyProfile` 映射与 Policy Engine 规则格式（`internal/config/config.go` + `internal/strategy/policy.go`，2025-12-12）。
- [x] **Phase 6+:** 设计执行链路一致性方案（deterministic replay + 链上回读校验）（TradeRecord 记录 nonce/from/to 并在 receipt watcher 回读校验，2025-12-12）。

## 2. 后端开发工程师 (Backend Developer)

### 基础架构 & I/O (Phase 0 - Phase 1)
- [x] **Phase 0:** 初始化 Go module，创建 `cmd/bot/main.go` 入口文件。
- [x] **Phase 0:** 实现全局 `config` 加载模块 (Viper/YAML) 和日志系统 (Zap/Logrus)。
- [x] **Phase 6+:** 将 `schema_version/strategy_version` 字段接入配置加载及策略模块。
- [x] **Phase 1 (Feed):** 实现 `feed` 模块，连接 Binance/OKX WebSocket，订阅 Ticker 数据。
- [x] **Phase 1 (DexState):** 实现 `dexstate` 模块，通过 RPC 读取 Uniswap V3 Pool 的 slot0 和流动性。
- [x] **Phase 1 (Monitor):** 实现基础 HTTP 服务 `/healthz` 接口，返回系统存活状态。

### 核心逻辑 & 策略 (Phase 2 - Phase 3)
- [x] **Phase 2 (Engine):** 实现 `engine` 模块 (ASMM 算法)，根据输入计算 target tick 和 delta。
- [x] **Phase 3 (Strategy):** 实现 `strategy` 模块，根据 Engine 输出和当前持仓生成 `Intent`。
- [x] **Phase 3 (Intent):** 实现 `intent` 模块的优先级队列 (Priority Queue) 和调度逻辑。

### 链上交互 & 存储 (Phase 4 - Phase 5)
- [x] **Phase 4 (Adapter):** 使用 `abigen` 生成 Uniswap V3 Go binding，封装 Mint/Burn/Collect 操作。
- [x] **Phase 4 (Gateway):** 实现 `chain/gateway` 模块，管理 Nonce，发送交易并追踪状态 (Pending/Mined/Failed)。
- [x] **Phase 7+:** EthGateway 增加 nonce 同步重试与 gas price 乘子/递增策略，失败自动 backoff 重发（2025-12-12）。
- [x] **Phase 5 (DryRun):** 在 `strategy` 和 `gateway` 中实现 `dry_run` 模式，只记录不发交易。
- [x] **Phase 5 (Storage):** 实现 `storage` 模块 (SQLite)，用于持久化 Intent 记录和模拟交易结果。

### 安全 & 风控 (Phase 6)
- [x] **Phase 6 (PoolGuard):** 实现 `poolguard` 模块，检查 ERC20 合规性、蜜獾币特征。
- [x] **Phase 6 (Risk):** 实现 `risk` 模块，校验 Gas 上限、最大回撤、连续失败次数。
- [x] **Phase 6+:** 落地 `internal/contracts` 包并重构 feed/dexstate/engine/strategy/intent/gateway 的共用类型。
- [x] **Phase 6+:** 接入 `internal/events` 内存事件流，策略与 API 通过订阅器消费行情/意图事件。
- [x] **Phase 6+:** 实现 Redis 事件流驱动（配置化 `events.driver=redis`），支持跨进程消费。
- [x] **Phase 6+:** 为 storage 增加 Supabase Postgres 自动检测 (`SUPABASE_DB_URL`)，本地回退 SQLite。
- [x] **Phase 6+:** 实现 `config.Manager` 文件热重载 & 订阅接口，运行时动态刷新策略/风控参数。
- [x] **Phase 6+:** 完成多源行情聚合（Binance + CoinGecko + Aggregator + Feed Health Monitor）。
- [x] **Phase 6+:** 实现事件流中间件与消费 SDK（Memory/Redis 实现 + 统一接口），替换直接函数调用链路。
- [x] **Phase 6+:** 提供事件回放 CLI (`cmd/replay`)，结合 Redis Streams 做断点重放与回归调试。
- [x] **Phase 6+:** 提供 `cmd/configcheck` 工具，在 CI 中执行配置 Schema 校验。
- [x] **Phase 6+:** 完成 `startPoolWatcher` + `startIntentExecutor`，实现池状态事件与异步调度器雏形。
- [x] **Phase 6+:** 扩展 storage `TradeRecord` 字段（链 ID、策略版本、风险模式、Notional/Gas），并在执行器写入审计信息。
- [x] **Phase 6+:** 接入 Uniswap V3 Adapter/Gateway 流程，基于 Intent Metadata 生成真实 calldata 并发送交易（DryRun 环境保留模拟 Execution）。
- [x] **Phase 6+:** 修复 Uniswap V3 Adapter ABI（fee/tick → `uint24/int24`），IntentExecutor 在 RealRun 可正确构造 calldata。
- [x] **Phase 6+:** 兼容 Supabase/pgBouncer：AutoMigrate 容错 + `prefer_simple_protocol=true`，消除 prepared statement 冲突。
- [x] **Phase 7:** IntentExecutor 关闭 dry_run，真实发送 mint 交易并写入 Supabase（tx hash/nonce 透明可追踪）。
- [x] **Phase 6+:** 扩展事件回放/回测 SDK，支持多 topic 批量回放与策略复盘（`cmd/replay -stream a,b,c`，2025-12-12）。
- [ ] **Phase 6+:** 实现远程配置中心客户端 + 配置审批流程，所有节点读取统一版本号。
- [ ] **Phase 6+:** 重构 storage：DAO 接口、迁移工具、审计字段、DryRun/RealRun 共用写路径。
- [ ] **Phase 6+:** 落地 Risk Policy Engine 与 `StrategyProfile` 动态调参机制。
- [x] **Phase 6+:** Gateway 增加 deterministic replay、链上回读校验与状态机化的 Nonce/Gas 管理（ReceiptResult 携带 nonce/from/to，storage 回读校验，2025-12-12）。
- [x] **Phase 7:** 引入余额/仓位风控（ERC20 余额校验），资产不足时自动熔断 Intent，避免浪费 gas。
- [x] **Phase 7:** 修复 UniV3 tick/价格反向映射问题，让 engine/dexstate 在 fee=500 的池子上输出与链上价格一致的目标区间。
- [x] **Phase 7:** Gateway 增加 receipt watcher，将 Supabase `trade_records.status` 从 `pending` 更新为 `success/failed` 并补写 gas 统计。
- [ ] **Phase 7+:** 将回执中的 `gas_used/gas_price` 映射到 storage，计算 `gas_cost_usd` 并在 Dashboard/Supabase 展示。
- [x] **Phase 7+:** 为 Swap 引入 Quoter/本地估值设置 `MinAmountOut`，并记录 swap 前后余额与滑点到 storage（2025-12-12）。
- [x] **Phase 7+:** Storage 扩展 gas 明细与 `/api/trades` 查询接口，Dashboard 展示最近交易与 gas/swap 状态（2025-12-12）。
- [x] **Phase 7+:** 增加 `/api/risk` 风险快照接口，并在 Dashboard 展示 RiskMode、日 gas/Swap 使用率（2025-12-12）。
- [x] **Phase 7+:** Dashboard 展开 `swap_details` 显示滑点/实际输出；新增 `/api/intents/detail` 返回意图明细（2025-12-12）。
- [x] **Phase 7+:** 新增 `/api/pools` 与 `/api/status.pools` 输出池运行快照，Dashboard 展示真实 DEX 价格/tick/流动性（2025-12-12）。
- [x] **Phase 7+:** 新增 `/api/pnl` 日度 PnL 聚合接口与 Dashboard PnL 表格展示（2025-12-12）。
- [x] **Phase 7+:** 执行链路增加 swap PnL 估算并写入 `TradeRecord.PnL`，为 Risk Drawdown 提供数据（2025-12-12）。
- [x] **Phase 7+:** Collect/Withdraw 意图执行前后对比钱包余额，估算 realized PnL 并写入 `TradeRecord.PnL`（2025-12-12）。
- [x] **Phase 7+:** 引入 Pool 级 cost basis（Mint notional 记账 + Withdraw 清算），估算完整 realized PnL（2025-12-12）。
- [x] **Phase 7+:** cost basis 改为基于 Mint/Rebalance 真实钱包 deltaUSD 记账，提升 PnL 精度（2025-12-12）。
- [x] **Phase 7+:** RiskManager 定时从 TradeRecord 聚合 PnL，更新 Drawdown 并自动熔断（2025-12-12）。

## 3. 前端开发工程师 (Frontend Developer)

- [x] **Phase 2:** 初始化 Web 项目 (React/Vue)，搭建 Dashboard 基础骨架。
- [x] **Phase 2:** 对接后端接口，实时显示 CEX 价格、链上价格、ASMM 建议区间。
- [x] **Phase 3:** 开发“意图队列 (Intent Queue)”组件，显示机器人待执行的操作。
- [ ] **Phase 7:** 开发 PnL (盈亏) 监控面板，显示实时收益和资产曲线。
- [ ] **Phase 8:** 实现控制台功能：暂停/恢复策略、一键清仓、紧急撤单按钮。
- [ ] **Phase 6+:** 对接新监控指标与 Risk Mode/Fault 事件，提供一键熔断与配置版本展示。
- [x] **Phase 6+:** PoolGuard 增加 allow/blacklist、缓存与外部 Provider 接口骨架，并在 API/Dashboard 暴露风险结果（2025-12-12）。
- [x] **Phase 7+:** Bot 增加 `--dry-run` CLI 覆盖开关，便于无远程 PoolGuard 时继续安全演练（2025-12-12）。

## 4. UI设计师 (UI Designer)

- [x] **Pre-Phase 2:** 设计 Dashboard 整体视觉风格 (Dark Mode, Tech/DeFi 风格)。
- [x] **Pre-Phase 3:** 设计“系统状态”和“意图队列”的可视化交互稿。
- [ ] **Pre-Phase 7:** 设计 PnL 收益曲线、资产分布饼图、交易历史列表的 UI 规范。
- [ ] **Pre-Phase 6+:** 设计配置版本提示、RiskMode 切换、事件回放状态的 UI 组件。

## 5. Review工程师 (Code Reviewer)

- [x] **Ongoing:** 建立代码审查规范 (检查错误处理、Context 传递、配置分离等)。
- [ ] **Phase 4 Audit:** 重点审查 `chain/gateway` 和 `adapter` 模块，确保私钥安全和 Nonce 管理无误。
- [ ] **Phase 6 Audit:** 重点审查 `risk` 模块的熔断逻辑，确保风控规则在极端行情下也能生效。
- [ ] **Phase 6+ Audit:** 审查 `internal/contracts` 数据契约与事件总线实现，避免破坏兼容。
- [ ] **Phase 6+ Audit:** 审查 deterministic replay + 链上回读逻辑，确保审计链条完整。

## 6. 测试工程师 (QA Engineer)

- [x] **Phase 1:** 编写集成测试，验证 CEX WebSocket 连接稳定性和 RPC 读取准确性。
- [x] **Phase 2:** 编写 `engine` 单元测试，输入特定价格/波动率，验证 ASMM 输出是否符合预期。
- [x] **Phase 5:** 运行“影子模式 (Paper Trading)” 24小时，分析日志，验证策略逻辑闭环。
- [x] **Phase 7 (Dry-Run)：** 基于 Sepolia RPC 与 WETH/USDC 池完成 dry-run，验证 Feed → Strategy → Intent → Gateway → Storage。
- [x] **Phase 7 (Real-Run)：** 在 Sepolia 测试网关闭 dry_run，发送真实 Intent 并在 Supabase 记录 tx_hash。
- [ ] **Phase 6+:** 构建事件流/数据契约的集成测试与断点续跑验证。
- [ ] **Phase 6+:** 构建 deterministic replay 与链上回读的端到端测试。
- [ ] **Phase 6+:** 针对 Redis 事件流编写消费/堆积/回放测试，覆盖消费者组与容错。
- [ ] **Phase 7+:** 以 TUSD/WETH 0.05% 新池为基准重跑 dry-run/real-run 闭环，核对 Supabase `trade_records` 与链上交易状态。

## 7. 运维工程师 (DevOps Engineer)

- [x] **Phase 0:** 编写 `Dockerfile` 和 `docker-compose.yml`。
- [ ] **Phase 0:** 配置基础 CI 流水线 (GitHub Actions/GitLab CI)，提交代码自动 lint/test。
- [x] **Phase 1:** 筛选并配置高可用的 RPC 节点（当前接入 https://ethereum-sepolia.publicnode.com，可替换为 Alchemy/Infura）。
- [x] **Phase 7:** 自部署 Sepolia SwapHelper（Uniswap V3 callback 合约）或可用 router，打通 WETH→USDC swap。
- [ ] **Phase 7:** 对接多 Token Faucet（QuickNode 等）并记录兼容的 USDC/其他稳定币地址，更新配置以支持 faucet 版本 LP。
- [x] **Phase 7:** 自建 TUSD/WETH 0.05% 池（0x1E80b0b6d12Ecf2CDD08bC9c66f2fD594394331d），注入 1,000 TUSD + 0.3 WETH 并同步 configs/DEPLOY/策略文档。
- [ ] **Phase 7:** 部署 Prometheus + Grafana 监控栈。
- [ ] **Phase 7:** 配置告警规则 (如：余额不足、RPC 延迟过高、服务崩溃)，并接入 Telegram/PagerDuty。
- [ ] **Phase 6+:** 上线配置中心/热更新服务与审批流程，管理策略版本。
- [ ] **Phase 6+:** 部署 Vault/HSM 或外部签名服务，替换本地私钥。
- [ ] **Phase 6+:** 为事件流、审计数据库、对象存储建立备份与灾备演练流程。
- [ ] **Phase 6+:** 统一管理 Supabase 实例/凭证（`SUPABASE_DB_URL`），提供密钥轮换与权限隔离。
- [x] **Phase 6+:** 打通 Supabase Postgres（示例：`SUPABASE_DB_URL=postgres://postgres.<tenant_id>:<password>@<host>:6543/postgres`；tenant/password 由你自己的 Supavisor/Supabase 环境提供）。
- [ ] **Phase 6+:** 部署与监控 Redis 事件流（ACL、持久化、连接数告警）并建立灾备方案。
