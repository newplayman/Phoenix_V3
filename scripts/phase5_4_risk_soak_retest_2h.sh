#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts var

OUT="artifacts/phase5_4_risk_soak_retest_2h.txt"
SUMMARY="artifacts/phase5_4_risk_soak_retest_2h_summary.json"
BOT_LOG="var/phase5_4_risk_soak_retest_2h_bot.log"

# Default duration: 2 hours. Phase 5.7 may override for shorter retests.
DURATION_SEC="${DURATION_SEC_OVERRIDE:-7200}"
SAMPLE_SEC="${SAMPLE_SEC:-60}"
ENQUEUE_EVERY_SEC="${ENQUEUE_EVERY_SEC:-60}"
API_PORT="${API_PORT:-}"
ADMIN_TOKEN="${ADMIN_TOKEN:-phase5_4}"
CHAIN_ID="${CHAIN_ID:-11155111}"
POOL_ID="${POOL_ID:-tusd-weth-005}"

CONTROL_PATH="var/control.json"
RISK_STATS_PATH="${RISK_STATS_PATH:-var/phase5_4_risk_stats_retest_2h.json}"
RISK_STATE_PATH="${RISK_STATE_PATH:-var/phase5_4_risk_state_retest_2h.json}"

ts_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
file_size() { [ -f "$1" ] && stat -c%s "$1" || echo 0; }

RISK_STATE_BYTES_START="$(file_size "$RISK_STATE_PATH")"

cat >"$CONTROL_PATH" <<JSON
{"desired_state":"RUNNING","force_dry_run":true,"risk_mode":"SAFE","reason":"phase5_4_soak_retest_2h"}
JSON

export PHOENIX_FORCE_DRY_RUN=1
export PHOENIX_AUTO_EVAL=1
export PHOENIX_ALLOW_LIVE=0
export ADMIN_TOKEN="$ADMIN_TOKEN"
export RISK_STATE_PATH="$RISK_STATE_PATH"
export RISK_STATS_PATH="$RISK_STATS_PATH"
export PHOENIX_DIVERGENCE_SAMPLES_JSON="artifacts/phase5_6_divergence_reject_samples.json"
export PHOENIX_DIVERGENCE_SAMPLES_TXT="artifacts/phase5_6_divergence_reject_samples.txt"

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
TMP_CFG="var/phase5_4_retest_2h_config.yaml"
sed 's/^  port: [0-9]\\+/  port: 0/' configs/config.yaml >"$TMP_CFG"
export PHOENIX_CONFIG="$TMP_CFG"

: >"$BOT_LOG"
: >"$PHOENIX_DIVERGENCE_SAMPLES_JSON" || true
: >"$PHOENIX_DIVERGENCE_SAMPLES_TXT" || true

echo "Phase 5.4 Risk Soak Retest (2h)" >"$OUT"
echo "ts_utc=$(ts_utc)" >>"$OUT"
echo "duration_sec=$DURATION_SEC sample_sec=$SAMPLE_SEC api_port=$API_PORT" >>"$OUT"
echo "chain_id=$CHAIN_ID pool_id=$POOL_ID" >>"$OUT"
echo "bot_log=$BOT_LOG" >>"$OUT"
echo "risk_stats_path=$RISK_STATS_PATH risk_state_path=$RISK_STATE_PATH" >>"$OUT"
echo >>"$OUT"

START_TS="$(date +%s)"

echo "=== bot start $(ts_utc) ===" >>"$BOT_LOG"
# Run in its own process group so cleanup can reliably terminate both the go wrapper and the compiled child binary.
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

echo "[retest] waiting for api..." >>"$OUT"
if ! wait_for_api; then
  echo "[retest] api not ready; last 200 lines of bot log:" >>"$OUT"
  tail -n 200 "$BOT_LOG" >>"$OUT" || true
  exit 1
fi
echo "[retest] api ready" >>"$OUT"

enqueue_intent() {
  local pool_id="$1"
  local intent_type="$2"
  curl -sf \
    -H "Content-Type: application/json" \
    -H "X-Admin-Token: ${ADMIN_TOKEN}" \
    -X POST "http://127.0.0.1:${API_PORT}/api/intents/enqueue" \
    -d "{\"chain_id\":${CHAIN_ID},\"pool_id\":\"${pool_id}\",\"type\":\"${intent_type}\",\"urgency\":5,\"strategy_version\":\"phase5_4\"}" >/dev/null
}

echo "[retest] enqueue bootstrap intents..." >>"$OUT"
sleep 2
if enqueue_intent "${POOL_ID}" "mock_rebalance"; then
  echo "[retest] enqueue ok #1" >>"$OUT"
else
  echo "[retest] enqueue failed #1" >>"$OUT"
fi
sleep 2
if enqueue_intent "${POOL_ID}" "mock_rebalance"; then
  echo "[retest] enqueue ok #2" >>"$OUT"
else
  echo "[retest] enqueue failed #2" >>"$OUT"
fi

SAMPLES=0
LAST_ENQUEUE_TS=0
while true; do
  NOW_TS="$(date +%s)"
  ELAPSED=$(( NOW_TS - START_TS ))
  if [ "$ELAPSED" -ge "$DURATION_SEC" ]; then
    break
  fi

  if ! kill -0 "$BOT_PID" >/dev/null 2>&1; then
    echo "[retest] bot exited early elapsed_sec=$ELAPSED" >>"$OUT"
    wait "$BOT_PID" || true
    echo "[retest] last 200 lines of bot log:" >>"$OUT"
    tail -n 200 "$BOT_LOG" >>"$OUT" || true
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
rule = data.get("rule_counts", {}) or {}
top_rule = ""
top_rule_n = 0
for k,v in rule.items():
  if v > top_rule_n:
    top_rule_n = v
    top_rule = k
reject = verdict.get("REJECT", 0)
skip = verdict.get("SKIP", 0)
div = data.get("price_divergence_stats", {}) or {}
max_bps = div.get("max_deviation_bps", 0)
print(f"[sample] ts_utc={ts} total_evaluations={total} reject={reject} skip={skip} top_rule={top_rule}:{top_rule_n} price_div_max_bps={max_bps}")
PY

  SAMPLES=$((SAMPLES + 1))

  # Ensure we generate comparable risk evaluations during the retest window.
  # Cooldown min interval is 60s, so default cadence is 60s.
  if [ "${ENQUEUE_EVERY_SEC}" -gt 0 ] && [ $(( NOW_TS - LAST_ENQUEUE_TS )) -ge "${ENQUEUE_EVERY_SEC}" ]; then
    if enqueue_intent "${POOL_ID}" "mock_rebalance"; then
      echo "[retest] periodic enqueue ok ts_utc=$(ts_utc)" >>"$OUT"
    else
      echo "[retest] periodic enqueue failed ts_utc=$(ts_utc)" >>"$OUT"
    fi
    LAST_ENQUEUE_TS="$NOW_TS"
  fi

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

python3 - "$RISK_STATS_PATH" "$SUMMARY" "$RUN_DURATION_SEC" "$SAMPLES" "$EXIT_CODE" "$RISK_STATE_BYTES_START" "$RISK_STATE_BYTES_END" "$BOT_LOG" >>"$OUT" <<'PY'
import json, os, re, sys
stats_path, out_path = sys.argv[1], sys.argv[2]
duration = int(sys.argv[3])
sample_count = int(sys.argv[4])
exit_code = int(sys.argv[5])
bs = int(sys.argv[6])
be = int(sys.argv[7])
bot_log = sys.argv[8]

stats = {}
if os.path.exists(stats_path):
  with open(stats_path, "r") as f:
    stats = json.load(f)

def topN(m, n=10):
  if not isinstance(m, dict):
    return []
  items = sorted(m.items(), key=lambda kv: kv[1], reverse=True)[:n]
  return [{"key": k, "count": v} for k,v in items]

absurd_count = 0
absurd_max = 0
absurd_example = ""
dev_re = re.compile(r"deviation_bps=([0-9]+)")

if os.path.exists(bot_log):
  with open(bot_log, "r", encoding="utf-8", errors="ignore") as f:
    for line in f:
      m = dev_re.search(line)
      if not m:
        continue
      try:
        v = int(m.group(1))
      except Exception:
        continue
      if v > 1_000_000:
        absurd_count += 1
        if v > absurd_max:
          absurd_max = v
        if not absurd_example:
          absurd_example = line.strip()

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
  "risk_state_bytes_delta": be - bs,
  "absurd_deviation_count": absurd_count,
  "absurd_deviation_max_bps": absurd_max if absurd_count else 0,
}

os.makedirs(os.path.dirname(out_path), exist_ok=True)
with open(out_path, "w") as f:
  json.dump(out, f, indent=2, sort_keys=True)
print(out_path)

print()
if absurd_count > 0:
  print(f"absurd_deviation_count={absurd_count} max_bps={absurd_max}")
  print("absurd_deviation_example:")
  print(absurd_example)
else:
  print("absurd_deviation_count=0 OK")
PY

echo >>"$OUT"
echo "=== bot log tail (200 lines) ===" >>"$OUT"
tail -n 200 "$BOT_LOG" >>"$OUT" || true
echo >>"$OUT"
echo "=== bot log highlights (risk_control / executor) ===" >>"$OUT"
if command -v rg >/dev/null 2>&1; then
  rg -n "risk_control|IntentExecutor\\]|ExecutorResultV1|price divergence too high|absurd_deviation" "$BOT_LOG" | tail -n 200 >>"$OUT" || true
else
  grep -nE "risk_control|IntentExecutor\\]|ExecutorResultV1|price divergence too high|absurd_deviation" "$BOT_LOG" | tail -n 200 >>"$OUT" || true
fi
echo >>"$OUT"
echo "wrote $OUT" >>"$OUT"
echo "wrote $SUMMARY" >>"$OUT"

echo >>"$OUT"
echo "=== Phase 5.6 divergence samples (first 30 lines) ===" >>"$OUT"
head -n 30 "$PHOENIX_DIVERGENCE_SAMPLES_TXT" >>"$OUT" 2>/dev/null || true
