# Contract v1 (single source of truth)

This module is the single source of truth for **Contract v1** types used by Phoenix-V3 and Alcova-X.

## Rules

- `schema_version` is always `"v1"`.
- **Add-only**: fields and enum values may only be added; never removed/renamed/repurposed.
- Timestamps use `*_ms` as `int64` milliseconds.
- Amount/price/quantity values use **string** (decimal) when applicable.
- Enums are encoded as JSON strings.
- `execution_status` uses `CANCELED` (single-L). Do not introduce `CANCELLED` (double-L).
- `execution_status` may be `SKIPPED` for non-error terminations (e.g. intent expired, risk gate blocked, or strategy decided no-op).

## Types

- `IntentV1`
- `RiskDecisionV1`
- `ExecutorResultV1`
- `StatusV1`
