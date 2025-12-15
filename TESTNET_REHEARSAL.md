# Phoenix V3 · Testnet Rehearsal Runbook (Phase 1)

目标：在 **不改代码** 的前提下，完成从 `dry-run` 到 testnet 小额真实交易的可验证闭环，并能用事件流/DB 做回放审计。

## 0. 前置条件

- Go 已安装，且能 `go test ./...`。
- 你有一个 testnet 钱包私钥（仅用于测试网）。
- `configs/config.yaml` 已填写正确的 `chains[].rpc` 与 `pools[]`，且每个池必须满足：
  - `cex_price_token`：哪个 token 用 CEX feed 定价（例如 WETH）。
  - `stable_tokens`：稳定币列表（会被强制置价为 1.0，必须包含池子的稳定侧，且不得包含 `cex_price_token`）。

注意：本仓库运行环境可能会禁用网络与端口监听；这种情况下请使用本文的 Offline rehearsal（第 2 节）先完成“事件回放闭环”。

环境变量：

```bash
export BOT_PRIVATE_KEY="0x<testnet_private_key>"
```

可选（使用 Redis Streams 作为事件流）：

```bash
export REDIS_URL="redis://localhost:6379/0"
```

## 1. 配置校验

```bash
go run ./cmd/configcheck -config configs/config.yaml
```

## 2. Dry-run 演练（不发链上交易）

方式 A：改配置（持久）

```yaml
strategy:
  dry_run: true
```

方式 B：命令行覆盖（推荐，避免改文件）

```bash
go run ./cmd/bot -config configs/config.yaml -dry-run
```

说明：dry-run 模式下现在 **不强制要求** 设置 `BOT_PRIVATE_KEY`，用于本地流程演练。
但如果你希望 Rebalancer/BalanceGuard 读取真实 ERC20 余额（更接近实盘行为），仍建议设置测试网私钥。

### 2.1 Offline rehearsal（无网络/无端口监听也能跑）

该模式会模拟：
- `ticker`（ETHUSDT 缓慢变化）
- `pool_state`（tick 缓慢变化 + sqrtPriceX96）

并把事件写入 `logs/events.jsonl`，用于回放审计。

```bash
rm -f logs/events.jsonl
go run ./cmd/bot -config configs/config.yaml -dry-run -offline -no-api -no-monitor
```

或直接一键脚本：

```bash
./scripts/rehearsal_offline.sh
```

回放：

```bash
go run ./cmd/replayfile -path logs/events.jsonl -topics ticker,pool_state,intent_exec -follow
```

观察点：
- `/api/status`：`control.paused`、`risk`、`pools` 是否正常。
- 日志：`[DEX] Pool ...`、`[Strategy]`、`[Rebalancer] Plan generated`。

手动触发（验证控制面板链路）：

```bash
curl -s -X POST "http://localhost:8081/api/control/rebalance?pool_id=tusd-weth-005"
curl -s -X POST "http://localhost:8081/api/control/pause"
curl -s -X POST "http://localhost:8081/api/control/resume"
```

## 3. 启用可回放事件流（推荐）

### 3.1 FileStream（推荐：无需网络/无需 Redis）

编辑 `configs/config.yaml`：

```yaml
events:
  driver: "file"
  file_path: "logs/events.jsonl"
```

启动 bot：

```bash
go run ./cmd/bot -config configs/config.yaml -dry-run
```

回放（类似 tail -f）：

```bash
go run ./cmd/replayfile -config configs/config.yaml -topics ticker,pool_state -follow
```

### 3.2 Redis Streams（可选：需要允许本机 TCP 监听/连接）

编辑 `configs/config.yaml`：

```yaml
events:
  driver: "redis"
  redis_url: "redis://localhost:6379/0"
  redis_prefix: "phoenix"
  acks_required: false
  replay_retention: "24h"
```

然后启动 bot（仍可 dry-run）：

```bash
go run ./cmd/bot -config configs/config.yaml -dry-run
```

如果你本机没有 Redis，可以使用系统 Redis 或 docker Redis；本仓库的 `cmd/redisdev` 仅在允许本机 socket 的环境下可用。

用 `cmd/replay` 追踪事件：

```bash
go run ./cmd/replay -config configs/config.yaml -topics ticker,pool_state -follow
```

## 4. Testnet 小额实盘（发链上交易）

1) 确保 `strategy.dry_run=false`（或不加 `-dry-run`）。

2) 启动 bot：

```bash
go run ./cmd/bot -config configs/config.yaml
```

注意：如果你之前用错误的 `sqrtPriceX96` 初始化过某个 `(token0,token1,fee)` 池子，无法通过同接口“重新初始化”；请按 `POOL_REBUILD_SEPOLIA_TUSD_WETH.md` 重建一个新 token/新池再继续演练。

也可以用一键脚本（会启动 bot→等待 API→pause→触发一次 rebalance→退出并停止 bot）：

```bash
export BOT_PRIVATE_KEY="0x..."
./scripts/rehearsal_sepolia.sh
```

3) 手动触发一笔 Rebalance（建议先从单池开始）：

```bash
curl -s -X POST "http://localhost:8081/api/control/rebalance?pool_id=tusd-weth-005"
```

4) 回放审计：

```bash
go run ./cmd/replay -config configs/config.yaml -topics intent_exec,audit -follow
```

## 5. 常见回滚/止损

- 暂停执行：

```bash
curl -s -X POST "http://localhost:8081/api/control/pause"
```

- 紧急撤摊（Cleanup）：

```bash
curl -s -X POST "http://localhost:8081/api/control/cleanup"
```

## 6. 你应该看到什么（验收）

- Dry-run：Intent 能生成、Rebalancer 能出 Plan、不会发 tx。
- Testnet：至少 1 笔 swap 或 mint 交易成功，Storage 中有记录（SQLite 或外部 DB）。
- Redis：能用 `cmd/replay` 看到 ticker/pool_state/intent_exec 的持续输出。
