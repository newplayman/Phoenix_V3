#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CONFIG_PATH="${CONFIG_PATH:-configs/config_sepolia_swaptest.yaml}"
API_BASE="${API_BASE:-http://127.0.0.1:8081}"
POOL_ID="${POOL_ID:-tusd-weth-005}"
CHAIN_ID="${CHAIN_ID:-11155111}"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }
}

require_cmd go
require_cmd curl
require_cmd jq
require_cmd rg
require_cmd ss

SKIP_START_BOT="${SKIP_START_BOT:-}"
BOT_FLAGS="${BOT_FLAGS:-}"

port_in_use() {
  ss -ltn | awk '{print $4}' | rg -q '(:|\\])8081$'
}

try_kill_existing_phoenix_bot() {
  # Only kill processes that look like our rehearsal bot binaries to avoid disrupting unrelated services.
  local pids cmdline pid killed
  pids="$(ss -ltnp 2>/dev/null | rg '(:|\\])8081\\b' | sed -n 's/.*pid=\\([0-9]\\+\\).*/\\1/p' | sort -u || true)"
  if [[ -z "${pids}" ]]; then
    return 1
  fi
  killed=0
  for pid in ${pids}; do
    if [[ ! -r "/proc/${pid}/cmdline" ]]; then
      continue
    fi
    cmdline="$(tr '\\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
    if rg -q '/tmp/phoenix_bot_|phoenix-v3|/cmd/bot' <<<"$cmdline"; then
      echo "[accept] port 8081 in use by phoenix bot (pid=$pid); stopping it ..."
      kill "$pid" >/dev/null 2>&1 || true
      killed=1
    fi
  done
  if [[ "$killed" -eq 0 ]]; then
    return 1
  fi
  for _ in {1..20}; do
    if ! port_in_use; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

if [[ -z "${ADMIN_TOKEN:-}" ]]; then
  ADMIN_TOKEN="$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)"
  export ADMIN_TOKEN
fi

if [[ -z "${BOT_PRIVATE_KEY:-}" && -z "${BOT_PRIVATE_KEY_FILE:-}" && -z "${BOT_WALLET_ADDRESS:-}" ]]; then
  echo "Either BOT_PRIVATE_KEY, BOT_PRIVATE_KEY_FILE, or BOT_WALLET_ADDRESS is required (preview needs balance reads)." >&2
  exit 2
fi

# SUPABASE_DB_URL is optional: if unset, the bot falls back to local SQLite.

LOG_PATH="${LOG_PATH:-/tmp/phoenix_accept_control_plane_v1.log}"
OUT_INTENT_ID_FILE="${OUT_INTENT_ID_FILE:-}"
OUT_BOT_PID_FILE="${OUT_BOT_PID_FILE:-}"
KEEP_BOT_RUNNING="${KEEP_BOT_RUNNING:-}"
PID=""

cleanup() {
  if [[ -n "${KEEP_BOT_RUNNING}" ]]; then
    return
  fi
  if [[ -n "${PID}" ]]; then
    kill "${PID}" >/dev/null 2>&1 || true
    wait "${PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ -z "$SKIP_START_BOT" ]]; then
  if port_in_use; then
    if ! try_kill_existing_phoenix_bot; then
      echo "port 8081 already in use; stop the existing process or set SKIP_START_BOT=1" >&2
      ss -ltnp 2>/dev/null | rg '(:|\\])8081\\b' || true
      exit 2
    fi
  fi

  echo "[accept] starting bot (dry-run) ..."
  BOT_BIN="${BOT_BIN:-/tmp/phoenix_bot_accept}"
  go build -o "$BOT_BIN" ./cmd/bot >/dev/null

  if [[ -z "$BOT_FLAGS" ]]; then
    BOT_FLAGS="-dry-run -config $CONFIG_PATH -no-monitor -offline-feed"
  fi
  # shellcheck disable=SC2086
  "$BOT_BIN" $BOT_FLAGS >"$LOG_PATH" 2>&1 &
  PID="$!"
  if [[ -n "${OUT_BOT_PID_FILE}" ]]; then
    echo "${PID}" >"${OUT_BOT_PID_FILE}"
  fi
else
  if ! port_in_use; then
    echo "SKIP_START_BOT=1 but port 8081 is not listening; start the bot first" >&2
    exit 2
  fi
  echo "[accept] using existing bot on $API_BASE (log: $LOG_PATH)"
fi

auth_header=(-H "Authorization: Bearer ${ADMIN_TOKEN}")

echo "[accept] waiting for API ..."
for _ in {1..120}; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" "$API_BASE/api/v1/health" 2>/dev/null || true)"
  if [[ "$code" == "200" ]]; then
    break
  fi
  sleep 0.5
done

code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" "$API_BASE/api/v1/health" 2>/dev/null || true)"
if [[ "$code" != "200" ]]; then
  echo "[accept] API did not become healthy (code=$code). See $LOG_PATH" >&2
  exit 1
fi

echo "[accept] auth required ..."
unauth="$(curl -sS -o /dev/null -w '%{http_code}' "$API_BASE/api/v1/health" 2>/dev/null || true)"
if [[ "$unauth" != "401" ]]; then
  echo "[accept] expected 401 without auth, got $unauth" >&2
  exit 1
fi

echo "[accept] pools/state endpoints ..."
curl -sS "${auth_header[@]}" "$API_BASE/api/v1/pools" | jq -e '.pools | length > 0' >/dev/null

echo "[accept] waiting for pool state ..."
for _ in {1..240}; do
  state="$(curl -sS "${auth_header[@]}" "$API_BASE/api/v1/pools/${POOL_ID}/state" || true)"
  price="$(jq -r '.dex.price_stable_per_weth // 0' <<<"$state" 2>/dev/null || echo 0)"
  if [[ "${price}" != "0" && "${price}" != "0.0" && "${price}" != "null" ]]; then
    break
  fi
  sleep 0.5
done
state="$(curl -sS "${auth_header[@]}" "$API_BASE/api/v1/pools/${POOL_ID}/state" || true)"
curl -sS "${auth_header[@]}" "$API_BASE/api/v1/pools/${POOL_ID}/state" | jq -e '.pool_id == "'"$POOL_ID"'" and .dex.price_stable_per_weth > 0' >/dev/null || {
  echo "[accept] pool state not ready: $state" >&2
  exit 1
}

echo "[accept] SSE stream smoke (wait for ping) ..."
set +o pipefail
timeout 20s curl -sS -N "${auth_header[@]}" "$API_BASE/api/v1/stream" 2>/dev/null | rg -m 1 '^: ping ' >/dev/null
set -o pipefail

idem="acc-$(date +%s)-$RANDOM"
preview_payload="$(jq -n \
  --arg action "force_rebalance" \
  --arg pool "$POOL_ID" \
  --argjson chain "$CHAIN_ID" \
  --arg ikey "$idem" \
  '{action_type:$action,pool_id:$pool,chain_id:$chain,params:{},idempotency_key:$ikey}')"

echo "[accept] preview (idempotent) ..."
op1="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$preview_payload" "$API_BASE/api/v1/operations/preview" | jq -r '.operation_id')"
if [[ -z "$op1" || "$op1" == "null" ]]; then
  echo "[accept] missing operation_id from preview" >&2
  exit 1
fi
op2="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$preview_payload" "$API_BASE/api/v1/operations/preview" | jq -r '.operation_id')"
if [[ "$op2" != "$op1" ]]; then
  echo "[accept] preview idempotency failed: $op1 != $op2" >&2
  exit 1
fi

echo "[accept] execute requires confirm/reason ..."
bad_exec="$(jq -n --arg op "$op1" --arg pool "$POOL_ID" '{operation_id:$op,pool_id:$pool,confirm_text:"",reason:"",idempotency_key:"exec-bad"}')"
bad_code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" -H "Content-Type: application/json" -d "$bad_exec" "$API_BASE/api/v1/operations/execute" || true)"
if [[ "$bad_code" != "400" ]]; then
  echo "[accept] expected 400 for missing confirm/reason, got $bad_code" >&2
  exit 1
fi

exec_payload="$(jq -n \
  --arg op "$op1" \
  --arg pool "$POOL_ID" \
  --arg reason "acceptance-test" \
  --arg ikey "exec-$idem" \
  '{operation_id:$op,pool_id:$pool,confirm_text:"CONFIRM",reason:$reason,idempotency_key:$ikey}')"

exec_resp="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$exec_payload" "$API_BASE/api/v1/operations/execute")"
intent1="$(jq -r '.intent_id' <<<"$exec_resp")"
if [[ -z "$intent1" || "$intent1" == "null" ]]; then
  echo "[accept] missing intent_id from execute: $exec_resp" >&2
  exit 1
fi
if [[ -n "$OUT_INTENT_ID_FILE" ]]; then
  echo "$intent1" >"$OUT_INTENT_ID_FILE"
fi

exec_resp2="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$exec_payload" "$API_BASE/api/v1/operations/execute")"
intent2="$(jq -r '.intent_id' <<<"$exec_resp2")"
if [[ "$intent2" != "$intent1" ]]; then
  echo "[accept] execute idempotency failed: $intent1 != $intent2" >&2
  exit 1
fi

echo "[accept] intent status/steps ..."
last_intent_json=""
steps_count="0"
status=""
for _ in {1..120}; do
  last_intent_json="$(curl -sS "${auth_header[@]}" "$API_BASE/api/v1/intents/$intent1" || true)"
  status="$(jq -r '.intent.status // empty' <<<"$last_intent_json" 2>/dev/null || true)"
  steps_count="$(jq -r '.steps | length' <<<"$last_intent_json" 2>/dev/null || echo 0)"
  if [[ -n "${status:-}" && "${steps_count:-0}" != "0" ]]; then
    break
  fi
  sleep 0.5
done
if ! jq -e '.intent.intent_id == "'"$intent1"'"' >/dev/null 2>&1 <<<"$last_intent_json"; then
  echo "[accept] intent lookup failed: $last_intent_json" >&2
  exit 1
fi
if [[ "${steps_count:-0}" == "0" ]]; then
  echo "[accept] intent has no steps after wait (status=$status): $last_intent_json" >&2
  exit 1
fi

echo "[accept] audit trail ..."
curl -sS "${auth_header[@]}" "$API_BASE/api/v1/audit?pool_id=${POOL_ID}&limit=50" | jq -e '.actions | any(.action_type == "preview_rebalance") and any(.action_type == "execute_rebalance")' >/dev/null

if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' | rg -q '^supabase-db$'; then
  echo "[accept] DB assertions (supabase-db) ..."
  docker exec supabase-db psql -U postgres -d postgres -tAc "select count(*) from operations;" >/dev/null
  docker exec supabase-db psql -U postgres -d postgres -tAc "select count(*) from operator_actions;" >/dev/null
  docker exec supabase-db psql -U postgres -d postgres -tAc "select count(*) from intent_records;" >/dev/null
fi

echo "[accept] OK"
