# Definition of Done (Pre-Live Final Verification)

This repo is considered ready for **final pre-live verification** when all items below pass.

## Build + Unit Tests

- `make ci`
  - Includes a secrets preflight scan (`scripts/secret_scan.sh`) over commit-candidate files only.
  - Includes a repo hygiene scan (`scripts/repo_hygiene_scan.sh`) to prevent committing `web/node_modules`, `web/dist`, or local DB files.
  - Includes a boundary scan (`scripts/boundary_scan.sh`) to prevent `internal/*` importing `cmd/*` or `bot/*`.
  - Includes a formatting check (`scripts/gofmt_check.sh`) so CI fails on unformatted Go code.

## Dry-run (no broadcasts)

- Ensure defaults are safe:
  - `strategy.dry_run: true`
  - `safety.kill_switch: true`
  - `safety.allow_tx_broadcast: false`
- Example (offline DEX + offline feed, no network required):
  - `ADMIN_TOKEN=testtoken go run ./cmd/bot -config configs/config.yaml -offline -offline-feed`
- Optional: disable automatic strategy loop during rehearsals:
  - `go run ./cmd/bot -config configs/config.yaml -offline -offline-feed -manual-only`

## API Contract

- Read endpoints work with auth:
  - `GET /api/v1/health`
  - `GET /api/v1/pools`
  - `GET /api/v1/pools/{pool_id}/state`
  - `GET /api/v1/intents`
  - `GET /api/v1/intents/{intent_id}`
  - `GET /api/v1/tx`
  - `GET /api/v1/audit`
  - `GET /api/v1/stream` (SSE)
- Control plane is disabled unless explicitly enabled:
  - `api.control_plane_enabled: true` or `PHOENIX_CONTROL_PLANE_ENABLED=1`
- Offline control-plane acceptance (no pool/funds required):
  - `scripts/rehearsal_arbitrum_sepolia_offline.sh` (runs with `-manual-only`)

## Testnet Verification (Arbitrum Sepolia)

- Optional contract presence preflight (prevents wrong-chain addresses):
  - `RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make check-contracts ADDRS="0x... 0x..."`
- Optional: validate config template + on-chain code in one step (requires real addresses):
  - `make validate-arb-sepolia-onchain`
- RPC connectivity + chainId:
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID`
- Read-only API smoke on testnet RPC (no broadcasts; offline pool-state):
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun`
- Optional live read-only rehearsal (requires a pool address with code on Arbitrum Sepolia):
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> POOL_ADDRESS=<pool> make rehearsal-testnet-live-read`
- Optional mock-LP plumbing e2e (deploys test contracts + broadcasts approve+mint under explicit confirm):
  - See `docs/runbook/lp_e2e_testnet.md:1`
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> BOT_PRIVATE_KEY_FILE=<path> MOCKLP_E2E_CONFIRM=I_UNDERSTAND_GAS_COSTS make rehearsal-testnet-mock-lp`
- Optional real UniV3 mint on testnet (requires: token0/token1 balances + explicit unlock; may not be possible on all testnets):
  - See `docs/runbook/real_univ3_e2e_testnet.md:1`
  - Optional funding helper (testnet gas; may deploy SwapHelper): `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> BOT_PRIVATE_KEY_FILE=<path> BOT_WALLET_ADDRESS=0x... FUND_TRL_USDT_CONFIRM=I_UNDERSTAND_TESTNET_GAS make rehearsal-testnet-fund-trl-usdt`
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> BOT_PRIVATE_KEY_FILE=<path> REAL_UNIV3_MINT_CONFIRM=I_UNDERSTAND_GAS_COSTS make rehearsal-testnet-real-univ3-mint`
  - Optional one-shot wrapper (fund + mint): `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> BOT_PRIVATE_KEY_FILE=<path> BOT_WALLET_ADDRESS=0x... FUND_TRL_USDT_CONFIRM=I_UNDERSTAND_TESTNET_GAS REAL_UNIV3_MINT_CONFIRM=I_UNDERSTAND_GAS_COSTS make rehearsal-testnet-real-univ3-full-trl-usdt`
- Optional broadcast probe (gas-only, tx to self, requires explicit unlock):
  - See `docs/runbook/testnet.md:1` section "Broadcast probe" (or `make broadcast-probe`).
  - Sanity-check (no key required): `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe` should print `effective_dry_run=true` unless explicitly unlocked.
  - Interactive (no secrets file): `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe-interactive` (prompts for key only if broadcast is explicitly unlocked).
  - Non-interactive without env key: `BOT_PRIVATE_KEY_FILE=<path> ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS make broadcast-probe`
  - Record helper (records only if actually sent): `BOT_PRIVATE_KEY_FILE=<path> ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS make broadcast-probe-record`
  - Receipt verify (post-broadcast): `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make tx-verify` (or `make tx-wait` to poll until mined)

## Security Guardrails

- No secrets committed to git.
- `npm audit` has no known vulnerabilities (or any remaining findings are explicitly documented and mitigated).
- Arbitrum One (mainnet, chainId=42161) is blocked by default (scripts + bot) and requires explicit override:
  - `PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1`

## Market Data Replay (Optional)

- Orderbook raw log + replay (Binance public feed; requires network):
  - See `docs/runbook/orderbook_replay.md:1`
  - `make rehearsal-orderbook-120s`

## Evidence (Optional, Recommended)

- Broadcast probe record (Arbitrum Sepolia):
  - Append-only log is kept in `docs/runbook/prelive_signoff.md:1` (includes explorer links).
- Live pool-state integration (Arbitrum Sepolia):
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> ARBITRUM_SEPOLIA_POOL_ADDRESS=<pool> go test ./internal/dexstate -tags=integration -run TestArbitrumSepoliaPoolState_Slot0AndLiquidity`
