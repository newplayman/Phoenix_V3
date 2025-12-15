# Phoenix V3 · 2025-12-12 活动说明（本次会话变更摘要）

本文档记录本次会话在 `Phoenix_V3` 仓库内完成的主要工作、原因、验证方式与下一步建议，便于你在本机/服务器上继续推进 testnet 演练与面板使用。

---

## 0. 背景目标

根据你的要求：

1. 审阅目录中所有文档（Phase 规范、审计意见、交接文档）。
2. 以 `PHASE1_REBUILD_TODO.md` 为最终对照，把剩余未完成项补齐，并在 TODO 文档标注。
3. 为“测试链→主网小额实盘”准备可执行的 rehearsal/runbook 与脚本。

---

## 1. 运行环境限制（影响决策的关键因素）

在当前容器环境中：

- **网络访问受限**：无法解析/访问公网 RPC、Binance、CoinGecko 等。
- **本机 socket/端口监听被禁用**：无法 `listen tcp`，因此无法启动 Redis、API server、monitor server。

因此：

- 无法在本环境里完成“真实 Sepolia RPC + Redis Streams + Dashboard”的端到端联调。
- 改为实现一套 **离线可验证 rehearsal**：模拟行情与池状态 → 生成/执行 dry-run intent → 事件落盘（可回放）。

---

## 2. Phase1 TODO 补齐（`PHASE1_REBUILD_TODO.md`）

### 2.1 风险自适应策略 / 阶梯利用率（完成）

落地方式：把“阶梯利用率”具体实现为 **RiskMode → StrategyProfile 动态调参**，并让运行时策略按风险模式实时更新。

- 动态调参：`TargetNotionalPct` / `MinSpreadTicks` / `EngineRiskFactor`
- 运行时每次评估时读取 `riskMgr.Snapshot().Mode`，应用 profile 并 `UpdateConfig`
- 统一 clamp：`pools[].max_cap_pct` 与 `risk.max_utilization_pct` 做一致性裁剪

相关文件：

- `cmd/bot/main.go`（策略评估循环按 RiskMode 动态更新策略配置；并新增 `effectiveMaxCapPct`）
- `internal/strategy/policy.go`、`internal/strategy/policy_test.go`
- `PHASE1_REBUILD_TODO.md`（已把“长期需继续…”更新为已完成说明）

验证：`go test ./...`。

---

## 3. Rehearsal 工具链（runbook + 脚本）

### 3.1 Runbook

- 新增/完善：`TESTNET_REHEARSAL.md`
  - 提供两条路径：
    - **Offline rehearsal**（无网络/无端口监听也能跑）：`-offline -no-api -no-monitor`
    - **Testnet rehearsal**（你自己的机器上跑）：配置 RPC、启 API/monitor、可用 web dashboard

### 3.2 一键脚本

- `scripts/rehearsal_offline.sh`
  - 在受限环境下可直接跑通：生成 `logs/events.jsonl` 并回放验证 `intent_exec`
  - 产物：`logs/events.jsonl`、`logs/bot_offline.log`

- `scripts/rehearsal_sepolia.sh`
  - 面向你自己的机器/服务器（需能联网+能监听端口）：
    - `configcheck` → 启动 bot → 等 API → pause → 触发一次手动 rebalance → 输出观察点
  - 依赖：`BOT_PRIVATE_KEY`（只建议 testnet 钱包）、可用 Sepolia RPC

---

## 4. 事件流：从 Redis 退化到 FileStream（为离线演练提供“可回放”能力）

由于当前环境无法启动 Redis（端口监听被禁用），实现了一个本地事件流驱动：

- 新增 `events.driver: "file"`
- 每次 `Publish` 以 JSONL 追加写入 `events.file_path`（默认 `logs/events.jsonl`）
- 支持 in-process `Subscribe`（同进程订阅），满足 bot 内部模块通信

相关文件：

- `internal/events/file_stream.go`
- `internal/config/config.go`（新增 `events.file_path` 与 driver=file 校验）
- `cmd/bot/main.go`（`initEventStream` 支持 file driver）
- `cmd/replayfile/main.go`（回放 `logs/events.jsonl`）

注意：这不是 Redis 的替代品，而是“受限环境可跑通”的最小实现。

---

## 5. Bot 在演练模式下的关键修复/增强

### 5.1 dry-run 不再硬编码为 false

此前 `executeIntent` 内部存在硬编码 `isDryRun := false` 的逻辑，会导致 dry-run 语义失效。
本次已改为从 config/flag 推导 dry-run，并写入日志与 intent metadata。

相关文件：`cmd/bot/main.go`。

### 5.2 dry-run 下禁止执行 swap

为了防止“dry-run 仍发 swap tx”，在 Rebalancer 执行 swap 的循环中加入 dry-run 分支：dry-run 时只生成 plan，不执行 swap。

相关文件：`cmd/bot/main.go`。

### 5.3 离线模式与禁用 HTTP

为适配受限环境新增 flags：

- `-offline`：模拟 ticker + pool_state（并发布事件）
- `-no-api`：不启动 API server
- `-no-monitor`：不启动 monitor server

相关文件：`cmd/bot/main.go`。

### 5.4 token 价格映射修正

将 `ETHUSDT` ticker 映射到池的 **Token1（WETH）**，并将 `stable_tokens` 设为 1.0，避免将 ETH 价格错误写到 token0（稳定币）上。

相关文件：`cmd/bot/main.go`（`updateTokenPrices`）。

---

## 6. Engine：按 decimals 计算 tick（避免不同池 decimals 下 tick 错算）

新增：

- `engine.EngineInput` 增加 `Token0Decimals/Token1Decimals`
- `PriceToTickWithDecimals` 使用 Uniswap tick 的 raw price 定义：`rawPrice = (1/price) * 10^(dec1-dec0)`

并在 bot 构造 `EngineInput` 时传入该池 decimals。

相关文件：

- `internal/engine/interface.go`
- `internal/engine/asmm.go`
- `internal/engine/asmm_test.go`
- `cmd/bot/main.go`

---

## 7. Dashboard（面板）现状

仓库内已有 Web Dashboard：`web/`（React/Vite）。

- 主要页面：`web/src/App.jsx`
- 默认请求后端：`VITE_API_BASE`（默认 `http://localhost:8081`）
- 依赖后端 API：`/api/status`、`/api/intents`、`/api/trades`、`/api/risk`、`/api/pools`、`/api/pnl`
- 控制功能：pause/resume/cleanup/riskmode/rebalance

受限环境下无法监听端口，无法真正启动面板；在你自己的机器/服务器上可正常启动：

```bash
# 后端
export BOT_PRIVATE_KEY="0x..."  # testnet 钱包
go run ./cmd/bot -config configs/config.yaml

# 前端
cd web
VITE_API_BASE=http://127.0.0.1:8081 npm run dev
```

---

## 8. 安全提示（重要）

你在聊天中发送过测试链私钥；建议立即：

- 将该钱包的 testnet 资产转出（哪怕只是测试币）
- 换新 testnet 钱包继续演练
- 后续只通过本机 `export BOT_PRIVATE_KEY=...` 设置，不要写进仓库或粘贴到任何聊天

---

## 9. 如何在你机器上进行下一步（建议顺序）

1) 先跑离线 rehearsal（确认事件/存储闭环）：

```bash
./scripts/rehearsal_offline.sh
```

2) 再跑 testnet（你机器具备网络/端口条件时）：

```bash
export BOT_PRIVATE_KEY="0x..."  # 新 testnet 钱包
./scripts/rehearsal_sepolia.sh
```

3) 需要长期可审计：保持 `events.driver=file` 或切换到你自己的 Redis。

