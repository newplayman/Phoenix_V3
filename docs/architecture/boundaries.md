# Architecture Boundaries (Contract-First)

Phoenix_V3 follows a layered boundary model:

- `cmd/*`: entrypoints only (wires config, starts services, no domain logic)
- `bot/*`: orchestration/runtime (loops, schedulers, in-memory runtime snapshots)
- `bot/*` also owns best-effort read-only chain synchronizers (e.g. pool watchers, position sync) used to keep read APIs accurate.
- `internal/*`: core domain logic (strategy, risk, execution, storage, chain adapters)
- `docs/*`: **source of truth** for API/web contracts
- `web/*`: API consumer only (read-only by default)

## Non-negotiables

- `web/*` must not import `internal/*` or access RPC/DB directly.
- APIs exist only if documented in `docs/api/*` and must match documented schemas.
- Execution/broadcast is off by default; requires explicit config unlock and must not be triggered by Web.
- `internal/*` must not import `cmd/*` or `bot/*` (entrypoints + orchestration must not leak into core).

## Event Flow

- Bot produces observational events (e.g. pool-state snapshots) into `internal/events` streams.
- Consumers (API, monitor) treat events as data; they do not infer execution outcomes without explicit backend confirmation.
