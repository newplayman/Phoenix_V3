# Phase Changelog (What Changed, How To Test)

This document tracks **what changed per phase** (files + intent), so reviewers can split work into small PRs while keeping `make ci` green.

## Phase 0 — Safety & Build Gates

**Key changes**

- Added a single “quality gate” entrypoint (`make ci`) and wired CI to run it.
- Added guard scripts:
  - secrets scan
  - mainnet (Arbitrum One) hardcode scan
  - boundary scan (layer invariants)
  - repo hygiene scan (prevent committing `web/node_modules`, `web/dist`, local DB)
- Ensured `web/` has a deterministic build (`npm -C web ci`, `npm -C web run build`).

**Key files**

- `Makefile`
- `.github/workflows/ci.yml`
- `scripts/ci.sh`
- `scripts/gofmt_check.sh`
- `scripts/secret_scan.sh`
- `scripts/mainnet_guard_scan.sh`
- `scripts/boundary_scan.sh`
- `scripts/repo_hygiene_scan.sh`

**Risk / mitigation**

- Risk: guard scripts can become “false positive” noisy.
  - Mitigation: each script targets repo-local patterns and fails with actionable remediation.

**How to test**

- `make ci`

**If the repo previously tracked artifacts**

- Untrack once (land as a hygiene-only PR):
  - `git rm -r --cached web/node_modules`
  - `git rm --cached phoenix.db`
  - `git rm --cached bot`

## Phase 1 — Boundary Refactors (Decouple + De-bloat)

**Key changes**

- Centralized orchestration/runtime state into `bot/*` (snapshots, guards, pool watchers, position sync, intent recording, executor loop).
- Thinned `cmd/bot/main.go` by moving the intent executor behind a DI surface (`bot.IntentExecutorDeps`) while keeping behavior stable.

**Key files**

- `bot/intent_executor.go`
- `bot/runtime_state.go`
- `bot/pool_watchers.go`
- `bot/position_sync.go`
- `bot/position_ops.go`
- `bot/intent_recording.go`
- `bot/rebalance_limits.go`
- `cmd/bot/main.go`
- `docs/architecture/STACK_HEALTH_REPORT.md`
- `docs/architecture/boundaries.md`

**Risk / mitigation**

- Risk: runtime in-memory state coupling.
  - Mitigation: boundary scan prevents `internal/*` importing `bot/*`; executor deps are injected to keep test seams.

**How to test**

- `make ci`
- `make rehearsal-offline`

## Phase 2 — API Contract Alignment (Docs-First)

**Key changes**

- Enforced docs-as-contract for `/api/v1/*` routes and shapes with tests.
- Aligned control-plane preview contract: `action_type` restricted to `force_rebalance` and verified by handler tests.

**Key files**

- `docs/api/API_AND_EVENT_SPEC.md`
- `docs/api/CONTROL_PLANE_BACKEND_SPEC.md`
- `internal/api/docs_contract_test.go`
- `internal/api/contract_v1_test.go`
- `internal/api/server_v1_test.go`

**Risk / mitigation**

- Risk: contract drift between docs and implementation.
  - Mitigation: tests fail if new v1 routes are undocumented or if required doc sections are missing.

**How to test**

- `go test ./internal/api -run TestDocsContract`
- `make ci`

## Phase 3 — Dry-Run & Testnet (Arbitrum Sepolia)

**Key changes**

- Safe-by-default execution gates (dry-run + kill-switch + broadcast allowlist).
- Repeatable rehearsals:
  - offline control-plane acceptance (no chain)
  - testnet dry-run read-only smoke (no broadcasts)
- Optional testnet broadcast probe (0-value tx to self; explicit unlock required).
- Optional “pre-live signoff” wrapper that runs all checks and only performs real broadcast when explicitly unlocked, then records the tx into the signoff doc.
- Optional receipt verification helper for broadcast probe (poll until mined).

**Key files**

- `internal/config/config.go`
- `internal/config/config_test.go`
- `scripts/rehearsal_arbitrum_sepolia_offline.sh`
- `scripts/rehearsal_arbitrum_sepolia_dryrun_testnet.sh`
- `scripts/smoke_api_v1_readonly.sh`
- `cmd/txprobe/main.go`
- `cmd/txverify/main.go`
- `scripts/broadcast_probe_arbitrum_sepolia.sh`
- `scripts/broadcast_probe_arbitrum_sepolia_interactive.sh`
- `scripts/broadcast_probe_record.sh`
- `scripts/broadcast_probe_and_record.sh`
- `scripts/setup_bot_private_key_file.sh`
- `scripts/txverify_arbitrum_sepolia.sh`
- `scripts/wait_tx_mined_arbitrum_sepolia.sh`
- `scripts/prelive_signoff.sh`
- `docs/runbook/testnet.md`
- `docs/runbook/definition_of_done.md`
- `docs/runbook/prelive_signoff.md`

**Risk / mitigation**

- Risk: accidental broadcasting.
  - Mitigation: broadcast requires explicit triple-unlock + confirmation; dry-run effective by default.

**How to test**

- `make rehearsal-offline`
- `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun`
- `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID`
- Optional (operator-run, gas cost): see `docs/runbook/testnet.md` and `docs/runbook/prelive_signoff.md`
  - Recommended (records tx + waits receipt): `BOT_PRIVATE_KEY_FILE=... TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make prelive-signoff`
  - Receipt check: `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make tx-wait`

## Phase 4 — Web Minimal Viable (Read-Only)

**Key changes**

- `web/` builds and runs in read-only mode, with `VITE_API_BASE` pointing to a real or mocked backend.
- Guardrails ensure web does not reference control-plane endpoints during `experimental` phase.

**Key files**

- `web/package.json`
- `web/src/*`
- `docs/web/WEB_CONSOLE_FRONTEND_SPEC.md`
- `scripts/boundary_scan.sh`

**Risk / mitigation**

- Risk: UI implies execution or accidentally calls control APIs.
  - Mitigation: boundary scan fails CI if web references control endpoints; docs explicitly mark web as read-only (`experimental`).

**How to test**

- `npm -C web ci`
- `npm -C web run build`
- `VITE_API_BASE=http://127.0.0.1:8081 npm -C web run dev`
