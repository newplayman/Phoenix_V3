#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

shopt -s nullglob

INTERVAL_SEC="${INTERVAL_SEC:-60}"
DURATION_MIN="${DURATION_MIN:-30}"
INJECT_EVERY_SEC="${INJECT_EVERY_SEC:-120}"
INJECT_DURATION_MIN="${INJECT_DURATION_MIN:-10}"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${OUT:-$ROOT/artifacts/riskadvisory_loop_${TS}}"
mkdir -p "$OUT"

LOOP_LOG="$OUT/loop.log"
INJECT_LOG="$OUT/inject.log"
SUMMARY="$OUT/summary.txt"

STATS_PATH="$ROOT/var/risk_stats.json"
ADVISORY_PATH="$ROOT/var/risk_advisory.json"
BIN="$OUT/riskadvisory"

echo "OUT=$OUT"
echo "interval_sec=$INTERVAL_SEC duration_min=$DURATION_MIN inject_every_sec=$INJECT_EVERY_SEC inject_duration_min=$INJECT_DURATION_MIN" | tee "$SUMMARY" >/dev/null
echo "stats_path=$STATS_PATH" | tee -a "$SUMMARY" >/dev/null
echo "advisory_path=$ADVISORY_PATH" | tee -a "$SUMMARY" >/dev/null

cp -a "$ADVISORY_PATH" "$OUT/risk_advisory.before.json" 2>/dev/null || true
stat "$ADVISORY_PATH" > "$OUT/risk_advisory.before.stat" 2>/dev/null || true

if [ -f "$STATS_PATH" ]; then
  cp -a "$STATS_PATH" "$OUT/risk_stats.before.json"
else
  echo "note: missing risk_stats.json before run" >> "$SUMMARY"
fi

cleanup() {
  if [ -f "$OUT/risk_stats.before.json" ]; then
    cp -a "$OUT/risk_stats.before.json" "$STATS_PATH" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "[build] building cmd/riskadvisory -> $BIN" | tee -a "$LOOP_LOG" >/dev/null
go build -o "$BIN" ./cmd/riskadvisory

inject_risk_stats() {
  local divergence="$1"
  local ts="$2"

  python3 - <<PY
import json, os, time

path = ${STATS_PATH@Q}
div = float(${divergence@Q})
now_ms = int(time.time() * 1000)

total = 100
reject = int(round(div * total))
if reject < 0:
  reject = 0
if reject > total:
  reject = total

data = {}
if os.path.exists(path):
  try:
    with open(path, "r") as f:
      data = json.load(f) or {}
  except Exception:
    data = {}

data["updated_at_ms"] = now_ms
data["total_evaluations"] = total
data.setdefault("reject_counts_by_rule_id", {})
data["reject_counts_by_rule_id"]["price_source_divergence"] = reject
data.setdefault("verdict_counts", {})
data["verdict_counts"]["REJECT"] = max(data["verdict_counts"].get("REJECT", 0), reject)
data.setdefault("price_divergence_stats", {})
data["price_divergence_stats"]["max_deviation_bps"] = max(int(data["price_divergence_stats"].get("max_deviation_bps", 0)), 900 if div >= 0.20 else (200 if div >= 0.05 else 50))

tmp = path + ".tmp"
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(tmp, "w") as f:
  json.dump(data, f, indent=2)
os.replace(tmp, path)
PY

  local hash
  hash="$(sha256sum "$STATS_PATH" | awk '{print $1}' || echo "")"
  echo "[$ts] inject divergence_reject_rate_target=$divergence risk_stats_sha256=$hash" | tee -a "$INJECT_LOG" >/dev/null
}

run_injector() {
  local end_epoch=$(( $(date +%s) + INJECT_DURATION_MIN*60 ))
  local i=0
  local rates=(0.01 0.06 0.25 0.01)

  echo "inject_start_utc=$(date -u +%Y%m%dT%H%M%SZ)" | tee "$INJECT_LOG" >/dev/null
  while [ "$(date +%s)" -lt "$end_epoch" ]; do
    local ts
    ts="$(date -u +%Y%m%dT%H%M%SZ)"
    inject_risk_stats "${rates[$((i % ${#rates[@]}))]}" "$ts"
    i=$((i+1))
    sleep "$INJECT_EVERY_SEC"
  done
  echo "inject_end_utc=$(date -u +%Y%m%dT%H%M%SZ)" | tee -a "$INJECT_LOG" >/dev/null

  # Verdict after the 10-minute injection window (loop keeps running).
  local modes
  modes="$(
    for f in "$OUT"/advisory_*.json; do jq -r '.suggested_risk_mode // empty' "$f" 2>/dev/null || true; done \
      | sort -u | tr '\n' ',' | sed 's/,$//'
  )"
  echo "modes_observed_during_inject=$modes" | tee -a "$INJECT_LOG" >/dev/null

  local verdict="FAIL"
  if [[ "$modes" == *"NO_CHANGE"* && "$modes" == *"SAFE"* && "$modes" == *"HALT"* ]]; then
    verdict="PASS"
  fi
  echo "VERDICT=$verdict" | tee -a "$INJECT_LOG" >/dev/null
}

run_injector &
INJECT_PID=$!
echo "$INJECT_PID" > "$OUT/inject.pid"

echo "loop_start_utc=$(date -u +%Y%m%dT%H%M%SZ)" | tee "$LOOP_LOG" >/dev/null

end_epoch=$(( $(date +%s) + DURATION_MIN*60 ))
sample_i=0

while [ "$(date +%s)" -lt "$end_epoch" ]; do
  sample_i=$((sample_i+1))
  now_utc="$(date -u +%Y%m%dT%H%M%SZ)"

  PHOENIX_RISK_STATS_FILE="$STATS_PATH" PHOENIX_RISK_ADVISORY_FILE="$ADVISORY_PATH" "$BIN" >>"$LOOP_LOG" 2>&1 || true

  sha="$(sha256sum "$ADVISORY_PATH" | awk '{print $1}' || echo "")"
  key="$(jq -c '{ts_ms, suggested_risk_mode, severity_score, confidence, evidence:{total_evaluations:(.evidence.total_evaluations // null), divergence_reject_rate:(.evidence.divergence_reject_rate // null)}}' "$ADVISORY_PATH" 2>/dev/null || echo '{}')"
  echo "[$now_utc] sample_i=$sample_i advisory_sha256=$sha key=$key" | tee -a "$LOOP_LOG" >/dev/null

  cp -a "$ADVISORY_PATH" "$OUT/advisory_${now_utc}.json" 2>/dev/null || true

  sleep "$INTERVAL_SEC"
done

echo "loop_end_utc=$(date -u +%Y%m%dT%H%M%SZ)" | tee -a "$LOOP_LOG" >/dev/null

wait "$INJECT_PID" >/dev/null 2>&1 || true

advisory_files=( "$OUT"/advisory_*.json )
if [ "${#advisory_files[@]}" -lt 5 ]; then
  echo "warning: advisory_samples_lt_5 count=${#advisory_files[@]}" >> "$SUMMARY"
fi

uniq_hash_count="$(
  for f in "${advisory_files[@]}"; do sha256sum "$f" | awk '{print $1}'; done \
    | sort -u | wc -l | tr -d ' '
)"
uniq_ts_count="$(
  for f in "${advisory_files[@]}"; do jq -r '.ts_ms' "$f" 2>/dev/null || true; done \
    | sort -u | wc -l | tr -d ' '
)"

first_json="$(printf '%s\n' "${advisory_files[@]}" | sort | head -n 1 || true)"
last_json="$(printf '%s\n' "${advisory_files[@]}" | sort | tail -n 1 || true)"

first_mode="$(jq -r '.suggested_risk_mode // empty' "$first_json" 2>/dev/null || true)"
first_sev="$(jq -r '.severity_score // empty' "$first_json" 2>/dev/null || true)"
last_mode="$(jq -r '.suggested_risk_mode // empty' "$last_json" 2>/dev/null || true)"
last_sev="$(jq -r '.severity_score // empty' "$last_json" 2>/dev/null || true)"

{
  echo ""
  echo "unique_hash_count=$uniq_hash_count"
  echo "unique_ts_ms_count=$uniq_ts_count"
  echo "first_mode=$first_mode first_severity=$first_sev"
  echo "last_mode=$last_mode last_severity=$last_sev"
} >> "$SUMMARY"

echo "inject_log=$INJECT_LOG" >> "$SUMMARY"

echo "summary_written=$SUMMARY"
echo "DONE OUT=$OUT"
