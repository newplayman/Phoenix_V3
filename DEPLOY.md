# Phoenix V3 部署与运行向导

本文档将指导您如何启动 Phoenix V3 系统，包括 Go 后端 (Bot) 和 React 前端 (Dashboard)。

## 1. 启动后端 (Bot)

后端负责连接交易所、计算策略、风险控制和执行交易。

**前提条件**: 已安装 Go 1.21+。

```bash
# 1. 确保在项目根目录
cd phoenix-v3

# 2. 配置私钥 / 数据库
# 注：当 `strategy.dry_run=true` 时可不设置 `BOT_PRIVATE_KEY`（Bot 不会广播交易，仅跑行情/策略/门禁与 API）。
export BOT_PRIVATE_KEY="0xYOUR_TESTNET_KEY"
# 可选：export SUPABASE_DB_URL="postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=require"

# 行情（WebSocket 双源聚合，默认 ETH/USDT）
# - PRICE_SYMBOL: 默认 ETH/USDT
# - PRICE_STALE_SEC: 默认 5（超过则 decision 被熔断）
# - PRICE_FREEZE_SEC: 默认 20（超过则 risk.mode=frozen，执行端拒绝所有 intent）
# - DIVERGENCE_PCT: 默认 0.003（0.3%，两源偏差超过则 risk.mode=degraded）
# - PRICE_MODE: 默认 ws_only；可选 ws_with_rest_fallback（显式开启旧 Binance/CoinGecko 轮询作为 fallback）

### 本地自检：模拟 stale/frozen → 恢复（WS-only）

脚本会：
- 启动 bot（强制 dry-run、WS-only）
- 等到 `market.stale=false`（行情正常）
- 临时阻断 WS 出站（优先用 `sudo iptables`，否则提示你手动断网）
- 观察 `risk.mode=frozen` + `decision.block_reason=price_frozen`
- 恢复网络后自动回到 `stale=false`

```bash
API_PORT=18081 PRICE_MODE=ws_only bash scripts/repro_price_freeze_recovery.sh
```

# 安全：强制 dry_run（即使 config.yaml 设置为 false）
# export PHOENIX_FORCE_DRY_RUN=1

# 3. 编译项目
go build -o bot ./cmd/bot/main.go

# 4. 运行 Bot
./bot
```

**成功标志**:
您应该在控制台看到类似以下的输出：
```text
Phoenix V3 Config Loaded...
Monitor server starting on :8080
Phoenix V3 Bot Started (Phase 6: Secured).
Executing Intent intent-xxx [DryRun=true]
>>> Dry Run: Simulated Tx Execution
```

### 配置热更新

后端默认使用 `config.NewManager` 监听 `configs/config.yaml`。当文件被修改并保存后，Bot 会自动重新加载配置，并在日志中打印：

```text
[config] reloaded schema=2024-07-01 strategy=basic-v1
```

无需重启即可调整 `dry_run`、策略参数或风控阈值（风险模块会即时更新最大 Gas / 回撤限制）。

### 受保护 Swap（Quoter 计算 minOut）

Phoenix 支持通过 API 注入一个 `swap` Intent，并由 Bot 自动使用链上 Quoter 报价计算 `amountOutMinimum`，最终把交易 calldata 组装成 `SwapHelper.swapExactInputSingleMinOut(...)`（在链上强制 `amountOut >= amountOutMinimum`）。

前提条件：
- `ADMIN_TOKEN`：用于调用 `/api/intents/enqueue`（请求头 `X-Admin-Token`）。
- `PHOENIX_SWAP_HELPER_ADDRESS`：你部署在目标链上的 `SwapHelper` 合约地址。
- 可选安全开关：`PHOENIX_SWAP_REQUIRE_QUOTER=1`（Quoter 失败则拒绝执行；不允许 `amountOutMinimum=0` 回退）。
- 广播安全闸（建议开启）：
  - `PHOENIX_MANUAL_ONLY=1`：禁用自动策略循环，只执行你注入的 intent
  - `PHOENIX_SWAP_MAX_AMOUNT_IN`：最大允许的 `swap_amount_in`（整数）
  - `PHOENIX_SWAP_ALLOWLIST_POOLS`：允许的 pool 地址（逗号分隔）
  - `PHOENIX_SWAP_ALLOWLIST_TOKENS`：允许的 token 地址（逗号分隔，in/out 都必须在列表里）
  - `PHOENIX_SWAP_MAX_SLIPPAGE_BPS`：最大允许滑点 bps
  - `PHOENIX_SWAP_CONFIRM_STRING`：确认字符串（默认 `I_UNDERSTAND_TESTNET_SWAP`）
- 推荐：使用 `PHOENIX_CONFIG=configs/config_arbitrum_sepolia.yaml`（示例 Arbitrum Sepolia 配置），并设置 `API_PORT=18081`。

示例（dry-run 推荐先开）：

```bash
export PHOENIX_CONFIG=configs/config_arbitrum_sepolia.yaml
export API_PORT=18081
export ADMIN_TOKEN=yourtoken
export BOT_PRIVATE_KEY=0x...
export PHOENIX_SWAP_HELPER_ADDRESS=0x...
export PHOENIX_SWAP_SLIPPAGE_BPS=100   # 1%
export PHOENIX_MANUAL_ONLY=1
export PHOENIX_SWAP_MAX_AMOUNT_IN=2000000
export PHOENIX_SWAP_ALLOWLIST_POOLS=0x53448a5c2c61da7A797f25cEd6d11BE838E674Fb
export PHOENIX_SWAP_ALLOWLIST_TOKENS=0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E,0x1b46aA4C362788E3b2557CE465487d9E41742Fd9
export PHOENIX_SWAP_MAX_SLIPPAGE_BPS=200

./bot
```

推荐（避免手动 export，减少误操作）：

```bash
cp scripts/secrets_template.sh ~/.config/phoenix/secrets.sh
chmod 600 ~/.config/phoenix/secrets.sh
${EDITOR:-vi} ~/.config/phoenix/secrets.sh

SECRETS_FILE=~/.config/phoenix/secrets.sh scripts/run_bot_arbitrum_sepolia_safe.sh
```

注入 swap intent（示例 TRL/USDT on Arbitrum Sepolia）：

```bash
curl -sS -X POST "http://127.0.0.1:18081/api/intents/enqueue" \
  -H 'Content-Type: application/json' \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  --data '{
    "type":"swap",
    "pool_id":"arbsepolia-trl-usdt-3000",
    "chain_id":421614,
    "urgency":9,
    "metadata":{
      "action":"swap_exact_in",
      "swap_pool":"0x53448a5c2c61da7A797f25cEd6d11BE838E674Fb",
      "swap_token_in":"0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E",
      "swap_token_out":"0x1b46aA4C362788E3b2557CE465487d9E41742Fd9",
      "swap_amount_in":"1000000",
      "swap_slippage_bps":"100",
      "swap_confirm":"I_UNDERSTAND_TESTNET_SWAP"
    }
  }'
```

说明：
- 当设置 `PHOENIX_SWAP_FORCE_CONFIRM=1` 时，API 会在入队前直接拒绝缺少/错误的 `swap_confirm`（避免进入队列后才被 Bot 拦截）。

推荐使用脚本注入（自动补齐 `swap_confirm`）：

```bash
SECRETS_FILE=~/.config/phoenix/secrets.sh API_PORT=18081 scripts/enqueue_swap_arbsepolia_trl_usdt.sh
```

### 数据库（Supabase 接入可选）

默认情况下，Bot 使用本地 `phoenix.db`（SQLite）记录交易与意图。如果机器上部署了 Supabase，可以通过设置环境变量切换到 Supabase Postgres：

```bash
export SUPABASE_DB_URL="postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=require"
```

启用后无需修改代码或配置文件，`storage` 模块会自动写入 Supabase 数据库。

### 事件流驱动

在 `configs/config.yaml` 中的 `events` 块可以指定事件流实现：

```yaml
events:
  driver: "redis"                     # memory | redis
  redis_url: "redis://host:6379/0"
  redis_prefix: "phoenix"             # 可选
  redis_group: "phoenix-consumer"     # 消费者组名
```

- `driver=memory`（默认）适合本地开发，所有事件仅在进程内传递。
- `driver=redis` 会把事件写入 Redis Streams，并使用消费者组分发，支持跨进程订阅与重放。

如需快速体验，可以使用 `docker run -p 6379:6379 redis:7` 启动本地 Redis 后再运行 Bot。

### 诊断工具

- **配置校验**：`go run ./cmd/configcheck -config configs/config.yaml`
    - 适合在 CI 中快速验证 schema/字段是否有效。
- **事件回放**（Redis 模式）：`go run ./cmd/replay -driver redis -redis-url redis://localhost:6379/0 -stream phoenix:ticker -count 50`
    - 读取指定 stream 的历史事件并输出 JSON，可用于复盘行情、验证 deterministic replay。

## 2. 启动前端 (Dashboard)

前端提供了一个可视化的控制面板，用于实时监控行情、策略状态和意图队列。

**前提条件**: 已安装 Node.js (建议 v18+) 和 npm。

```bash
# 1. 进入 web 目录
cd web

# 2. 安装依赖 (仅需第一次运行)
npm install

# 3. 启动开发服务器
npm run dev
```

**成功标志**:
终端会显示访问地址，通常是：
```text
  ➜  Local:   http://localhost:5173/
```

## 3. 访问 Dashboard UI

打开浏览器，访问 **http://localhost:5173/**。

### 界面功能说明：

1.  **顶部状态栏**
    *   **ETH Network / Binance Feed**: 显示当前网络和数据源连接状态（绿色为正常）。
    *   **LIVE 标签**: 表示系统处于实时运行模式。

2.  **Market Overview (市场概览)**
    *   显示当前 ETH/USDT 的实时价格（从后端 API 获取）。
    *   **区间可视化条**: 蓝色条代表当前 Bot 设定的 LP 区间，白色滑块代表当前价格位置。直观展示价格是否偏离区间中心。

3.  **Engine State (引擎状态)**
    *   **Current Tick**: 当前链上的 Tick 值。
    *   **Target Tick**: 策略计算出的理想 Tick 值。
    *   **Rebalance Now**: 手动触发再平衡（模拟按钮）。

4.  **Intent Queue (意图队列)**
    *   显示显示待执行的策略意图数量。
    *   当后端触发策略时（每5秒），这里的计数会实时跳动。

## 4. SwapHelper 合约（可选）

在测试网缺乏官方 Router 时，可部署仓库中的 `contracts/SwapHelper.sol`，直接与 Uniswap V3 Pool 交互完成 swap。

1. **部署**
   - 打开 Remix / Foundry，将 `contracts/SwapHelper.sol` 导入后在 Sepolia 选择钱包编译、部署（无构造参数）。
2. **调用**
   - 让用户钱包对 SwapHelper `approve` `tokenIn` 数量（例如 `WETH.approve(helper, amount)`）。
   - 调用 `swapExactInputSingle(pool, tokenIn, tokenOut, amountIn, sqrtLimit)`；如不限制价格，将 `sqrtLimit` 传 `0` 即可自动使用全区间。
   - 合约在回调中完成向池子支付 tokenIn，并把 tokenOut 发送回调用者地址。

> 默认实现支持任意单池 swap（例如 `0xC31a...` 的 WETH/USDC 0.3% 池），可用于在测试网快速补齐 USDC 或验证 LP 生命周期。

## 5. 常见问题

*   **Q: 为什么价格不更新？**
    *   A: 请确保后端 `./bot` 正在运行，并且没有因为错误退出。前端依赖 `http://localhost:8081` 的 API。

*   **Q: 如何连接真实钱包？**
    *   A: 修改 `configs/config.yaml` 中的 RPC 地址，并在环境变量或配置中填入真实私钥（注意安全）。将 `dry_run` 改为 `false`。

*   **Q: 为什么显示 "Simulated Tx Execution"?**
    *   A: 默认配置为 `dry_run: true`，处于影子模式，不会消耗真实 Gas。

---
**Enjoy your trading with Phoenix V3!**

## 6. 自建测试稳定币（TUSD）与流动性池

当公网 faucet 无法提供足量 USDC 时，可使用仓库中的 `contracts/TestUSD.sol` 自建 6 位精度的测试稳定币，并配合 Uniswap V3 建立 `TUSD/WETH` 池供 Phoenix 使用。

### 6.1 部署 TestUSD (TUSD)

1. 打开 Remix 或 Foundry，导入 `contracts/TestUSD.sol`，编译版本 `0.8.20`。
2. 使用测试网钱包部署（无构造参数）。部署者自动成为 owner，可调用 `mint()` 为任意地址增发。
3. 或直接运行仓库提供的脚本（需安装 `python3`）：

```bash
pip install -r scripts/requirements.txt
export RPC_URL="https://ethereum-sepolia.publicnode.com"
export PRIVATE_KEY="0x你的私钥"
python scripts/tusd_setup.py deploy-token
```

脚本会自动编译并部署合约，终端会打印新地址。

4. （可选）部署完成后执行

```solidity
mint(0xYourPhoenixWallet, 1_000_000_000); // 1,000 TUSD (decimals = 6)
```

### 6.2 创建 Uniswap V3 池 & 初始流动性

以下步骤以 Sepolia 上的官方合约为例：

| 合约 | 地址 |
|------|------|
| UniswapV3Factory | `0x0227628f3F023bb0B980b67D528571c95c6DaC1c` |
| NonfungiblePositionManager | `0x1238536071E1c677A632429e3655c799b22cDA52` |

1. 通过 `NonfungiblePositionManager.createAndInitializePoolIfNecessary(token0=TUSD, token1=WETH, fee=500, sqrtPriceX96=...)` 新建池（`token0` 必须是地址更小的合约，一般为 TUSD）。也可以使用脚本：

```bash
# 假设 TUSD_ADDRESS 为上一步输出
python scripts/tusd_setup.py create-pool --token $TUSD_ADDRESS --fee 500 --price 0.000314
```

2. 使用 PositionManager `mint` 一笔基础仓位，例如：
   - `amount0Desired = 1_000_000_000`（1,000 TUSD）
   - `amount1Desired = 300_000_000_000_000_000`（0.3 WETH）
   - `tickLower/tickUpper` 选择覆盖当前价格的区间（例如 `[-90000, -70000]`，对应 ETH≈3,180 USDT）。
3. 也可用脚本自动增加流动性：

```bash
python scripts/tusd_setup.py add-liquidity \
  --token $TUSD_ADDRESS \
  --fee 500 \
  --amount0 1000 \
  --amount1 0.3 \
  --tick-lower -90000 \
  --tick-upper -70000
```

4. 成功后可在 [Uniswap info](https://sepolia-info.uniswap.org/) 或 `eth_call` 查看池的 `liquidity` 与 `slot0`。

### 6.3 更新 Phoenix 配置

完成上述步骤后，在 `configs/config.yaml` 中将目标池信息替换为 TUSD/WETH：

```yaml
pools:
  - id: "tusd-weth-005"
    chain_id: 11155111
    token0: "<TUSD_CONTRACT_ADDRESS>"
    token1: "0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9"   # Sepolia WETH
    token0_decimals: 6
    token1_decimals: 18
    fee: 500
    address: "<POOL_ADDRESS>"
    max_investment: "0.5"
    position_manager: "0x1238536071E1c677A632429e3655c799b22cDA52"
    amount0: "100000000"              # 100 TUSD
    amount1: "31500000000000000"      # 0.0315 WETH
```

策略加载后即可使用自建稳定币完成 mint/swap。若需要补仓，直接调用 `mint()` 给策略钱包增发，再通过 SwapHelper 兑换或重新添加流动性即可。

## 7. Sepolia 真实部署指引

该流程用于在 Sepolia 测试网运行 `dry_run=false` 的真实环境，串联行情、策略、意图、网关与 Supabase 审计。

### 7.1 前置条件

1. Go 1.21+、Python 3.10+、Node.js 18+、git、psql/psycopg（用于操作 Supabase）。
2. 至少一条稳定的 Sepolia RPC（示例：`https://ethereum-sepolia.publicnode.com`）。
3. 钱包地址需持有 ≥0.9 ETH，并可调用 `TestUSD.mint()`。默认资产信息：

| 资源 | 地址 |
|------|------|
| TUSD 合约 | `0x3E49DB88bC85135b6F716E5CD573cDd42b8640c5` |
| WETH 合约 | `0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9` |
| TUSD/WETH 0.05% 池 | `0x1E80b0b6d12Ecf2CDD08bC9c66f2fD594394331d` |
| PositionManager | `0x1238536071E1c677A632429e3655c799b22cDA52` |

### 7.2 资产准备

```bash
# 增发 TUSD（示例：1000 TUSD）
python scripts/tusd_setup.py mint \
  --token 0x3E49DB88bC85135b6F716E5CD573cDd42b8640c5 \
  --to 0xYOUR_WALLET --amount 1000 \
  --rpc https://ethereum-sepolia.publicnode.com --key 0xYOUR_KEY

# （可选）追加流动性
python scripts/tusd_setup.py add-liquidity \
  --token 0x3E49DB88bC85135b6F716E5CD573cDd42b8640c5 \
  --weth 0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9 \
  --fee 500 --amount0 1000 --amount1 0.3 \
  --tick-lower -90000 --tick-upper -70000 \
  --rpc https://ethereum-sepolia.publicnode.com --key 0xYOUR_KEY

# 永久授权 PositionManager（可使用任意钱包或 web3.py 脚本执行 ERC20 approve）
# 例如：cast send 0x3E49... "approve(address,uint256)" 0x1238... 5000000
```

WETH 需要先调用 `deposit()`，并对 PositionManager 执行 `approve`（可通过现有脚本或钱包完成）。

### 7.3 环境变量与配置

```bash
export BOT_PRIVATE_KEY="0x<your_private_key>"
export SUPABASE_DB_URL="postgres://postgres.<tenant>:<password>@192.168.3.18:6543/postgres?sslmode=disable"

# 确保 dry_run=false
sed -i 's/dry_run: true/dry_run: false/' configs/config.yaml  # 或直接编辑
go run ./cmd/configcheck -config configs/config.yaml
```

### 7.4 启动 Bot

```bash
go run ./cmd/bot
```

关键日志：

- `[DEX] Pool ... tick=... liquidity=... dexPrice=...`：来自 `dexstate` 的真实池数据。
- `[IntentExecutor] executing ... dry_run=false`：策略正在执行真实意图。
- `[Gateway] Sent Tx Hash` / `[Gateway] Tx ... status=1`：网关发送并回读交易。
- `[ReceiptWatcher]`：Supabase `trade_records.status` 已根据回执更新。
- `[BalanceGuard] skip intent ... insufficient token balance`：余额风控阻断，需增发 TUSD 或补充 WETH。

### 7.5 监控与审计

- `curl http://localhost:8080/healthz`：检查 feed/dexstate/monitor 状态。
- Supabase：`SELECT * FROM trade_records ORDER BY time DESC LIMIT 20;` 即可验证 `status`、`tx_hash` 以及 `meta_json`（swap/minOut/calldata/approve 等审计信息）。
- 事件流：如切换 `events.driver=redis`，可用 `cmd/replay` 验证 deterministic replay。

#### PnL（估值版）与告警（本地）

- `GET /api/status` 返回 `pnl.portfolio_usd / pnl.daily_pnl_usd / pnl.total_pnl_usd`（基于钱包资产估值 + gas 成本）。
- 常用环境变量：
  - `PHOENIX_STABLE_TOKENS=0xTokenA,0xTokenB`：标记稳定币按 $1 估值（脚本默认包含 Arb Sepolia USDT）。
  - `PHOENIX_PRICE_USD_OVERRIDES=0xToken=1.23,0xOther=4.56`：对无法从池价推导的 token 提供固定 USD 价格。
  - `PHOENIX_PNL_BASELINE_FILE=~/.config/phoenix/pnl_baseline.json`：日基线持久化位置（可覆盖）。
  - `PHOENIX_PNL_RESET_BASELINE=1`：强制重置当日/总基线为当前资产估值（用于切换钱包或首次上线）。

### 7.6 常见问题

| 症状 | 排查思路 |
|------|----------|
| Binance WS 451 | 程序自动退回 REST 轮询；如需低延迟，请在海外 VPS 部署。 |
| Intent 一直被 BalanceGuard 拦截 | 钱包 `token0/token1` 余额不足；重新 mint 或增加 WETH 后重启。 |
| Supabase 写入失败 | 检查 `SUPABASE_DB_URL`、网络连通性、`sslmode`；如需，可临时移除该变量回落到本地 SQLite。 |

完成以上流程即可在 Sepolia 上完成真实干预，后续如需扩展监控或主网部署，可参考 TODO 列表的 Phase 7+ 任务。
