# Stack Health Report (Shit-Mountain Risk Scan)

This document is the **current** engineering risk scan for Phoenix_V3, written under the repo boundary model:

- `cmd/*` entrypoints only
- `bot/*` orchestration/runtime
- `internal/*` domain + adapters
- `docs/*` is the **contract source of truth**
- `web/*` is an API consumer only (read-only by default)

## 1) Dependency Graph (text)

Expected layering:

- `cmd/*` → `bot/*` → `internal/*`
- `cmd/*` → `internal/*` (wiring is allowed)
- `web/*` → `/api/v1/*` (HTTP only)

Forbidden / high-risk:

- `internal/*` → `cmd/*` (reverse dependency)
- `web/*` importing `internal/*` (UI leaking into core)
- API handlers exposing `internal/*` structs directly as JSON (contract drift risk)

Current notes:

- `web/*` does not import `internal/*` (OK).
- `/api/v1/*` routes are enforced by tests and docs (OK).
- Web spec clarified: write flows are `beta/stable` only; `experimental` web remains read-only (prevents doc-driven drift).
- Repo hygiene: `web/node_modules` and `phoenix.db` are removed from git tracking and ignored (reduces churn + prevents accidental large/binary artifacts in PRs).
- `cmd/bot/main.go` remains a “god entrypoint” with multiple responsibilities (P1).
- Intent executor startup does not double-spawn goroutines (minor cleanup; behavior unchanged).
- Control-plane wiring moved into `bot/*` controllers + `ControlFlags` (reduces entrypoint-specific state).
- Strategy width calculation + RPC state wiring were moved into `bot/*` helpers (further reduces entrypoint bloat).
- Strategy construction/wiring moved into `bot/*` helpers (keeps `cmd/` closer to wiring-only).
- Token price cache helpers moved into `bot/*` (removes more entrypoint-local logic).
- API preview balance reader is now wired to support (a) live gateways, (b) offline fake balances for rehearsals, (c) RPC read-only balance reads via `BOT_WALLET_ADDRESS` (keeps preview usable without private keys).
- Pool runtime state + mint guards + DEX pool watchers + position sync are centralized in `bot/*` (removes duplicate implementations from `cmd/bot/main.go` and reduces boundary erosion risk).
- Position close/drain/mint receipt parsing + intent step recording are centralized in `bot/*` (further de-bloats `cmd/bot/main.go` and avoids duplicate execution helpers).
- Intent executor is now in `bot/intent_executor.go` behind a DI surface (`IntentExecutorDeps`); `cmd/bot/main.go` only wires dependencies + starts the goroutine (reduces entrypoint bloat and makes executor testable in isolation).
- Boundary scan gate exists in `make ci` (prevents `internal/*` importing `cmd/*` or `bot/*`).

## 2) Risk List (P0/P1/P2)

### P0 — Funds / execution safety

- **Unsafe defaults**: any default that can broadcast tx or bypass kill-switch is unacceptable.
  - Status: mitigated via safe defaults + triple-unlock checks (see `internal/config/config.go`).
- **Secret leakage**: keys/tokens must never be committed or printed.
  - Status: mitigated via `scripts/secret_scan.sh` included in `make ci`.
- **Mainnet mis-targeting**: Arbitrum One (42161) must be blocked by default.
  - Status: blocked unless `PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1`.

### P1 — Coupling / boundary erosion

- **Entrypoint bloat** (`cmd/bot/main.go`): orchestration, state wiring, and execution helpers can accrete into an unreviewable “UI-driven core”.
  - Fix path: continue extracting orchestration helpers into `bot/*`, keep pure domain logic in `internal/*`.
- **Global runtime state**: in-memory snapshots and guards can introduce implicit coupling + race risk.
  - Fix path: keep accessors narrow, add tests for concurrency invariants, avoid `internal/*` depending on bot state.

### P2 — Test coverage / drift

- Many packages are `no test files`; without a minimum contract suite this drifts silently.
  - Fix path: keep `make ci` green and extend targeted handler+contract tests when endpoints grow.

## 3) Docs vs Code Drift (contract checks)

Contract principle: **if it isn’t in `docs/`, it doesn’t exist**.

Current enforcement:

- Route presence checks: `internal/api/docs_contract_test.go`
- Read contract shape: `internal/api/contract_v1_test.go`

Known drift hotspots to watch:

- Response shapes for list endpoints (`/tx`, `/audit`) and SSE keepalive semantics.
- Any new endpoint must update `docs/api/*` first, then code + tests.

## 4) Phase Plan (small PR cadence)

Phase 0 — Safety & build gates
- `make ci` (Go fmt/vet/test + secret scan + web build)
- deterministic rehearsal scripts (offline/testnet dry-run)
- boundary gate: `scripts/boundary_scan.sh` (fails CI on layer violations)

Phase 1 — Boundary refactors
- reduce `cmd/*` responsibility; move orchestration to `bot/*`
- abstract external dependencies behind interfaces; mock in tests

Phase 2 — API contract alignment
- `/api/v1/*` DTOs only; never expose `internal/*` structs
- handler tests: success + error path per endpoint

Phase 3 — Dry-run + testnet
- dry-run default true; kill-switch default true
- broadcast requires explicit unlock + audit trail
- testnet runbook + optional broadcast probe
- contract preflight: `scripts/check_contract_code.sh` to avoid wrong-chain address config

Phase 4 — Web minimal viable
- read-only by default + mock mode
- build must pass without private endpoints/secrets
