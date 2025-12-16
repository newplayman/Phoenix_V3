# Phoenix V3 部署与运行向导

本文档将指导您如何启动 Phoenix V3 系统，包括 Go 后端 (Bot) 和 React 前端 (Dashboard)。

重要说明（安全与网络）：
- 默认目标网络：**Arbitrum Sepolia**（`chainId=421614`），仅测试网演练。
- 广播交易默认禁用：`dry_run=true`、`kill_switch=true`、`allow_tx_broadcast=false`；任何广播必须显式解锁并遵循 `docs/runbook/testnet.md`。
- 本文档中的 `configs/config.yaml`（Ethereum Sepolia 11155111）主要用于本地/历史演练；以 Arbitrum Sepolia 为准请使用 `configs/config_arbitrum_sepolia.template.yaml` + `docs/runbook/testnet.md`。

## 1. 启动后端 (Bot)

后端负责连接交易所、计算策略、风险控制和执行交易。

**前提条件**: 已安装 Go 1.21+。

```bash
# 1. 确保在项目根目录
cd phoenix-v3

# 2. 配置私钥 / 数据库
export BOT_PRIVATE_KEY="0xYOUR_TESTNET_KEY"
# 可选：export SUPABASE_DB_URL="postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=require"

# 3. 编译项目
go build -o bot ./cmd/bot

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
    *   A: 请确保后端 `./bot` 正在运行，并且没有因为错误退出。前端依赖 `http://localhost:8081` 的 `/api/v1/*`（需要 `ADMIN_TOKEN`），或使用 `VITE_USE_MOCK=1` 运行前端 mock 模式。

*   **Q: 如何连接真实钱包？**
    *   A: 仅建议测试网。广播交易默认禁用，除非同时满足：
        - `strategy.dry_run: false`
        - `safety.kill_switch: false`
        - `safety.allow_tx_broadcast: true`
      私钥只允许通过环境变量提供（例如 `BOT_PRIVATE_KEY`），禁止写入代码/配置/日志。

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

1. 通过 `NonfungiblePositionManager.createAndInitializePoolIfNecessary(token0=TUSD, token1=WETH, fee=500, sqrtPriceX96=...)` 新建池（注意：Uniswap 的 token0/token1 是**按地址排序**的，不保证稳定币一定是 token0）。也可以使用脚本（`--price` 固定表示 **WETH/TUSD** 的人类单位价格，脚本会自动处理 token0/token1 排序与 decimals）：

```bash
# 假设 TUSD_ADDRESS 为上一步输出；若 ETH≈3180 TUSD，则 WETH/TUSD≈1/3180≈0.000314
python scripts/tusd_setup.py create-pool --token $TUSD_ADDRESS --fee 500 --price 0.000314
```

2. 使用 PositionManager `mint` 一笔基础仓位，例如：
   - `amount0Desired = 1_000_000_000`（1,000 TUSD）
   - `amount1Desired = 300_000_000_000_000_000`（0.3 WETH）
   - `tickLower/tickUpper` 选择覆盖当前价格的区间：建议先用脚本根据目标价格自动算 ticks（并对齐 tick spacing），不要手填。
3. 也可用脚本自动增加流动性：

```bash
python scripts/tusd_setup.py calc-ticks --token $TUSD_ADDRESS --fee 500 --stable-per-weth 3180 --width-pct 0.05

python scripts/tusd_setup.py add-liquidity \
  --token $TUSD_ADDRESS \
  --fee 500 \
  --amount0 1000 \
  --amount1 0.3 \
  --tick-lower <LOWER_TICK> \
  --tick-upper <UPPER_TICK>
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
    cex_price_token: "0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9"
    token0_decimals: 6
    token1_decimals: 18
    fee: 500
    address: "<POOL_ADDRESS>"
    max_investment: "0.5"
    position_manager: "0x1238536071E1c677A632429e3655c799b22cDA52"
    amount0: "100000000"              # 100 TUSD
    amount1: "31500000000000000"      # 0.0315 WETH
    stable_tokens:
      - "<TUSD_CONTRACT_ADDRESS>"     # stable side (priced at 1.0)
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
  --tick-lower <LOWER_TICK> --tick-upper <UPPER_TICK> \
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
- Supabase：`SELECT * FROM trade_records ORDER BY time DESC LIMIT 20;` 即可验证 `status` 和 `tx_hash`。
- 事件流：如切换 `events.driver=redis`，可用 `cmd/replay` 验证 deterministic replay。

### 7.6 常见问题

| 症状 | 排查思路 |
|------|----------|
| Binance WS 451 | 程序自动退回 REST 轮询；如需低延迟，请在海外 VPS 部署。 |
| Intent 一直被 BalanceGuard 拦截 | 钱包 `token0/token1` 余额不足；重新 mint 或增加 WETH 后重启。 |
| Supabase 写入失败 | 检查 `SUPABASE_DB_URL`、网络连通性、`sslmode`；如需，可临时移除该变量回落到本地 SQLite。 |

完成以上流程即可在 Sepolia 上完成真实干预，后续如需扩展监控或主网部署，可参考 TODO 列表的 Phase 7+ 任务。

## 附：Phase 1 Testnet Rehearsal

更细的 dry-run → Redis 事件流 → testnet 小额实盘演练步骤见：`TESTNET_REHEARSAL.md`。
