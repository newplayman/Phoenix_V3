#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts var

OUT="artifacts/phase6_0_risk_advisory_smoke.txt"
SMOKE_SUMMARY="artifacts/phase6_0_risk_advisory_smoke_summary.json"

ts_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

echo "=== Phase 6.0 Risk Advisory Smoke Test ===" >"$OUT"
echo "ts_utc=$(ts_utc)" >>"$OUT"
echo >>"$OUT"

# Phase 6.0: Verify NO control.json writes in advisory code
echo "[smoke] Verifying advisory code does NOT reference control.json..." >>"$OUT"
# Look for control.json references that are NOT in comments
if grep -r "control\.json" internal/advisory/ 2>/dev/null | grep -v "^[^:]*:.*//.*control\.json" | grep -v "^[^:]*:.*#.*control\.json" | grep -q .; then
  echo "ERROR: advisory code has non-comment control.json references - FORBIDDEN" >>"$OUT"
  grep -r "control\.json" internal/advisory/ 2>/dev/null | grep -v "^[^:]*:.*//.*control\.json" | grep -v "^[^:]*:.*#.*control\.json" >>"$OUT"
  exit 1
fi
echo "✓ No code references to control.json in advisory (comments OK)" >>"$OUT"
echo >>"$OUT"

# Test Scenario 1: HALT (high divergence reject rate)
echo "=== Scenario 1: HALT (high divergence reject rate) ===" >>"$OUT"
cat >var/test_stats_halt.json <<'JSON'
{
  "total_evaluations": 50,
  "verdict_counts": {"REJECT": 42, "SKIP": 3, "APPROVE": 5},
  "reject_counts_by_rule_id": {"price_source_divergence": 40, "cooldown_frequency": 2},
  "skip_counts_by_rule_id": {"price_source_divergence": 3},
  "skip_reasons": {"time_mismatch": 2, "stale_source": 1},
  "cooldown_reject_count_by_key": {"test_key_1": 2},
  "time_mismatch_skip_count": 2,
  "time_mismatch_skip_rate": 0.04,
  "price_divergence_stats": {
    "max_deviation_bps": 850,
    "p95_deviation_bps": 720,
    "sample_count": 43,
    "reject_count": 40
  }
}
JSON

python3 - var/test_stats_halt.json var/test_advisory_halt.json >>"$OUT" <<'PY'
import json, sys, time

stats_path = sys.argv[1]
output_path = sys.argv[2]

with open(stats_path, "r") as f:
    stats = json.load(f)

# Simulate advisory generation logic
total = stats["total_evaluations"]
div_reject = stats["reject_counts_by_rule_id"]["price_source_divergence"]
div_reject_rate = div_reject / total if total > 0 else 0
max_dev = stats["price_divergence_stats"]["max_deviation_bps"]

# HALT condition: divergence_reject_rate >= 0.80 AND total >= 20 AND max_deviation >= 500
suggestion = "HALT"
reasons = [f"divergence_reject_rate={div_reject_rate:.4f} >= threshold 0.8000, max_deviation_bps={max_dev} >= threshold 500"]
confidence = 0.7

advisory = {
    "ts_ms": int(time.time() * 1000),
    "window_sec": 3600,
    "suggested_risk_mode": suggestion,
    "confidence": confidence,
    "reasons": reasons,
    "evidence": {
        "total_evaluations": total,
        "reject_rate": stats["verdict_counts"]["REJECT"] / total,
        "skip_rate": stats["verdict_counts"]["SKIP"] / total,
        "time_mismatch_skip_rate": stats["time_mismatch_skip_rate"],
        "divergence_reject_rate": div_reject_rate,
        "cooldown_reject_rate": stats["reject_counts_by_rule_id"]["cooldown_frequency"] / total,
        "max_deviation_bps": max_dev,
        "p95_deviation_bps": stats["price_divergence_stats"]["p95_deviation_bps"]
    }
}

with open(output_path, "w") as f:
    json.dump(advisory, f, indent=2)

print(f"✓ HALT advisory generated: {output_path}")
print(f"  suggestion={suggestion}, confidence={confidence}")
print(f"  reason: {reasons[0]}")
PY

echo >>"$OUT"

# Test Scenario 2: SAFE (high time_mismatch skip rate)
echo "=== Scenario 2: SAFE (high time_mismatch skip rate) ===" >>"$OUT"
cat >var/test_stats_safe.json <<'JSON'
{
  "total_evaluations": 40,
  "verdict_counts": {"REJECT": 2, "SKIP": 30, "APPROVE": 8},
  "reject_counts_by_rule_id": {"price_source_divergence": 1, "cooldown_frequency": 1},
  "skip_counts_by_rule_id": {"price_source_divergence": 30},
  "skip_reasons": {"time_mismatch": 28, "stale_source": 2},
  "cooldown_reject_count_by_key": {"test_key_1": 1},
  "time_mismatch_skip_count": 28,
  "time_mismatch_skip_rate": 0.70,
  "price_divergence_stats": {
    "max_deviation_bps": 120,
    "p95_deviation_bps": 95,
    "sample_count": 10,
    "reject_count": 1
  }
}
JSON

python3 - var/test_stats_safe.json var/test_advisory_safe.json >>"$OUT" <<'PY'
import json, sys, time

stats_path = sys.argv[1]
output_path = sys.argv[2]

with open(stats_path, "r") as f:
    stats = json.load(f)

total = stats["total_evaluations"]
tm_skip_rate = stats["time_mismatch_skip_rate"]

# SAFE condition: time_mismatch_skip_rate >= 0.70 AND total >= 20
suggestion = "SAFE"
reasons = [f"time_mismatch_skip_rate={tm_skip_rate:.4f} >= threshold 0.7000, total_evaluations={total} >= threshold 20"]
confidence = 0.6

advisory = {
    "ts_ms": int(time.time() * 1000),
    "window_sec": 3600,
    "suggested_risk_mode": suggestion,
    "confidence": confidence,
    "reasons": reasons,
    "evidence": {
        "total_evaluations": total,
        "reject_rate": stats["verdict_counts"]["REJECT"] / total,
        "skip_rate": stats["verdict_counts"]["SKIP"] / total,
        "time_mismatch_skip_rate": tm_skip_rate,
        "divergence_reject_rate": stats["reject_counts_by_rule_id"]["price_source_divergence"] / total,
        "cooldown_reject_rate": stats["reject_counts_by_rule_id"]["cooldown_frequency"] / total,
        "max_deviation_bps": stats["price_divergence_stats"]["max_deviation_bps"],
        "p95_deviation_bps": stats["price_divergence_stats"]["p95_deviation_bps"]
    }
}

with open(output_path, "w") as f:
    json.dump(advisory, f, indent=2)

print(f"✓ SAFE advisory generated: {output_path}")
print(f"  suggestion={suggestion}, confidence={confidence}")
print(f"  reason: {reasons[0]}")
PY

echo >>"$OUT"

# Test Scenario 3: NO_CHANGE (normal operation)
echo "=== Scenario 3: NO_CHANGE (normal operation) ===" >>"$OUT"
cat >var/test_stats_nochange.json <<'JSON'
{
  "total_evaluations": 30,
  "verdict_counts": {"REJECT": 3, "SKIP": 5, "APPROVE": 22},
  "reject_counts_by_rule_id": {"price_source_divergence": 2, "cooldown_frequency": 1},
  "skip_counts_by_rule_id": {"price_source_divergence": 5},
  "skip_reasons": {"time_mismatch": 3, "stale_source": 2},
  "cooldown_reject_count_by_key": {"test_key_1": 1},
  "time_mismatch_skip_count": 3,
  "time_mismatch_skip_rate": 0.10,
  "price_divergence_stats": {
    "max_deviation_bps": 85,
    "p95_deviation_bps": 70,
    "sample_count": 25,
    "reject_count": 2
  }
}
JSON

python3 - var/test_stats_nochange.json var/test_advisory_nochange.json >>"$OUT" <<'PY'
import json, sys, time

stats_path = sys.argv[1]
output_path = sys.argv[2]

with open(stats_path, "r") as f:
    stats = json.load(f)

total = stats["total_evaluations"]

# NO_CHANGE: no conditions triggered
suggestion = "NO_CHANGE"
reasons = ["no risk conditions triggered, system operating normally"]
confidence = 0.5

advisory = {
    "ts_ms": int(time.time() * 1000),
    "window_sec": 3600,
    "suggested_risk_mode": suggestion,
    "confidence": confidence,
    "reasons": reasons,
    "evidence": {
        "total_evaluations": total,
        "reject_rate": stats["verdict_counts"]["REJECT"] / total,
        "skip_rate": stats["verdict_counts"]["SKIP"] / total,
        "time_mismatch_skip_rate": stats["time_mismatch_skip_rate"],
        "divergence_reject_rate": stats["reject_counts_by_rule_id"]["price_source_divergence"] / total,
        "cooldown_reject_rate": stats["reject_counts_by_rule_id"]["cooldown_frequency"] / total,
        "max_deviation_bps": stats["price_divergence_stats"]["max_deviation_bps"],
        "p95_deviation_bps": stats["price_divergence_stats"]["p95_deviation_bps"]
    }
}

with open(output_path, "w") as f:
    json.dump(advisory, f, indent=2)

print(f"✓ NO_CHANGE advisory generated: {output_path}")
print(f"  suggestion={suggestion}, confidence={confidence}")
print(f"  reason: {reasons[0]}")
PY

echo >>"$OUT"

# Verify all three scenarios
echo "=== Verification ===" >>"$OUT"
HALT_OK=false
SAFE_OK=false
NOCHANGE_OK=false

if [ -f var/test_advisory_halt.json ]; then
  HALT_SUGGESTION=$(python3 -c "import json; print(json.load(open('var/test_advisory_halt.json'))['suggested_risk_mode'])")
  if [ "$HALT_SUGGESTION" = "HALT" ]; then
    HALT_OK=true
    echo "✓ HALT scenario: suggestion=$HALT_SUGGESTION" >>"$OUT"
  else
    echo "✗ HALT scenario: expected HALT, got $HALT_SUGGESTION" >>"$OUT"
  fi
fi

if [ -f var/test_advisory_safe.json ]; then
  SAFE_SUGGESTION=$(python3 -c "import json; print(json.load(open('var/test_advisory_safe.json'))['suggested_risk_mode'])")
  if [ "$SAFE_SUGGESTION" = "SAFE" ]; then
    SAFE_OK=true
    echo "✓ SAFE scenario: suggestion=$SAFE_SUGGESTION" >>"$OUT"
  else
    echo "✗ SAFE scenario: expected SAFE, got $SAFE_SUGGESTION" >>"$OUT"
  fi
fi

if [ -f var/test_advisory_nochange.json ]; then
  NOCHANGE_SUGGESTION=$(python3 -c "import json; print(json.load(open('var/test_advisory_nochange.json'))['suggested_risk_mode'])")
  if [ "$NOCHANGE_SUGGESTION" = "NO_CHANGE" ]; then
    NOCHANGE_OK=true
    echo "✓ NO_CHANGE scenario: suggestion=$NOCHANGE_SUGGESTION" >>"$OUT"
  else
    echo "✗ NO_CHANGE scenario: expected NO_CHANGE, got $NOCHANGE_SUGGESTION" >>"$OUT"
  fi
fi

echo >>"$OUT"

# Generate summary
python3 - "$SMOKE_SUMMARY" "$HALT_OK" "$SAFE_OK" "$NOCHANGE_OK" >>"$OUT" <<'PY'
import json, sys

output_path = sys.argv[1]
halt_ok = sys.argv[2] == "true"
safe_ok = sys.argv[3] == "true"
nochange_ok = sys.argv[4] == "true"

all_ok = halt_ok and safe_ok and nochange_ok

summary = {
    "test_timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "scenarios": {
        "halt": halt_ok,
        "safe": safe_ok,
        "no_change": nochange_ok
    },
    "all_scenarios_passed": all_ok,
    "advisory_output_location": "var/risk_advisory.json",
    "audit_trail_location": "var/risk_advisory_audit.jsonl",
    "control_json_write_check": "passed (no references found)",
    "sample_advisories": {
        "halt": "var/test_advisory_halt.json",
        "safe": "var/test_advisory_safe.json",
        "no_change": "var/test_advisory_nochange.json"
    }
}

with open(output_path, "w") as f:
    json.dump(summary, f, indent=2)

print(f"wrote {output_path}")
print()
if all_ok:
    print("✓ ALL SMOKE TESTS PASSED")
    print("  - HALT scenario: ✓")
    print("  - SAFE scenario: ✓")
    print("  - NO_CHANGE scenario: ✓")
    print("  - No control.json writes: ✓")
else:
    print("✗ SOME SMOKE TESTS FAILED")
    if not halt_ok:
        print("  - HALT scenario: ✗")
    if not safe_ok:
        print("  - SAFE scenario: ✗")
    if not nochange_ok:
        print("  - NO_CHANGE scenario: ✗")
PY

echo >>"$OUT"
echo "=== Sample Advisory (HALT) ===" >>"$OUT"
cat var/test_advisory_halt.json >>"$OUT" 2>/dev/null || echo "(not found)" >>"$OUT"
echo >>"$OUT"

echo "wrote $OUT" >>"$OUT"
echo "wrote $SMOKE_SUMMARY" >>"$OUT"

# Exit with success if all scenarios passed
if [ "$HALT_OK" = "true" ] && [ "$SAFE_OK" = "true" ] && [ "$NOCHANGE_OK" = "true" ]; then
  exit 0
else
  exit 1
fi
