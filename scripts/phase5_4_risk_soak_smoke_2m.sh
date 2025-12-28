#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts var

OUT="artifacts/phase5_4_risk_soak_smoke_2m.txt"
SUMMARY="artifacts/phase5_4_risk_soak_smoke_2m_summary.json"
BOT_LOG="var/phase5_4_risk_soak_smoke_2m_bot.log"

DURATION_SEC="${DURATION_SEC:-120}"
SAMPLE_SEC="${SAMPLE_SEC:-10}"
API_PORT="${API_PORT:-}"
ADMIN_TOKEN="${ADMIN_TOKEN:-phase5_4}"
CHAIN_ID="${CHAIN_ID:-11155111}"
POOL_ID="${POOL_ID:-tusd-weth-005}"

CONTROL_PATH="var/control.json"
RISK_STATS_PATH="${RISK_STATS_PATH:-var/phase5_4_risk_stats_smoke_2m.json}"
RISK_STATE_PATH="${RISK_STATE_PATH:-var/phase5_4_risk_state_smoke_2m.json}"

ts_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
file_size() { [ -f "$1" ] && stat -c%s "$1" || echo 0; }

RISK_STATE_BYTES_START="$(file_size "$RISK_STATE_PATH")"

cat >"$CONTROL_PATH" <<JSON
{"desired_state":"RUNNING","force_dry_run":true,"risk_mode":"SAFE","reason":"phase5_4_soak_smoke_2m"}
JSON

export PHOENIX_FORCE_DRY_RUN=1
export PHOENIX_AUTO_EVAL=1
export PHOENIX_ALLOW_LIVE=0
export ADMIN_TOKEN="$ADMIN_TOKEN"
export RISK_STATE_PATH="$RISK_STATE_PATH"
export RISK_STATS_PATH="$RISK_STATS_PATH"

if [ -z "${API_PORT}" ]; then
  API_PORT="$(python3 - <<'PY'
import socket
s=socket.socket()
s.bind(("127.0.0.1",0))
print(s.getsockname()[1])
s.close()
PY
)"
fi
export API_PORT="$API_PORT"

# Avoid local port conflicts: create a temp config with monitoring.port=0.
TMP_CFG="var/phase5_4_smoke_config.yaml"
sed 's/^  port: [0-9]\\+/  port: 0/' configs/config.yaml >"$TMP_CFG"
export PHOENIX_CONFIG="$TMP_CFG"

: >"$BOT_LOG"

echo "Phase 5.4 Risk Soak Smoke (2m)" >"$OUT"
echo "ts_utc=$(ts_utc)" >>"$OUT"
echo "duration_sec=$DURATION_SEC sample_sec=$SAMPLE_SEC api_port=$API_PORT" >>"$OUT"
echo "chain_id=$CHAIN_ID pool_id=$POOL_ID" >>"$OUT"
echo "bot_log=$BOT_LOG" >>"$OUT"
echo >>"$OUT"

START_TS="$(date +%s)"

(
  echo "=== bot start $(ts_utc) ==="
  go run ./cmd/bot
) >>"$BOT_LOG" 2>&1 &
BOT_PID="$!"

cleanup() {
  if kill -0 "$BOT_PID" >/dev/null 2>&1; then
    echo "=== bot stop $(ts_utc) pid=$BOT_PID ===" >>"$BOT_LOG"
    kill "$BOT_PID" >/dev/null 2>&1 || true
    for _ in 1 2 3 4 5; do
      if ! kill -0 "$BOT_PID" >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
    if kill -0 "$BOT_PID" >/dev/null 2>&1; then
      echo "=== bot SIGKILL $(ts_utc) pid=$BOT_PID ===" >>"$BOT_LOG"
      kill -9 "$BOT_PID" >/dev/null 2>&1 || true
    fi
  fi
}
trap cleanup EXIT

wait_for_api() {
  local deadline=$(( $(date +%s) + 30 ))
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
  echo "[smoke] api not ready; last 200 lines of bot log:" >>"$OUT"
  tail -n 200 "$BOT_LOG" >>"$OUT" || true
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
    -d "{\"chain_id\":${CHAIN_ID},\"pool_id\":\"${pool_id}\",\"type\":\"${intent_type}\",\"urgency\":5,\"strategy_version\":\"phase5_4\"}" >/dev/null
}

echo "[smoke] enqueue intents (trigger min-interval reject)..." >>"$OUT"
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
    echo "[smoke] last 200 lines of bot log:" >>"$OUT"
    tail -n 200 "$BOT_LOG" >>"$OUT" || true
    exit 2
  fi

  RISK_STATE_BYTES="$(file_size "$RISK_STATE_PATH")"
  RISK_STATS_BYTES="$(file_size "$RISK_STATS_PATH")"

  python3 - "$RISK_STATS_PATH" "$RISK_STATE_BYTES" "$RISK_STATS_BYTES" <<'PY' >>"$OUT"
import json, os, sys, time
path = sys.argv[1]
state_bytes = int(sys.argv[2])
stats_bytes = int(sys.argv[3])
ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
data = {}
if os.path.exists(path):
  with open(path, "r") as f:
    data = json.load(f)
total = data.get("total_evaluations", 0)
verdict = data.get("verdict_counts", {}) or {}
rule = data.get("rule_counts", {}) or {}
top_rule = ""
top_rule_n = 0
for k,v in rule.items():
  if v > top_rule_n:
    top_rule_n = v
    top_rule = k
reject = verdict.get("REJECT", 0)
skip = verdict.get("SKIP", 0)
print(f"[sample] ts_utc={ts} alive=1 total_evaluations={total} reject={reject} skip={skip} top_rule={top_rule}:{top_rule_n} risk_state_bytes={state_bytes} risk_stats_bytes={stats_bytes}")
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

RISK_STATE_BYTES_END="$(file_size "$RISK_STATE_PATH")"

python3 - "$RISK_STATS_PATH" "$SUMMARY" "$RUN_DURATION_SEC" "$SAMPLES" "$EXIT_CODE" "$RISK_STATE_BYTES_START" "$RISK_STATE_BYTES_END" >>"$OUT" <<'PY'
import json, os, sys
stats_path, out_path = sys.argv[1], sys.argv[2]
duration = int(sys.argv[3])
sample_count = int(sys.argv[4])
exit_code = int(sys.argv[5])
bs = int(sys.argv[6])
be = int(sys.argv[7])

stats = {}
if os.path.exists(stats_path):
  with open(stats_path, "r") as f:
    stats = json.load(f)

def topN(m, n=10):
  if not isinstance(m, dict):
    return []
  items = sorted(m.items(), key=lambda kv: kv[1], reverse=True)[:n]
  return [{"key": k, "count": v} for k,v in items]

out = {
  "run_duration_sec": duration,
  "sample_count": sample_count,
  "process_exit_code": exit_code,
  "total_evaluations": stats.get("total_evaluations", 0),
  "verdict_counts": stats.get("verdict_counts", {}) or {},
  "rule_counts": stats.get("rule_counts", {}) or {},
  "reject_counts_by_rule_id": stats.get("reject_counts_by_rule_id", {}) or {},
  "skip_counts_by_rule_id": stats.get("skip_counts_by_rule_id", {}) or {},
  "top_cooldown_keys": topN((stats.get("cooldown_reject_count_by_key") or {}), 10),
  "price_divergence_stats": stats.get("price_divergence_stats", {}) or {},
  "stale_skip_count": int((stats.get("skip_reasons", {}) or {}).get("stale_source", 0)),
  "missing_skip_count": int((stats.get("skip_reasons", {}) or {}).get("missing_source", 0)),
  "risk_state_bytes_start": bs,
  "risk_state_bytes_end": be,
  "risk_state_bytes_delta": be - bs,
}

with open(out_path, "w") as f:
  json.dump(out, f, indent=2, sort_keys=True)
print(out_path)
PY

echo >>"$OUT"
echo "=== bot log tail (200 lines) ===" >>"$OUT"
tail -n 200 "$BOT_LOG" >>"$OUT" || true
echo >>"$OUT"
echo "=== bot log highlights (risk_control / executor) ===" >>"$OUT"
rg -n "risk_control|IntentExecutor\\]|ExecutorResultV1" "$BOT_LOG" | tail -n 200 >>"$OUT" || true
echo >>"$OUT"
echo "wrote $OUT" >>"$OUT"
echo "wrote $SUMMARY" >>"$OUT"
