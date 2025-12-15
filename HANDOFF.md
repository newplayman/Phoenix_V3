## Phoenix V3 开发交接（截至 2025-01-30）

### 已完成的核心改造
1. **配置 & 多池基础**
   - `configs/config.yaml`、`internal/config/config.go` 新增 `wallet.min_idle_pct`、`risk.max_utilization_pct`、`pools[].max_cap_pct/stable_tokens` 等字段，校验逻辑补充默认值。
   - `cmd/bot/main.go` 支持多池运行态（`poolRuntime`）、独立 mint guard、token 价格缓存及热更新。

2. **Feed & DexState**
   - DEX watcher 现按池/链分别启动，事件携带 `pool_id` 与 `SqrtPriceX96`。
   - 价格聚合器支持多源并写入 `tokenPriceStore`。

3. **Strategy & Intent**
   - `internal/strategy/basic.go` 加入 `TargetNotionalPct/MaxCapPct/TickSpacing`，Intent Metadata 带 `max_cap_pct`。
   - `cmd/bot.main` 策略构建流程按池生成 `BasicStrategy`，多池轮询执行。

4. **Rebalancer**
   - 接入链上 `sqrtPriceX96`、稳定币白名单、钱包留存约束；SwapAction 自带美元估值、token decimals。
   - RebalanceInput 包含 `PoolStateSnapshot`，Swap 前后调用 `riskMgr.CanSwap/RecordSwap`。

5. **Gateway & Risk**
   - 重新设计 `EthGateway`：多链实例、`EnsureAllowance` 内部发送 approve 交易，回执结构包含 `EffectiveGasPrice`。
   - `cmd/bot.main` 支持多链 Gateway 选择；`startReceiptWatcher` 将 gas 费用写入 storage 并调用 `riskMgr.RecordGas`。
   - RiskManager 已接入 swap/gas 实际数据，`go test ./...` 全部通过。

6. **文档 & TODO**
   - `PHASE1_REBUILD_TODO.md` 中的配置、Feed、多池策略、Rebalancer 进度已更新。

### 待完成/后续重点
1. **Rebalancer & Swap**
   - 集成 Uniswap Quoter 估算 `MinAmountOut`；替换 `time.Sleep` 为基于 receipt/event 的回调。
   - 将 swap 前后实际余额写入 storage，便于监控滑点。

2. **Gateway/Nonce 管理**
   - 对 `EthGateway` 增加 nonce 重试、gas price 策略；支持外部签名/Vault。
   - Adapter 需支持多 PositionManager/chain，后续可按池构造 calldata。

3. **Storage & Dashboard**
   - `TradeRecord` 的 gas/PnL 字段已有入口，但前端/监控尚未显示。
   - 需补充 API、Web Dashboard（PnL 曲线、意图列表、风险状态）。

4. **PoolGuard & Risk 引擎**
   - PoolGuard 仍为占位实现，未来需要接入第三方安全 API 并扩展规则。
   - RiskManager 的 Drawdown/MaxDailyGas 需有真实数据来源（PnL 统计未实现）。

5. **测试与部署**
   - 目前仅跑了 `go test ./...`，尚未在测试网进行 end-to-end 验证。
   - 按 TODO 还需 `cmd/replay`、`bot --dry-run`、Sepolia/Base 小额试跑（Runbook 见 `TESTNET_REHEARSAL.md`）。

### 环境与脚本
- Go 版本：1.25.x（见 `go.mod`）。
- 关键脚本：`cmd/bot` 主程序、`cmd/configcheck` 配置校验、`cmd/replay` 事件回放。
- 自测：`go test ./...` 已通过；未执行链上脚本。

### 本次会话活动说明
- 见 `DAILY_ACTIVITY_2025-12-12.md`（本次会话的改动点、runbook 与脚本汇总）。

交接以上工作后，下一阶段可优先完善 Rebalancer 的滑点控制与 Gateway 的 nonce/gas 管理，再推进 storage/监控前端闭环及 testnet 演练。
