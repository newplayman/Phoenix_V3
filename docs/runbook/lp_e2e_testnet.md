# LP E2E Testnet Runbook (Arbitrum Sepolia)

目标：在 **Arbitrum Sepolia (chainId=421614)** 上完成“LP 相关链路”的端到端验证，但保持 Phoenix 的安全原则：
- 默认不广播（`DRY_RUN=true` + `KILL_SWITCH=true` + `allow_tx_broadcast=false`）
- 任何广播必须显式解锁 + 明确确认
- Web 仍只读（不触发写接口）

本 runbook 提供两种级别：

1) **Mock-LP（推荐 / 可复现）**：部署一套“最小可用 mock”合约（ERC20 + Mock PositionManager + Mock Pool），用 Phoenix 的同一套 calldata/审批/审计链路跑通 `approve → mint`。
   - 目的：验证 Phoenix 的“交易构建/nonce/重试/审计/回执记录”闭环，而不依赖真实 Uniswap V3 Periphery 在 testnet 的部署情况。
   - 注意：这不是 Uniswap V3 的真实 LP，链上数据仅用于 Phoenix plumbing 验证。

2) **Real UniV3（可选）**：如果你提供 Arbitrum Sepolia 上真实可用的 `NonfungiblePositionManager` + pool + tokens 地址，并且 `scripts/check_contract_code.sh` 证明有代码，才执行真实 LP 交易。

---

## 0) Safety Checklist（必须）

- 确认网络：Arbitrum Sepolia（`421614`），RPC 由 `ARBITRUM_SEPOLIA_RPC_URL` 指定。
- 私钥只允许放在本地文件：`$HOME/.config/phoenix/bot_private_key.txt`，权限建议 `600`。
- 默认安全开关不改动；广播时必须显式设置：
  - `strategy.dry_run=false`
  - `safety.kill_switch=false`
  - `safety.allow_tx_broadcast=true`

---

## 1) Mock-LP E2E（推荐）

### 1.1 预备：质量门禁

- `make ci`

### 1.2 部署 Mock-LP 栈（会花 testnet gas）

依赖：
- `python3`
- 推荐 venv：`python3 -m venv /tmp/phoenix_venv && /tmp/phoenix_venv/bin/pip install -r scripts/requirements.txt`

部署（需要显式确认）：

```bash
export ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/...'
export BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt"

MOCKLP_CONFIRM=I_UNDERSTAND_TESTNET_GAS \
  /tmp/phoenix_venv/bin/python scripts/mock_lp_stack_setup.py deploy \
  --rpc "$ARBITRUM_SEPOLIA_RPC_URL" \
  --key-file "$BOT_PRIVATE_KEY_FILE"
```

输出会给出：
- `token_usd`（6 decimals）
- `token_weth`（18 decimals）
- `pool`（MockUniV3Pool，提供 `slot0/liquidity` 读接口）
- `position_manager`（MockNonfungiblePositionManager，提供 `mint/positions/...`）
- 推荐的 Phoenix env 变量（`POOL_ID/POOL_ADDRESS/...`）

### 1.3 Phoenix dry-run（不广播）

```bash
export ADMIN_TOKEN='testtoken'
export PHOENIX_CONTROL_PLANE_ENABLED=1

# 使用 mock-lp 模板（默认安全：dry-run+kill-switch）
export CONFIG_PATH='configs/config_arbitrum_sepolia_mock_lp.template.yaml'

# 按 deploy 输出设置：POOL_ID/POOL_ADDRESS/TOKEN0_ADDRESS/... 等
scripts/accept_control_plane_v1.sh
```

期望结果：
- `operations/preview` 返回 plan，包含 `mint` 步骤（以及可能的 `approve` 步骤）
- `operations/execute` 生成 intent 与 steps，但不会广播 tx（effective_dry_run=true）

### 1.4 Phoenix live execute（会广播，显式解锁）

本步骤会产生链上交易（至少包含 ERC20 approve + position manager mint），用于验证 LP 交易链路。

使用执行配置（脚本会生成临时 YAML，仍使用 env substitution）：

```bash
export ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/...'
export BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt"
export PHOENIX_CONTROL_PLANE_ENABLED=1
export ADMIN_TOKEN='testtoken'
export PHOENIX_DB_PATH='/tmp/phoenix_mock_lp_e2e.sqlite' # 避免复用旧状态，保证可重复

export MOCKLP_E2E_CONFIRM=I_UNDERSTAND_GAS_COSTS
scripts/rehearsal_arbitrum_sepolia_mock_lp_e2e.sh
```

脚本会输出关键 tx hash（approve + mint）并在最后用 `make tx-verify` 校验 mined 状态。

额外验证（推荐）：
- 脚本会在输出中显示 `position_token_id`（来自 PositionManager 的 ERC721 `Transfer` 事件解析），用于确认“确实 mint 了一个 position NFT（mock）”。
- 如果你设置了 `MOCKLP_REUSE_EXISTING=1` 并复用旧部署，而旧合约未发出 `Transfer`，可能看不到 `position_token_id`；此时删除 `/tmp/phoenix_mock_lp_stack.json` 或不设置 `MOCKLP_REUSE_EXISTING` 重新部署即可。

---

## 2) Real UniV3（可选，需你提供地址）

当你给出以下地址，并且 `scripts/check_contract_code.sh` 显示 code=present：
- `POOL_ADDRESS`
- `POSITION_MANAGER_ADDRESS`
- `TOKEN0_ADDRESS` / `TOKEN1_ADDRESS`

才允许在 testnet 做真实 LP e2e。此路径仍需“显式解锁三连”并设置更严格的资金上限（`max_cap_pct`）。
