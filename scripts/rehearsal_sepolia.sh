#!/usr/bin/env bash
set -euo pipefail

# Testnet rehearsal helper.
#
# What it does:
# 1) Validates config
# 2) Starts bot
# 3) Pauses strategy loop (avoid auto-enqueue)
# 4) Triggers one manual rebalance intent
# 5) Prints where to watch logs/events
#
# Requirements (your machine):
# - Network access (Sepolia RPC + price feeds)
# - Ability to bind localhost ports 8081/8082
# - BOT_PRIVATE_KEY set (testnet wallet only)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CONFIG_PATH="${CONFIG_PATH:-configs/config.yaml}"
API_BASE="${API_BASE:-http://127.0.0.1:8081}"
BOT_LOG="${BOT_LOG:-logs/bot_sepolia.log}"
BOT_PID_FILE="${BOT_PID_FILE:-logs/bot_sepolia.pid}"
POOL_ID="${POOL_ID:-${1:-}}"

mkdir -p logs

if [[ -z "${BOT_PRIVATE_KEY:-}" ]]; then
  echo "ERROR: BOT_PRIVATE_KEY is not set" >&2
  echo "Hint: export BOT_PRIVATE_KEY=0x... (testnet wallet)" >&2
  exit 1
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
  echo "ERROR: config not found: $CONFIG_PATH" >&2
  exit 1
fi

if [[ -z "$POOL_ID" ]]; then
  # Best-effort parse first pools[].id from YAML (works with current repo config format).
  POOL_ID="$(awk '
    /^pools:/ {in_pools=1; next}
    in_pools && $1=="-" && $2=="id:" {gsub(/"/,"",$3); print $3; exit}
  ' "$CONFIG_PATH" || true)"
fi

if [[ -z "$POOL_ID" ]]; then
  echo "ERROR: POOL_ID not provided and failed to parse from config" >&2
  echo "Usage: POOL_ID=... ./scripts/rehearsal_sepolia.sh" >&2
  exit 1
fi

cleanup() {
  if [[ -f "$BOT_PID_FILE" ]]; then
    pid="$(cat "$BOT_PID_FILE" || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      sleep 0.5
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$BOT_PID_FILE"
  fi
}
trap cleanup EXIT

echo "[rehearsal_sepolia] configcheck: $CONFIG_PATH"
go run ./cmd/configcheck -config "$CONFIG_PATH"

rm -f "$BOT_LOG"

echo "[rehearsal_sepolia] starting bot..."
(go run ./cmd/bot -config "$CONFIG_PATH") >"$BOT_LOG" 2>&1 &
echo $! > "$BOT_PID_FILE"
echo "[rehearsal_sepolia] bot pid=$(cat "$BOT_PID_FILE")"

echo "[rehearsal_sepolia] waiting for API: $API_BASE/api/status"
deadline=$((SECONDS + 45))
until curl -fsS "$API_BASE/api/status" >/dev/null 2>&1; do
  if [[ $SECONDS -ge $deadline ]]; then
    echo "ERROR: API did not become ready within 45s" >&2
    echo "bot log: $BOT_LOG" >&2
    tail -n 80 "$BOT_LOG" || true
    exit 1
  fi
  sleep 1
done

echo "[rehearsal_sepolia] pausing auto strategy loop (manual intents only)"
curl -fsS -X POST "$API_BASE/api/control/pause" >/dev/null || true

echo "[rehearsal_sepolia] trigger rebalance: pool_id=$POOL_ID"
curl -fsS -X POST "$API_BASE/api/control/rebalance?pool_id=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$POOL_ID")" >/dev/null

echo "[rehearsal_sepolia] done. Watch logs/events:"
echo "  - tail -f $BOT_LOG"
echo "  - if events.driver=file: tail -f logs/events.jsonl"
echo "  - dashboard: cd web && VITE_API_BASE=$API_BASE npm run dev"

echo "[rehearsal_sepolia] showing last 40 log lines:"
tail -n 40 "$BOT_LOG" || true

echo "[rehearsal_sepolia] stopping bot (script ends)"

