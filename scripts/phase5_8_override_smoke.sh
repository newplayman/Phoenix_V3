#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts var

OUT="artifacts/phase5_8_override_smoke.txt"
SUMMARY="artifacts/phase5_8_override_smoke_summary.json"
BOT_LOG="var/phase5_8_override_smoke_bot.log"

DURATION_SEC="${DURATION_SEC:-90}"
SAMPLE_SEC="${SAMPLE_SEC:-15}"
ADMIN_TOKEN="${ADMIN_TOKEN:-phase5_8_smoke}"
CHAIN_ID="${CHAIN_ID:-11155111}"
POOL_ID="${POOL_ID:-tusd-weth-005}"

CONTROL_PATH="var/control.json"
RISK_STATS_PATH="var/phase5_8_override_smoke_stats.json"
RISK_STATE_PATH="var/phase5_8_override_smoke_state.json"

ts_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

cat >"$CONTROL_PATH" <<JSON
{"desired_state":"RUNNING","force_dry_run":true,"risk_mode":"SAFE","reason":"phase5_8_override_smoke"}
JSON

export PHOENIX_FORCE_DRY_RUN=1
export PHOENIX_AUTO_EVAL=1
export PHOENIX_ALLOW_LIVE=0
export ADMIN_TOKEN="$ADMIN_TOKEN"
export RISK_STATE_PATH="$RISK_STATE_PATH"
export RISK_STATS_PATH="$RISK_STATS_PATH"
export PHOENIX_DIVERGENCE_SAMPLES_JSON="artifacts/phase5_8_override_smoke_samples.json"
export PHOENIX_DIVERGENCE_SAMPLES_TXT="artifacts/phase5_8_override_smoke_samples.txt"

# Phase 5.8: test override threshold
export RISK_PRICE_DIVERGENCE_MAX_BPS_OVERRIDE=500

API_PORT="$(python3 - <<'PY'
import socket
s=socket.socket()
s.bind(("127.0.0.1",0))
print(s.getsockname()[1])
s.close()
PY
)"
export API_PORT="$API_PORT"

TMP_CFG="var/phase5_8_override_smoke_config.yaml"
sed 's/^  port: [0-9]\+/  port: 0/' configs/config.yaml >"$TMP_CFG"
export PHOENIX_CONFIG="$TMP_CFG"

: >"$BOT_LOG"
: >"$PHOENIX_DIVERGENCE_SAMPLES_JSON" || true
: >"$PHOENIX_DIVERGENCE_SAMPLES_TXT" || true

echo "Phase 5.8 Override Smoke Test" >"$OUT"
echo "ts_utc=$(ts_utc)" >>"$OUT"
echo "duration_sec=$DURATION_SEC sample_sec=$SAMPLE_SEC api_port=$API_PORT" >>"$OUT"
echo "override_threshold_bps=500" >>"$OUT"
echo >>"$OUT"

START_TS="$(date +%s)"

echo "=== bot start $(ts_utc) ===" >>"$BOT_LOG"
setsid -w go run ./cmd/bot >>"$BOT_LOG" 2>&1 &
BOT_PID="$!"

cleanup() {
  [ -n "${BOT_PID:-}" ] || return 0
  echo "=== bot stop $(ts_utc) pgid=$BOT_PID ===" >>"$BOT_LOG"
  kill -TERM -- "-$BOT_PID" >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ! pgrep -g "$BOT_PID" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "=== bot SIGKILL $(ts_utc) pgid=$BOT_PID ===" >>"$BOT_LOG"
  kill -KILL -- "-$BOT_PID" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_api() {
  local deadline=$(( $(date +%s) + 60 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if curl -sf "http://127.0.0.1:${API_PORT}/api/status" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

echo "[smoke] waiting for api..." >>"$OUT"
if ! wait_for_api; then
  echo "[smoke] api not ready; last 100 lines of bot log:" >>"$OUT"
  tail -n 100 "$BOT_LOG" >>"$OUT" || true
  exit 1
fi
echo "[smoke] api ready" >>"$OUT"

enqueue_intent() {
  local pool_id="$1"
  local intent_type="$2"
  curl -sf \
    -H "Content-Type: application/json" \
    -H "X-Admin-Token: ${ADMIN_TOKEN}" \
    -X POST "http://127.0.0.1:${API_PORT}/api/intents/enqueue" \
    -d "{\"chain_id\":${CHAIN_ID},\"pool_id\":\"${pool_id}\",\"type\":\"${intent_type}\",\"urgency\":5,\"strategy_version\":\"phase5_8\"}" >/dev/null
}

echo "[smoke] enqueue bootstrap intents..." >>"$OUT"
sleep 2
if enqueue_intent "${POOL_ID}" "mock_rebalance"; then
  echo "[smoke] enqueue ok #1" >>"$OUT"
else
  echo "[smoke] enqueue failed #1" >>"$OUT"
fi
sleep 2
if enqueue_intent "${POOL_ID}" "mock_rebalance"; then
  echo "[smoke] enqueue ok #2" >>"$OUT"
else
  echo "[smoke] enqueue failed #2" >>"$OUT"
fi

SAMPLES=0
while true; do
  NOW_TS="$(date +%s)"
  ELAPSED=$(( NOW_TS - START_TS ))
  if [ "$ELAPSED" -ge "$DURATION_SEC" ]; then
    break
  fi

  if ! kill -0 "$BOT_PID" >/dev/null 2>&1; then
    echo "[smoke] bot exited early elapsed_sec=$ELAPSED" >>"$OUT"
    wait "$BOT_PID" || true
    echo "[smoke] last 100 lines of bot log:" >>"$OUT"
    tail -n 100 "$BOT_LOG" >>"$OUT" || true
    exit 2
  fi

  python3 - "$RISK_STATS_PATH" <<'PY' >>"$OUT"
import json, os, sys, time
path = sys.argv[1]
ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
data = {}
if os.path.exists(path):
  with open(path, "r") as f:
    data = json.load(f)
total = data.get("total_evaluations", 0)
verdict = data.get("verdict_counts", {}) or {}
reject = verdict.get("REJECT", 0)
skip = verdict.get("SKIP", 0)
tm_skip = data.get("time_mismatch_skip_count", 0)
tm_rate = data.get("time_mismatch_skip_rate", 0.0)
print(f"[sample] ts_utc={ts} total={total} reject={reject} skip={skip} time_mismatch_skip={tm_skip} tm_rate={tm_rate:.4f}")
PY

  SAMPLES=$((SAMPLES + 1))
  sleep "$SAMPLE_SEC"
done

cleanup
set +e
wait "$BOT_PID" >/dev/null 2>&1
EXIT_CODE=$?
set -e
END_TS="$(date +%s)"
RUN_DURATION_SEC=$(( END_TS - START_TS ))

python3 - "$RISK_STATS_PATH" "$SUMMARY" "$RUN_DURATION_SEC" "$SAMPLES" "$EXIT_CODE" "$BOT_LOG" >>"$OUT" <<'PY'
import json, os, re, sys
stats_path, out_path = sys.argv[1], sys.argv[2]
duration = int(sys.argv[3])
sample_count = int(sys.argv[4])
exit_code = int(sys.argv[5])
bot_log = sys.argv[6]

stats = {}
if os.path.exists(stats_path):
  with open(stats_path, "r") as f:
    stats = json.load(f)

# Phase 5.8: check for override_used in logs
override_found = False
threshold_found = False
if os.path.exists(bot_log):
  with open(bot_log, "r", encoding="utf-8", errors="ignore") as f:
    for line in f:
      if "override_used=true" in line:
        override_found = True
      if "threshold_bps=500" in line:
        threshold_found = True
      if override_found and threshold_found:
        break

out = {
  "run_duration_sec": duration,
  "sample_count": sample_count,
  "process_exit_code": exit_code,
  "total_evaluations": stats.get("total_evaluations", 0),
  "verdict_counts": stats.get("verdict_counts", {}) or {},
  "time_mismatch_skip_count": stats.get("time_mismatch_skip_count", 0),
  "time_mismatch_skip_rate": stats.get("time_mismatch_skip_rate", 0.0),
  "threshold_bps_override_found_in_logs": override_found,
  "threshold_bps_500_found_in_logs": threshold_found,
  "validation": {
    "time_mismatch_field_present": "time_mismatch_skip_count" in stats,
    "override_effective": override_found and threshold_found
  }
}

os.makedirs(os.path.dirname(out_path), exist_ok=True)
with open(out_path, "w") as f:
  json.dump(out, f, indent=2, sort_keys=True)
print(out_path)

print()
print("=== Phase 5.8 Smoke Test Validation ===")
print(f"time_mismatch_skip_count field present: {out['validation']['time_mismatch_field_present']}")
print(f"override threshold effective: {out['validation']['override_effective']}")
if out['validation']['time_mismatch_field_present'] and out['validation']['override_effective']:
  print("✓ SMOKE TEST PASSED")
else:
  print("✗ SMOKE TEST FAILED")
PY

echo >>"$OUT"
echo "=== bot log tail (100 lines) ===" >>"$OUT"
tail -n 100 "$BOT_LOG" >>"$OUT" || true
echo >>"$OUT"
echo "=== bot log override markers ===" >>"$OUT"
if command -v rg >/dev/null 2>&1; then
  rg -n "override_used|threshold_bps" "$BOT_LOG" | tail -n 50 >>"$OUT" || true
else
  grep -nE "override_used|threshold_bps" "$BOT_LOG" | tail -n 50 >>"$OUT" || true
fi
echo >>"$OUT"
echo "wrote $OUT" >>"$OUT"
echo "wrote $SUMMARY" >>"$OUT"
