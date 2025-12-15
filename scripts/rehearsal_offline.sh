#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LOG_DIR="logs"
EVENTS_FILE="$LOG_DIR/events.jsonl"
BOT_LOG="$LOG_DIR/bot_offline.log"
BOT_PID_FILE="$LOG_DIR/bot_offline.pid"

mkdir -p "$LOG_DIR"

cleanup() {
  if [[ -f "$BOT_PID_FILE" ]]; then
    pid="$(cat "$BOT_PID_FILE" || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      sleep 0.2
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$BOT_PID_FILE"
  fi
}
trap cleanup EXIT

rm -f "$EVENTS_FILE" "$BOT_LOG"

echo "[rehearsal_offline] starting bot (offline + dry-run)"
(
  go run ./cmd/bot -config configs/config.yaml -dry-run -offline -no-api -no-monitor
) >"$BOT_LOG" 2>&1 &
echo $! > "$BOT_PID_FILE"

echo "[rehearsal_offline] bot pid=$(cat "$BOT_PID_FILE")"

echo "[rehearsal_offline] waiting for events..."
deadline=$((SECONDS + 15))
while [[ $SECONDS -lt $deadline ]]; do
  if [[ -f "$EVENTS_FILE" ]] && grep -q '"topic":"intent_exec"' "$EVENTS_FILE"; then
    break
  fi
  sleep 0.5
done

if [[ ! -f "$EVENTS_FILE" ]]; then
  echo "[rehearsal_offline] ERROR: no events file produced: $EVENTS_FILE" >&2
  echo "[rehearsal_offline] bot log: $BOT_LOG" >&2
  exit 1
fi

echo "[rehearsal_offline] topic counts:"
grep -o '"topic":"[^"]\+"' "$EVENTS_FILE" | sort | uniq -c | sort -nr || true

echo "[rehearsal_offline] sample replay (first 10 lines):"
go run ./cmd/replayfile -path "$EVENTS_FILE" -topics ticker,pool_state,intent_exec | head -n 10 || true

echo "[rehearsal_offline] done. artifacts:"
echo "  - $EVENTS_FILE"
echo "  - $BOT_LOG"

