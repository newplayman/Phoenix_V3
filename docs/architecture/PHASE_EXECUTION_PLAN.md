# Phase Execution Plan (Small-PR Cadence)

This plan maps `AGENTS.md` phases to concrete change-groups and **exact** verification commands.

## Phase 0 — Safety & Build Gates

Goal: repeatable build + minimum guardrails that fail CI early.

Key deliverables (examples):

- `make ci` as the single “quality gate” entrypoint.
- Scanners in CI:
  - secrets preflight (`scripts/secret_scan.sh`)
  - mainnet guard (`scripts/mainnet_guard_scan.sh`)
  - repo hygiene guard (`scripts/repo_hygiene_scan.sh`)
  - boundary guard (`scripts/boundary_scan.sh`)
- Web build sanity:
  - `npm -C web ci`
  - `npm -C web run build`

Verify:

- `make ci`

## Phase 1 — Boundary Refactors (Decouple + De-bloat)

Goal: keep `cmd/*` as wiring-only; orchestration in `bot/*`; domain in `internal/*`.

Typical change-groups:

- Move orchestration helpers from `cmd/bot` into `bot/*` and inject deps (no `internal/*` → `bot/*` imports).
- Centralize runtime state snapshots/guards in `bot/*`.

Verify (every PR):

- `make ci`
- `make rehearsal-offline`

## Phase 2 — API Contract Alignment (Docs-First)

Goal: `docs/api/*` defines endpoints, schemas, errors; code follows.

Rules:

- All APIs are versioned (`/api/v1/*`).
- Do not expose `internal/*` structs directly as JSON; use API DTOs.
- Any API change requires:
  - docs update (contract source of truth)
  - handler tests (success + error path) or schema tests

Verify:

- `go test ./internal/api -run TestDocsContract`
- `make ci`

## Optional — Market Data Replay (Orderbook Raw + Replay)

Goal: store replayable orderbook raw logs and deterministically reconstruct top-of-book.

Verify:

- `make rehearsal-orderbook-120s`
- `go test ./internal/feed`

## Phase 3 — Dry-Run & Testnet (Arbitrum Sepolia)

Goal: safe-by-default execution with explicit unlock; testnet rehearsal is repeatable.

Guardrails:

- `DRY_RUN` default **true**
- `KILL_SWITCH` default **true**
- `allow_tx_broadcast` default **false**
- broadcast requires explicit triple-unlock + audit

Verify:

- Offline acceptance (no chain): `make rehearsal-offline`
- Testnet dry-run read-only smoke:
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun`
- Testnet chainId integration:
  - `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID`
- Optional (operator-run, gas cost): broadcast probe
  - See `docs/runbook/testnet.md` (“Broadcast probe”).

## Phase 4 — Web Minimal Viable (Read-Only)

Goal: web builds and runs in mock or real read-only API mode; no control/execution implied.

Verify:

- `npm -C web ci`
- `npm -C web run build`
- `VITE_API_BASE=http://127.0.0.1:8081 npm -C web run dev`

## Final Definition of Done

See `docs/runbook/definition_of_done.md`.
