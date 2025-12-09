# Phoenix V3 任务分配清单 (TODO List)

本文档基于《Phoenix V3：跨域流动性指挥官》将项目开发任务按角色进行分解。

## 1. 系统架构师 (System Architect)

- [x] **Phase 0:** 定义项目整体 Go 目录结构 (cmd, internal, pkg 等) 和模块边界。
- [x] **Phase 1:** 定义 `Feed` 接口 (用于标准化不同 CEX 数据) 和 `DexState` 接口。
- [x] **Phase 3:** 设计 `Intent` (交易意图) 结构体字段和 `Strategy` 模块的输入输出接口。
- [x] **Phase 4:** 定义 `Gateway` (网关) 和 `Adapter` (适配器) 的通用接口，确保支持多链/多 DEX。
- [x] **Phase 6:** 制定 `PoolGuard` (防蜜獾) 的检测标准和 `Risk` (风控) 的熔断规则。

## 2. 后端开发工程师 (Backend Developer)

### 基础架构 & I/O (Phase 0 - Phase 1)
- [x] **Phase 0:** 初始化 Go module，创建 `cmd/bot/main.go` 入口文件。
- [x] **Phase 0:** 实现全局 `config` 加载模块 (Viper/YAML) 和日志系统 (Zap/Logrus)。
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
- [x] **Phase 5 (DryRun):** 在 `strategy` 和 `gateway` 中实现 `dry_run` 模式，只记录不发交易。
- [x] **Phase 5 (Storage):** 实现 `storage` 模块 (SQLite)，用于持久化 Intent 记录和模拟交易结果。

### 安全 & 风控 (Phase 6)
- [x] **Phase 6 (PoolGuard):** 实现 `poolguard` 模块，检查 ERC20 合规性、蜜獾币特征。
- [x] **Phase 6 (Risk):** 实现 `risk` 模块，校验 Gas 上限、最大回撤、连续失败次数。

## 3. 前端开发工程师 (Frontend Developer)

- [x] **Phase 2:** 初始化 Web 项目 (React/Vue)，搭建 Dashboard 基础骨架。
- [x] **Phase 2:** 对接后端接口，实时显示 CEX 价格、链上价格、ASMM 建议区间。
- [x] **Phase 3:** 开发“意图队列 (Intent Queue)”组件，显示机器人待执行的操作。
- [ ] **Phase 7:** 开发 PnL (盈亏) 监控面板，显示实时收益和资产曲线。
- [ ] **Phase 8:** 实现控制台功能：暂停/恢复策略、一键清仓、紧急撤单按钮。

## 4. UI设计师 (UI Designer)

- [x] **Pre-Phase 2:** 设计 Dashboard 整体视觉风格 (Dark Mode, Tech/DeFi 风格)。
- [x] **Pre-Phase 3:** 设计“系统状态”和“意图队列”的可视化交互稿。
- [ ] **Pre-Phase 7:** 设计 PnL 收益曲线、资产分布饼图、交易历史列表的 UI 规范。

## 5. Review工程师 (Code Reviewer)

- [x] **Ongoing:** 建立代码审查规范 (检查错误处理、Context 传递、配置分离等)。
- [ ] **Phase 4 Audit:** 重点审查 `chain/gateway` 和 `adapter` 模块，确保私钥安全和 Nonce 管理无误。
- [ ] **Phase 6 Audit:** 重点审查 `risk` 模块的熔断逻辑，确保风控规则在极端行情下也能生效。

## 6. 测试工程师 (QA Engineer)

- [x] **Phase 1:** 编写集成测试，验证 CEX WebSocket 连接稳定性和 RPC 读取准确性。
- [x] **Phase 2:** 编写 `engine` 单元测试，输入特定价格/波动率，验证 ASMM 输出是否符合预期。
- [x] **Phase 5:** 运行“影子模式 (Paper Trading)” 24小时，分析日志，验证策略逻辑闭环。
- [ ] **Phase 7:** 在 Goerli/Sepolia 测试网或主网小资金环境进行全流程实盘测试。

## 7. 运维工程师 (DevOps Engineer)

- [x] **Phase 0:** 编写 `Dockerfile` 和 `docker-compose.yml`。
- [ ] **Phase 0:** 配置基础 CI 流水线 (GitHub Actions/GitLab CI)，提交代码自动 lint/test。
- [ ] **Phase 1:** 筛选并配置高可用的 RPC 节点 (Alchemy/Infura/QuickNode)。
- [ ] **Phase 7:** 部署 Prometheus + Grafana 监控栈。
- [ ] **Phase 7:** 配置告警规则 (如：余额不足、RPC 延迟过高、服务崩溃)，并接入 Telegram/PagerDuty。
