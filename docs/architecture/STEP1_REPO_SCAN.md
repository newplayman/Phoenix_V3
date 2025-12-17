# Step 1 — Repo Quick Scan (Read-Only Findings)

This document records the initial “quick scan” required by `AGENTS.md` (no code changes), so later phases can be reviewed against a stable baseline.

## Entrypoints (`cmd/*`)

Executables discovered via `find cmd -name main.go`:

- `cmd/bot` — main runtime (API + monitor + strategy loop + intent execution; must stay “wiring-only” over time).
- `cmd/configcheck` — config validation (`configs/*.yaml`).
- `cmd/redisdev` — local-only Redis Streams subset server for rehearsals (no external deps).
- `cmd/replay` — Redis stream replay/export helper.
- `cmd/replayfile` — file stream (jsonl) replay helper.
- `cmd/txprobe` — **testnet-only** broadcast probe (0-value tx to self; triple-unlock required; safe by default).

## Orchestration (`bot/*`)

Key responsibilities (must not leak into `internal/*`):

- runtime pool snapshots + mint guards
- DEX pool watchers + position sync (read-only chain calls)
- intent step recording + receipt parsing helpers
- intent execution loop (consumes intents, enforces risk gates, invokes adapters)

## Domain + Adapters (`internal/*`)

Expected responsibilities:

- domain: strategy, risk, rebalancer math/types, storage, monitoring
- adapters: chain gateway, univ3 ABIs/router/quoter, events stream drivers

## Web (`web/*`)

- Build tool: Vite (`npm -C web ci`, `npm -C web run build`).
- Contract: web is **read-only consumer** in `experimental` phase (see `docs/web/WEB_CONSOLE_FRONTEND_SPEC.md`).
- Guardrail: boundary scan asserts web does not reference control endpoints and does not import Go internals.

## Contract Source of Truth (`docs/*`)

- API contract: `docs/api/API_AND_EVENT_SPEC.md` (+ `docs/api/CONTROL_PLANE_BACKEND_SPEC.md`)
- Runbook/DoD: `docs/runbook/testnet.md`, `docs/runbook/definition_of_done.md`
- Architecture boundaries: `docs/architecture/boundaries.md`

## Current “Shit-Mountain” Risk Summary

See `docs/architecture/STACK_HEALTH_REPORT.md` for:

- dependency graph and boundary invariants
- P0/P1/P2 risk list
- docs-vs-code drift enforcement notes

