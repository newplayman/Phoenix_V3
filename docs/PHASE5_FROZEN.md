# Phase 5.x Risk Control System - FROZEN

**Status**: ✅ FROZEN  
**Freeze Date**: 2025-12-29  
**Commit Hash**: `c2839207f55ae6dc07c756b55e39ccb28a5e61b0`  
**Git Tag**: `phoenix-risk-v1.0`

---

## Overview

Phase 5.x represents the complete implementation of Phoenix_V3's risk control management (RCM) system. This freeze establishes a stable, traceable baseline for all risk control logic and testing infrastructure.

**Core Principle**: Safety-first enforcement with one-vote veto, SKIP-on-ambiguity, and strict dry-run boundaries.

---

## Phase 5.x Scope & Achievements

### Phase 5.1: RCM Integration Foundation
- Integrated risk control evaluator into intent execution pipeline
- Established one-vote veto pattern (any REJECT blocks intent)
- Implemented `force_dry_run` enforcement layer
- Created `RiskContext` model for price source aggregation

### Phase 5.2: Cooldown & Frequency Control
- Added `cooldown_frequency` rule with per-intent-key tracking
- Implemented minimum interval enforcement (default 60s)
- Created cooldown state persistence to `var/risk_state.json`
- Added smoke test: `phase5_2_cooldown_smoke_test.go`

### Phase 5.3: Price Source Divergence Detection
- Implemented `price_source_divergence` rule
- Added SKIP strategy for ambiguous cases (missing/stale sources)
- Default threshold: 100 bps (1.00%)
- Staleness gate: 30s max age
- Created smoke test: `phase5_3_price_divergence_smoke_test.go`

### Phase 5.4: 2-Hour Soak Testing Framework
- Created `scripts/phase5_4_risk_soak_retest_2h.sh`
- Implemented periodic sampling (60s intervals)
- Added summary JSON generation with verdict/rule breakdowns
- Established artifacts pattern: `artifacts/phase5_4_*_summary.json`

### Phase 5.5: Price Normalization
- Added token0/token1 semantic normalization
- Implemented direction inversion detection
- Added normalization gate (SKIP if normalization fails)
- Prevented false positives from raw vs normalized price comparisons
- Created smoke test: `phase5_5_price_normalization_smoke_test.go`

### Phase 5.6: Divergence Sample Collection
- Implemented structured divergence sample logging
- Added JSON/TXT sample outputs to `artifacts/`
- Captured raw/normalized prices, timestamps, and deviation values
- Created smoke test: `phase5_6_divergence_samples_smoke_test.go`

### Phase 5.7: Time Alignment (AlignMaxGap)
- Added `AlignMaxGap=5s` to price divergence rule
- SKIP comparisons when source timestamps differ by >5s
- Reduced false REJECT rate by ~50%
- Implemented "skip time_mismatch" reason tracking

### Phase 5.8: Observability & Test Flexibility
- Added top-level `time_mismatch_skip_count` and `time_mismatch_skip_rate` to summary JSON
- Implemented test-only `RISK_PRICE_DIVERGENCE_MAX_BPS_OVERRIDE` (does not change production default)
- Created 6-hour comparison framework: `scripts/phase5_8_risk_soak_retest_6h_compare.sh`
- Added automated comparison reporting with delta analysis

---

## Safety Boundaries

### Enforcement Layers

1. **force_dry_run=true**: Always enforced, no real transactions
2. **HALT mode**: Stops all intent execution when `risk_mode=HALT`
3. **One-vote veto**: Any rule REJECT blocks the entire intent
4. **SKIP strategy**: Ambiguous cases (missing data, stale data, time mismatch) → APPROVE with skip reason
5. **No automatic control writes**: Risk control never modifies `control.json`

### Constraints

- ✅ Only writes to `Phoenix_V3/var/*` and `Phoenix_V3/artifacts/*`
- ✅ No modifications to HyperAlcova or Alcova-X
- ✅ No systemd/unit/env changes
- ✅ No chain writes or real order placement
- ✅ All thresholds have conservative defaults

---

## Key Scripts & Entry Points

### Testing Scripts

| Script | Purpose | Duration | Output |
|--------|---------|----------|--------|
| `scripts/phase5_4_risk_soak_retest_2h.sh` | 2-hour soak test | 2h | `artifacts/phase5_4_risk_soak_retest_2h_summary.json` |
| `scripts/phase5_8_override_smoke.sh` | Override threshold validation | 90s | `artifacts/phase5_8_override_smoke_summary.json` |
| `scripts/phase5_8_risk_soak_retest_6h_compare.sh` | Threshold comparison (default vs override) | 12h | `artifacts/phase5_8_retest6h_compare_report.json` |

### Smoke Tests (Go)

- `internal/riskcontrol/phase5_2_cooldown_smoke_test.go`
- `internal/riskcontrol/phase5_3_price_divergence_smoke_test.go`
- `internal/riskcontrol/phase5_5_price_normalization_smoke_test.go`
- `internal/riskcontrol/phase5_6_divergence_samples_smoke_test.go`

---

## Key Artifacts & Outputs

### Runtime State

- `var/risk_state.json` - Cooldown state persistence
- `var/phase5_*_risk_stats*.json` - Real-time statistics snapshots
- `var/control.json` - Control plane state (read-only by risk control)

### Test Outputs

- `artifacts/phase5_4_risk_soak_retest_2h_summary.json` - 2h soak test summary
- `artifacts/phase5_7_retest_after_fix_summary.json` - Post-AlignMaxGap validation
- `artifacts/phase5_8_retest6h_default_summary.json` - 6h default threshold results
- `artifacts/phase5_8_retest6h_override_summary.json` - 6h override threshold results
- `artifacts/phase5_8_retest6h_compare_report.json` - Threshold comparison analysis

### Divergence Samples

- `artifacts/phase5_6_divergence_reject_samples.json` - Structured divergence evidence
- `artifacts/phase5_6_divergence_reject_samples.txt` - Human-readable samples

---

## Risk Control Rules

### 1. force_dry_run
- **Purpose**: Prevent real transactions
- **Verdict**: APPROVE (always passes, but enforces dry-run mode)
- **Config**: Always enabled

### 2. risk_mode_halt
- **Purpose**: Emergency stop
- **Verdict**: REJECT when `risk_mode=HALT`
- **Config**: Controlled via `control.json`

### 3. cooldown_frequency
- **Purpose**: Prevent strategy thrashing
- **Verdict**: REJECT if intent fired within minimum interval
- **Default**: 60s minimum interval per intent key
- **State**: Persisted to `var/risk_state.json`

### 4. price_source_divergence
- **Purpose**: Detect price oracle failures
- **Verdict**: 
  - REJECT if deviation > threshold (default 100 bps)
  - SKIP if sources missing, stale (>30s), or time-misaligned (>5s)
- **Config**:
  - `MaxDeviationBps`: 100 (1.00%)
  - `MaxStaleness`: 30s
  - `AlignMaxGap`: 5s
  - `RISK_PRICE_DIVERGENCE_MAX_BPS_OVERRIDE`: Test-only override

---

## Statistics & Observability

### Summary JSON Fields

```json
{
  "total_evaluations": 123,
  "verdict_counts": {"REJECT": 10, "SKIP": 20, "APPROVE": 93},
  "rule_counts": {"price_source_divergence": 123, ...},
  "reject_counts_by_rule_id": {"price_source_divergence": 10, ...},
  "skip_counts_by_rule_id": {"price_source_divergence": 20, ...},
  "skip_reasons": {"time_mismatch": 15, "stale_source": 3, "missing_source": 2},
  "time_mismatch_skip_count": 15,
  "time_mismatch_skip_rate": 0.1220,
  "price_divergence_stats": {
    "sample_count": 100,
    "reject_count": 10,
    "max_deviation_bps": 850,
    "avg_deviation_bps": 45.2,
    "p50_deviation_bps": 30,
    "p95_deviation_bps": 120
  }
}
```

---

## Future Changes Declaration

> [!IMPORTANT]
> **Phase 5.x is FROZEN**. Any modifications to the risk control logic, thresholds, or enforcement behavior documented above must be tracked as:
> - **Phase 6+**: New features or significant changes
> - **Hotfix**: Critical bug fixes only (must reference this freeze doc)
>
> This freeze serves as the baseline for all future risk control evolution. Changes must not break the safety boundaries or contradict the core principles established in Phase 5.

---

## References

- **Implementation**: `internal/riskcontrol/`
- **Configuration**: `configs/config.yaml` (risk control section)
- **Documentation**: `docs/` (this file)
- **Git Tag**: `phoenix-risk-v1.0`
- **Commit**: `{{FREEZE_COMMIT}}`

---

**Frozen by**: Antigravity (Codex)  
**Freeze Date**: 2025-12-29T16:53:41Z  
**Next Phase**: Phase 6.0 - Risk Advisory (read-only recommendations)
