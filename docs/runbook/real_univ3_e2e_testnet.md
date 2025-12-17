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

---

## 1) Preflight (no tx)

验证这些地址在 Arbitrum Sepolia 上确实有合约代码（防止把主网地址粘到测试网）：

```bash
RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" scripts/check_contract_code.sh \
  "$POOL_ADDRESS" "$POSITION_MANAGER_ADDRESS" "$TOKEN0_ADDRESS" "$TOKEN1_ADDRESS"
```

---

## 2) Dry-run end-to-end (no broadcast)

跑通真实 pool 读状态 + 控制面 preview/execute + intent/steps 记录，但保持 `effective_dry_run=true`：

```bash
export ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/...'
export BOT_WALLET_ADDRESS='0x...'

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

此步骤资金/合约依赖更强，建议先在 `docs/runbook/lp_e2e_testnet.md` 的 Mock-LP 路径跑通，再升级到 Real mint。

