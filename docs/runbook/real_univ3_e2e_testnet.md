# Real UniV3 E2E (Arbitrum Sepolia) — Dry-run First

本 runbook 的目标是把 Phoenix 的“真实 UniV3 目标合约 + 真实 Pool 读状态 + 控制面 preview/execute + 审计/回放”链路跑通，但 **默认不广播**。

> 注意：是否能做“真实 LP mint（链上出现 position NFT）”取决于 Arbitrum Sepolia 上是否存在可用的 UniV3 `NonfungiblePositionManager` + 对应 pool（code=present）。本仓库不假设任何地址；你必须显式提供并通过 preflight。

---

## 0) Required Inputs (explicit)

必须由操作者提供（环境变量）：

- `ARBITRUM_SEPOLIA_RPC_URL`（必须是 chainId=421614）
- `POOL_ID`（自定义字符串）
- `POOL_ADDRESS`（UniV3 pool 合约地址，必须有 code）
- `POSITION_MANAGER_ADDRESS`（UniV3 NonfungiblePositionManager 合约地址，必须有 code）
- `TOKEN0_ADDRESS` / `TOKEN1_ADDRESS`（池 token0/token1，必须符合 Uniswap 排序：`token0 < token1`）
- `TOKEN0_DECIMALS` / `TOKEN1_DECIMALS`
- `POOL_FEE`（e.g. 500/3000/10000）
- `STABLE_TOKEN_ADDRESS`（作为 stable side 的 token address，用于价格=1.0）
- `CEX_PRICE_TOKEN_ADDRESS`（用离线/CE X feed 定价的 token address）

推荐（但不必提供私钥）：

- `BOT_WALLET_ADDRESS="0x..."`（用于 preview 时的余额读取；不需要私钥）
- `PHOENIX_PREVIEW_FAKE_BALANCES=1`（可选：当钱包里没有对应 ERC20 时，允许 preview 使用固定假余额以生成 plan；仅用于 dry-run 演练，不能当成真实资金依据）

---

## 1) Preflight (no tx)

验证这些地址在 Arbitrum Sepolia 上确实有合约代码（防止把主网地址粘到测试网）：

```bash
RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" scripts/check_contract_code.sh \
  "$POOL_ADDRESS" "$POSITION_MANAGER_ADDRESS" "$TOKEN0_ADDRESS" "$TOKEN1_ADDRESS"
```

可选：快速确认 `POOL_ADDRESS` 至少是“UniV3-like”（能读 `slot0/liquidity`），并尽量读出 `token0/token1/fee`（如果 pool 实现了这些 view）：

```bash
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/poolinspect -pool "$POOL_ADDRESS"
```

如果你暂时不知道 testnet 上有哪些 UniV3 pool 地址（可选/尽力而为）：

- 扫描最近 N 个区块里是否出现过 UniswapV3Pool `Initialize(uint160,int24)` 事件（不保证存在；范围越大越慢/越可能被 RPC 限制）：

```bash
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/univ3poolscan -lookback 20000
```

- 对扫描出来的候选地址逐个用 `poolinspect` 验证。

如果你有一个 `POOL_ADDRESS`，但不确定 testnet 上对应的 `NonfungiblePositionManager`（或想验证“哪个 PM 真正在给这个 pool mint”），可以用 `univ3mintscan` 反向溯源（仅读 RPC，不发交易）：

```bash
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  go run ./cmd/univ3mintscan -pool "$POOL_ADDRESS" -lookback 200000 -trace
```

期望输出中出现类似：
- `pm=0x... token_id=...`（表示在该 mint tx 的 receipt 中找到了 ERC721 mint，并且 `positions(tokenId)` 与 pool 的 `token0/token1/fee`、ticks 匹配）
- `trace_summary` 会列出“匹配次数最多的 PM 地址”，通常就是你要填的 `POSITION_MANAGER_ADDRESS`

---

## 2) Dry-run end-to-end (no broadcast)

跑通真实 pool 读状态 + 控制面 preview/execute + intent/steps 记录，但保持 `effective_dry_run=true`：

```bash
export ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/...'
export BOT_WALLET_ADDRESS='0x...'
export PHOENIX_PREVIEW_FAKE_BALANCES=1 # 可选：无 token 余额时，用假余额跑通 preview/plan

export POOL_ID='arbsepolia-real-univ3'
export POOL_ADDRESS='0x...'
export POSITION_MANAGER_ADDRESS='0x...'
export TOKEN0_ADDRESS='0x...'
export TOKEN1_ADDRESS='0x...'
export TOKEN0_DECIMALS=6
export TOKEN1_DECIMALS=18
export POOL_FEE=3000
export STABLE_TOKEN_ADDRESS="$TOKEN0_ADDRESS"
export CEX_PRICE_TOKEN_ADDRESS="$TOKEN1_ADDRESS"

make rehearsal-testnet-real-univ3-dryrun
```

期望：
- `/api/v1/pools/{POOL_ID}/state` ready（来自链上 `slot0/liquidity`）
- `operations/preview` 返回包含 `mint` 的 plan（只是计划，不发交易）
- `operations/execute` 生成 intent，并被执行器模拟执行（不会广播 tx）

---

## 3) Real mint (optional, requires explicit unlock)

如果你要链上出现真实 LP position（NFT），必须满足：
- 真实 UniV3 合约地址可用（preflight code=present）
- 你提供 testnet 私钥（仅本地文件），并显式三连解锁：
  - `strategy.dry_run=false`
  - `safety.kill_switch=false`
  - `safety.allow_tx_broadcast=true`

并且 **钱包必须持有足够的 token0/token1**（否则 preview 会因为 `total equity is zero` 或执行阶段余额不足而失败）：

```bash
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN0_ADDRESS" -owner "$BOT_WALLET_ADDRESS"
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN1_ADDRESS" -owner "$BOT_WALLET_ADDRESS"
```

此步骤资金/合约依赖更强，建议先在 `docs/runbook/lp_e2e_testnet.md` 的 Mock-LP 路径跑通，再升级到 Real mint。

推荐使用受控脚本（会先 preview 并拒绝需要 swap 的计划；余额不足则直接退出，不会发交易）：

```bash
export ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/...'
export BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt"
export BOT_WALLET_ADDRESS='0x...'

export POOL_ID='arbsepolia-real-univ3'
export POOL_ADDRESS='0x...'
export POSITION_MANAGER_ADDRESS='0x...'
export TOKEN0_ADDRESS='0x...'
export TOKEN1_ADDRESS='0x...'
export TOKEN0_DECIMALS=18
export TOKEN1_DECIMALS=18
export POOL_FEE=3000
export STABLE_TOKEN_ADDRESS="$TOKEN1_ADDRESS"
export CEX_PRICE_TOKEN_ADDRESS="$TOKEN0_ADDRESS"

REAL_UNIV3_MINT_CONFIRM=I_UNDERSTAND_GAS_COSTS make rehearsal-testnet-real-univ3-mint
```

### 3.1 Funding helper (optional)

如果你的 token0/token1 无法 permissionless `mint()`，但 pool 本身有流动性，你可以用 pool swap 获取另一侧 token：

- `scripts/swaphelper_swap_arbitrum_sepolia.py`（会在 testnet 部署 `contracts/SwapHelper.sol`，并执行 `approve + swap`；强制确认串；带 amount 上限）

---

## Real Mint Record (Example)

- timestamp_utc: 2025-12-17T12:10:35Z
  - pool: 0x53448a5c2c61da7A797F25cEd6d11BE838E674fb
  - position_manager: 0x6b2937Bde17889EDCf8fbD8dE31C3C2a70Bc4d65
  - token0: 0x1b46aA4C362788E3b2557CE465487d9E41742Fd9 (TRL, decimals=9)
  - token1: 0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E (USDT, decimals=6)
  - position_token_id: "2573"
  - tx_approve_token0: 0xb1001d9ce1aaa8eae6dc91cf133065ef2fd6e38eeb71f7215d18de7661e0e925
  - tx_approve_token1: 0xfdfccaf3da65e2b92cf4a3ab073cc08fe7ab6dc4ed326f843fbc66577b2769f2
  - tx_mint: 0xdebfc48f59e8a3f6016741a0a032129937ef70727b6b5f01e0a6c3b81af475cc
  - explorer_mint: https://sepolia.arbiscan.io/tx/0xdebfc48f59e8a3f6016741a0a032129937ef70727b6b5f01e0a6c3b81af475cc
