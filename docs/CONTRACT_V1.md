# Contract v1

Contract v1 provides a minimal, **add-only** JSON schema for cross-repo observability and UI.

## Rules

- `schema_version` is always `"v1"`.
- Fields are **add-only** (new fields may be added; existing fields must not be removed/renamed).
- Enums are **add-only** (new enum values may be added; existing values must not change).
- Timestamps use `*_ms` as `int64` (milliseconds).
- Amount/price/quantity values use **string** (decimal) when applicable.
- Enums are encoded as JSON strings.

## Types

- `IntentV1`: describes an action that may be executed (or simulated).
- `RiskDecisionV1`: risk gate decision for an intent/run.
- `ExecutorResultV1`: result/receipt after attempting execution (or simulation).
- `StatusV1`: current bot/runner status snapshot.

## Enums

See `internal/shared/contract/v1/types.go` for the complete list:

- `IntentType`
- `RiskLevel`
- `ExecutionStatus`
- `ErrorKind`
- `Mode`
- `State`

