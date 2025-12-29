#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts var

OVERRIDE_BPS="${OVERRIDE_BPS:-500}"
DURATION_SEC=21600  # 6 hours
SAMPLE_SEC="${SAMPLE_SEC:-60}"

ts_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

echo "=== Phase 5.8: 6h Risk Soak Retest Comparison ==="
echo "ts_utc=$(ts_utc)"
echo "duration_per_run=6h (21600s)"
echo "override_bps=$OVERRIDE_BPS"
echo

# Run A: Default threshold (100 bps)
echo "=== Run A: Default Threshold (100 bps) ==="
echo "start_time=$(ts_utc)"
export DURATION_SEC_OVERRIDE="$DURATION_SEC"
export SAMPLE_SEC="$SAMPLE_SEC"
export RISK_STATS_PATH="var/phase5_8_retest6h_default_stats.json"
export RISK_STATE_PATH="var/phase5_8_retest6h_default_state.json"
unset RISK_PRICE_DIVERGENCE_MAX_BPS_OVERRIDE

# Clean up previous run artifacts
rm -f "$RISK_STATS_PATH" "$RISK_STATE_PATH" || true

# Reuse existing retest infrastructure
./scripts/phase5_4_risk_soak_retest_2h.sh

# Move outputs to phase5_8 naming
mv artifacts/phase5_4_risk_soak_retest_2h.txt artifacts/phase5_8_retest6h_default.txt || true
mv artifacts/phase5_4_risk_soak_retest_2h_summary.json artifacts/phase5_8_retest6h_default_summary.json || true

echo "end_time=$(ts_utc)"
echo "✓ Run A complete"
echo

# Run B: Override threshold
echo "=== Run B: Override Threshold ($OVERRIDE_BPS bps) ==="
echo "start_time=$(ts_utc)"
export DURATION_SEC_OVERRIDE="$DURATION_SEC"
export SAMPLE_SEC="$SAMPLE_SEC"
export RISK_STATS_PATH="var/phase5_8_retest6h_override_stats.json"
export RISK_STATE_PATH="var/phase5_8_retest6h_override_state.json"
export RISK_PRICE_DIVERGENCE_MAX_BPS_OVERRIDE="$OVERRIDE_BPS"

# Clean up previous run artifacts
rm -f "$RISK_STATS_PATH" "$RISK_STATE_PATH" || true

# Reuse existing retest infrastructure
./scripts/phase5_4_risk_soak_retest_2h.sh

# Move outputs to phase5_8 naming
mv artifacts/phase5_4_risk_soak_retest_2h.txt artifacts/phase5_8_retest6h_override.txt || true
mv artifacts/phase5_4_risk_soak_retest_2h_summary.json artifacts/phase5_8_retest6h_override_summary.json || true

echo "end_time=$(ts_utc)"
echo "✓ Run B complete"
echo

# Generate comparison report
echo "=== Generating Comparison Report ==="
python3 - <<'PY'
import json, os, sys

def load_json(path):
  if not os.path.exists(path):
    return {}
  with open(path, "r") as f:
    return json.load(f)

default_summary = load_json("artifacts/phase5_8_retest6h_default_summary.json")
override_summary = load_json("artifacts/phase5_8_retest6h_override_summary.json")

def get_val(d, *keys, default=0):
  for k in keys:
    if isinstance(d, dict):
      d = d.get(k, {})
    else:
      return default
  return d if d != {} else default

# Extract key metrics
default_total = get_val(default_summary, "total_evaluations")
override_total = get_val(override_summary, "total_evaluations")

default_reject = get_val(default_summary, "verdict_counts", "REJECT")
override_reject = get_val(override_summary, "verdict_counts", "REJECT")

default_skip = get_val(default_summary, "verdict_counts", "SKIP")
override_skip = get_val(override_summary, "verdict_counts", "SKIP")

default_approve = default_total - default_reject - default_skip
override_approve = override_total - override_reject - override_skip

default_div_reject = get_val(default_summary, "reject_counts_by_rule_id", "price_source_divergence")
override_div_reject = get_val(override_summary, "reject_counts_by_rule_id", "price_source_divergence")

default_div_skip = get_val(default_summary, "skip_counts_by_rule_id", "price_source_divergence")
override_div_skip = get_val(override_summary, "skip_counts_by_rule_id", "price_source_divergence")

default_tm_skip = get_val(default_summary, "time_mismatch_skip_count")
override_tm_skip = get_val(override_summary, "time_mismatch_skip_count")

default_tm_rate = get_val(default_summary, "time_mismatch_skip_rate", default=0.0)
override_tm_rate = get_val(override_summary, "time_mismatch_skip_rate", default=0.0)

default_div_stats = get_val(default_summary, "price_divergence_stats", default={})
override_div_stats = get_val(override_summary, "price_divergence_stats", default={})

default_absurd = get_val(default_summary, "absurd_deviation_count")
override_absurd = get_val(override_summary, "absurd_deviation_count")

# Calculate deltas
delta_total = override_total - default_total
delta_reject = override_reject - default_reject
delta_skip = override_skip - default_skip
delta_approve = override_approve - default_approve
delta_div_reject = override_div_reject - default_div_reject
delta_tm_skip = override_tm_skip - default_tm_skip

# Generate conclusion
reject_to_approve = abs(delta_reject) if delta_reject < 0 else 0
skip_still_dominant = override_skip > override_approve if override_total > 0 else False
override_increases_approvals = delta_approve > 0

conclusion_text = ""
if override_increases_approvals:
  if reject_to_approve > 0:
    conclusion_text = f"Override primarily converts REJECT→APPROVE ({reject_to_approve} fewer rejects, {delta_approve} more approves)"
  else:
    conclusion_text = "Override increases approvals but not by reducing rejects (may be reducing skips or other effects)"
else:
  conclusion_text = "Override does not significantly increase approvals"

if skip_still_dominant:
  conclusion_text += "; SKIP still dominant (time_mismatch or other skip reasons)"
else:
  conclusion_text += "; APPROVE now dominant"

if default_absurd > 0 or override_absurd > 0:
  conclusion_text += f" ⚠️ ABSURD DEVIATION DETECTED (default={default_absurd}, override={override_absurd})"

# Build report
report = {
  "comparison_timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "override_bps": int(os.getenv("OVERRIDE_BPS", "500")),
  "default": {
    "total_evaluations": default_total,
    "verdict_counts": get_val(default_summary, "verdict_counts", default={}),
    "reject_counts_by_rule_id": get_val(default_summary, "reject_counts_by_rule_id", default={}),
    "skip_counts_by_rule_id": get_val(default_summary, "skip_counts_by_rule_id", default={}),
    "time_mismatch_skip_count": default_tm_skip,
    "time_mismatch_skip_rate": default_tm_rate,
    "price_divergence_stats": default_div_stats,
    "absurd_deviation_count": default_absurd
  },
  "override": {
    "total_evaluations": override_total,
    "verdict_counts": get_val(override_summary, "verdict_counts", default={}),
    "reject_counts_by_rule_id": get_val(override_summary, "reject_counts_by_rule_id", default={}),
    "skip_counts_by_rule_id": get_val(override_summary, "skip_counts_by_rule_id", default={}),
    "time_mismatch_skip_count": override_tm_skip,
    "time_mismatch_skip_rate": override_tm_rate,
    "price_divergence_stats": override_div_stats,
    "absurd_deviation_count": override_absurd
  },
  "delta": {
    "total_evaluations_diff": delta_total,
    "reject_count_diff": delta_reject,
    "skip_count_diff": delta_skip,
    "approve_count_diff": delta_approve,
    "price_divergence_reject_diff": delta_div_reject,
    "time_mismatch_skip_diff": delta_tm_skip
  },
  "conclusion": {
    "override_increases_approvals": override_increases_approvals,
    "reject_to_approve_conversion": reject_to_approve,
    "skip_still_dominant": skip_still_dominant,
    "recommendation": conclusion_text
  }
}

# Write JSON report
with open("artifacts/phase5_8_retest6h_compare_report.json", "w") as f:
  json.dump(report, f, indent=2, sort_keys=True)

# Write TXT report
with open("artifacts/phase5_8_retest6h_compare_report.txt", "w") as f:
  f.write("=== Phase 5.8: 6h Risk Soak Retest Comparison Report ===\n")
  f.write(f"Generated: {report['comparison_timestamp']}\n")
  f.write(f"Override Threshold: {report['override_bps']} bps\n")
  f.write("\n")
  
  f.write("=== Run A: Default Threshold (100 bps) ===\n")
  f.write(f"Total Evaluations: {default_total}\n")
  f.write(f"REJECT: {default_reject}\n")
  f.write(f"SKIP: {default_skip}\n")
  f.write(f"APPROVE: {default_approve}\n")
  f.write(f"Price Divergence REJECTs: {default_div_reject}\n")
  f.write(f"Price Divergence SKIPs: {default_div_skip}\n")
  f.write(f"Time Mismatch SKIPs: {default_tm_skip} (rate={default_tm_rate:.4f})\n")
  f.write(f"Absurd Deviation Count: {default_absurd}\n")
  f.write("\n")
  
  f.write("=== Run B: Override Threshold ({} bps) ===\n".format(report['override_bps']))
  f.write(f"Total Evaluations: {override_total}\n")
  f.write(f"REJECT: {override_reject}\n")
  f.write(f"SKIP: {override_skip}\n")
  f.write(f"APPROVE: {override_approve}\n")
  f.write(f"Price Divergence REJECTs: {override_div_reject}\n")
  f.write(f"Price Divergence SKIPs: {override_div_skip}\n")
  f.write(f"Time Mismatch SKIPs: {override_tm_skip} (rate={override_tm_rate:.4f})\n")
  f.write(f"Absurd Deviation Count: {override_absurd}\n")
  f.write("\n")
  
  f.write("=== Delta (Override - Default) ===\n")
  f.write(f"Total Evaluations: {delta_total:+d}\n")
  f.write(f"REJECT: {delta_reject:+d}\n")
  f.write(f"SKIP: {delta_skip:+d}\n")
  f.write(f"APPROVE: {delta_approve:+d}\n")
  f.write(f"Price Divergence REJECTs: {delta_div_reject:+d}\n")
  f.write(f"Time Mismatch SKIPs: {delta_tm_skip:+d}\n")
  f.write("\n")
  
  f.write("=== Conclusion ===\n")
  f.write(f"{conclusion_text}\n")
  f.write("\n")
  
  if default_absurd == 0 and override_absurd == 0:
    f.write("✓ No absurd deviations detected in either run\n")
  else:
    f.write(f"⚠️ ABSURD DEVIATIONS: default={default_absurd}, override={override_absurd}\n")

print("✓ Comparison report generated:")
print("  - artifacts/phase5_8_retest6h_compare_report.json")
print("  - artifacts/phase5_8_retest6h_compare_report.txt")
print()
print("=== Quick Summary ===")
print(f"Default: {default_total} evals, {default_reject} REJ, {default_skip} SKIP, {default_approve} APP")
print(f"Override: {override_total} evals, {override_reject} REJ, {override_skip} SKIP, {override_approve} APP")
print(f"Delta: {delta_reject:+d} REJ, {delta_skip:+d} SKIP, {delta_approve:+d} APP")
print()
print(f"Conclusion: {conclusion_text}")
PY

echo
echo "=== Phase 5.8: 6h Comparison Complete ==="
echo "Output files:"
echo "  - artifacts/phase5_8_retest6h_default.txt"
echo "  - artifacts/phase5_8_retest6h_default_summary.json"
echo "  - artifacts/phase5_8_retest6h_override.txt"
echo "  - artifacts/phase5_8_retest6h_override_summary.json"
echo "  - artifacts/phase5_8_retest6h_compare_report.json"
echo "  - artifacts/phase5_8_retest6h_compare_report.txt"
echo
echo "To view comparison report:"
echo "  cat artifacts/phase5_8_retest6h_compare_report.txt"
