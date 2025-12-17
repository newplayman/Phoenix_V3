# Testnet Runbook (Arbitrum Sepolia)

## Preconditions

- Go toolchain installed.
- Node.js + npm installed (for `web/`).
- No secrets committed; use env vars for keys/tokens/DB URLs.
- Testnet:
  - Network: Arbitrum Sepolia
  - ChainID: `421614`
  - Explorer: `https://sepolia.arbiscan.io/`
- Funding note:
  - Gas is paid in **ETH on Arbitrum Sepolia (L2)**.
  - Sepolia ETH on L1 is not spendable on Arbitrum Sepolia until it is bridged to Arbitrum Sepolia or obtained via an Arbitrum Sepolia faucet.

## 1) Build + local quality gate

- `make ci`

## 1.5) Offline rehearsal (no chain calls)

Validates control-plane v1 preview/execute + audit + DB writes **without** requiring a real pool or funds:

- `make rehearsal-offline` (or `scripts/rehearsal_arbitrum_sepolia_offline.sh`)

## 2) Start bot + API (dry-run, safe default)

1. Prepare a config from `configs/config_arbitrum_sepolia.template.yaml` by exporting env vars referenced in the template:
   - `ARBITRUM_SEPOLIA_RPC_URL`
   - `POOL_ID`, `POOL_ADDRESS`, `POSITION_MANAGER_ADDRESS`
   - `TOKEN0_ADDRESS`, `TOKEN1_ADDRESS`, `STABLE_TOKEN_ADDRESS`, `CEX_PRICE_TOKEN_ADDRESS`
   - `TOKEN0_DECIMALS`, `TOKEN1_DECIMALS`, `POOL_FEE`
2. Validate the template expands and passes schema checks:
   - `scripts/validate_arbitrum_sepolia_template.sh`
   - Optional (also checks on-chain code for `POOL_ADDRESS` and `POSITION_MANAGER_ADDRESS`): `make validate-arb-sepolia-onchain`
3. Provide auth token for the console API:
   - `export ADMIN_TOKEN="<random-string>"`
4. Provide a wallet identity for preview-time balance reads:
   - Preferred (no private key needed): `export BOT_WALLET_ADDRESS="0x..."`
   - Or: `export BOT_PRIVATE_KEY="0x<testnet_private_key>"`

Run:

- `PHOENIX_CONTROL_PLANE_ENABLED=1 go run ./cmd/bot -config configs/config_arbitrum_sepolia.template.yaml -offline-feed`

Tip:
- For testnet rehearsals, consider disabling the automatic strategy loop (manual control-plane actions only):
  - `go run ./cmd/bot -config configs/config_arbitrum_sepolia.template.yaml -offline-feed -manual-only`

Notes:

- Browser CORS is allowlisted via `api.cors_allowed_origins` (defaults to `localhost:5173`).
- Offline acceptance mode (no real pool needed) can use a fake balance reader for preview planning:
  - `export PHOENIX_PREVIEW_FAKE_BALANCES=1`
  - Run bot with `-offline` (simulated pool state) so `/api/v1/pools/{pool_id}/state` becomes ready without on-chain calls.
  - Note: fake balances are only enabled when both `-offline` and `effective_dry_run=true` are true.
  - This is **for dry-run rehearsals only**; it must not be used for real executions.

## 2.2) Contract presence preflight (recommended)

Before configuring a real pool/position manager on Arbitrum Sepolia, verify the addresses actually have code on-chain:

- `RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" scripts/check_contract_code.sh 0x... 0x...`
- Or via make: `RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make check-contracts ADDRS="0x... 0x..."`
- Or via secrets file (recommended to avoid pasting RPC URLs into shell history):
  - `SECRETS_FILE="$HOME/.config/phoenix/secrets.sh" scripts/check_contract_code.sh 0x... 0x...`

This prevents common testnet failures where a “mainnet address” is pasted into a testnet config (code will be missing).

## 2.5) Testnet dry-run read-only rehearsal (recommended)

This validates:
- Arbitrum Sepolia RPC connectivity (chainId integration test)
- `/api/v1/*` read endpoints (including SSE) in dry-run + offline pool-state mode
- without broadcasting any transactions

Prereqs:
- Export `ARBITRUM_SEPOLIA_RPC_URL`.
- Optional: set `ARBITRUM_SEPOLIA_POOL_ADDRESS` to run the live pool-state integration test.

Run:
- `ADMIN_TOKEN=testtoken API_BASE=http://127.0.0.1:8081 scripts/rehearsal_arbitrum_sepolia_dryrun_testnet.sh`

Optional integration test (requires a real pool address):
- `ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" ARBITRUM_SEPOLIA_POOL_ADDRESS="$POOL_ADDRESS" go test ./... -tags=integration -run TestArbitrumSepoliaPoolState_Slot0AndLiquidity`

## 2.6) Live read-only rehearsal (requires on-chain pool code)

This validates that Phoenix can read `slot0/liquidity` from a real on-chain contract on Arbitrum Sepolia **without broadcasting any transactions**.

If you do not have a UniV3 pool deployment on Arbitrum Sepolia, you can deploy a minimal mock pool that implements the required view methods:

- Prereqs:
  - `python3` + `pip` installed
  - Python deps (recommended venv; avoids PEP 668 "externally managed" errors):
    - `python3 -m venv /tmp/phoenix_venv`
    - `/tmp/phoenix_venv/bin/pip install -r scripts/requirements.txt`
- Deploy mock pool (requires explicit confirmation; spends testnet gas):
  - `MOCKPOOL_CONFIRM=I_UNDERSTAND_TESTNET_GAS RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" /tmp/phoenix_venv/bin/python scripts/mock_univ3_pool_setup.py deploy`
  - The command prints `POOL_ADDRESS`.
- Run live read-only rehearsal:
  - `ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" POOL_ADDRESS="$POOL_ADDRESS" make rehearsal-testnet-live-read`

Notes:
- This keeps `PHOENIX_CONTROL_PLANE_ENABLED=0` and uses `-dry-run -manual-only` (no broadcasts).
- Address preflight is enforced via `scripts/check_contract_code.sh` and chainId must be `421614`.

## 3) Verify API contract (smoke)

- `CONFIG_PATH=configs/config_arbitrum_sepolia.template.yaml POOL_ID="$POOL_ID" CHAIN_ID=421614 PHOENIX_CONTROL_PLANE_ENABLED=1 scripts/accept_control_plane_v1.sh`
  - Or use the wrapper: `make rehearsal-arb-sepolia` (requires the template env vars + RPC)

Notes:

- Control plane write APIs are disabled unless `api.control_plane_enabled: true` (or `PHOENIX_CONTROL_PLANE_ENABLED=1`).
- Transaction broadcast is disabled unless all of the following are true:
  - `strategy.dry_run: false`
  - `safety.kill_switch: false`
  - `safety.allow_tx_broadcast: true`

## 4) Web console (read-only)

- `npm -C web ci`
- `VITE_API_BASE=http://127.0.0.1:8081 npm -C web run dev`

## 5) Broadcast probe (testnet-only, minimal risk)

This verifies **wallet signing + nonce + broadcast** without interacting with Uniswap contracts.
It sends a **0-value transaction to self** (gas cost only).

Precondition:
- The wallet must have some Arbitrum Sepolia ETH for gas. You can print the address from the local key file (no broadcast):
  - `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" make wallet-addr`
  - `ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" ADDR=0x... make native-balance`

Safety requirements (all must be set explicitly):
- `TXPROBE_DRY_RUN=false`
- `TXPROBE_KILL_SWITCH=false`
- `TXPROBE_ALLOW_BROADCAST=true`
- `TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS`
- `BOT_PRIVATE_KEY` (or `BOT_PRIVATE_KEY_FILE`) set **only when broadcasting** (never commit / never log); in dry-run the probe simulates without requiring a key.

Run:
- Wrapper (recommended):
  - `SECRETS_FILE="$HOME/.config/phoenix/secrets.sh" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS scripts/broadcast_probe_arbitrum_sepolia.sh`
- Interactive (recommended if you do not want to store a secrets file; hidden input):
  - `TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make broadcast-probe-interactive`
- Non-interactive without env key (recommended for CI-like shells; key file is local-only, never committed):
  - `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make broadcast-probe`
  - Optional helper (records only if actually sent): `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make broadcast-probe-record`
- Or directly:
  - `ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" BOT_PRIVATE_KEY="$BOT_PRIVATE_KEY" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS go run ./cmd/txprobe -chain-id 421614`

Verify mined receipt (post-broadcast):
- `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make tx-verify`
- Wait until mined (recommended):
  - `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make tx-wait`

Tip:
- To create the key file without pasting into your shell history: `scripts/setup_bot_private_key_file.sh "$HOME/.config/phoenix/bot_private_key.txt"` (interactive hidden input; local-only).

## 6) Emergency Stop / “Rollback” Semantics

On-chain transactions are not reversible. Phoenix implements “rollback” as **rapid prevention of further side effects**:

- **Kill-switch**: set `safety.kill_switch: true` (or leave it default) to force `effective_dry_run=true` (no broadcasts).
- **Dry-run**: set `strategy.dry_run: true` to prevent broadcasts even if kill-switch is off.
- **Broadcast allowlist**: keep `safety.allow_tx_broadcast: false` unless you explicitly unlock it.
- **Manual-only mode**: run the bot with `-manual-only` to disable the automatic strategy loop (control-plane actions only).
- **Pause/Resume (control plane)**: if the control plane is enabled, use pause/resume style actions to stop new work at the pool level (always audited).

Operator guidance:
- Treat any “execute” that could broadcast as “point of no return”; only proceed after preview, and only after explicit unlock of all safety gates.
