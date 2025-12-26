#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${API_PORT:-18081}"

export API_PORT="$PORT"
export PHOENIX_CONFIG="${PHOENIX_CONFIG:-$ROOT_DIR/configs/config.yaml}"
export PHOENIX_FORCE_DRY_RUN="${PHOENIX_FORCE_DRY_RUN:-1}"
export PRICE_MODE="${PRICE_MODE:-ws_only}"
export PRICE_SYMBOL="${PRICE_SYMBOL:-ETH/USDT}"
export PRICE_STALE_SEC="${PRICE_STALE_SEC:-5}"
export PRICE_FREEZE_SEC="${PRICE_FREEZE_SEC:-20}"
export DIVERGENCE_PCT="${DIVERGENCE_PCT:-0.003}"

LOG_FILE="${LOG_FILE:-$ROOT_DIR/.check_price_health.bot.log}"
BIN_FILE="${BIN_FILE:-$ROOT_DIR/.check_price_health.bot}"

echo "[check] building bot ..."
(cd "$ROOT_DIR" && go build -o "$BIN_FILE" ./cmd/bot) >/dev/null 2>&1

echo "[check] starting bot (API_PORT=$API_PORT, PRICE_SYMBOL=$PRICE_SYMBOL) ..."
"$BIN_FILE" >"$LOG_FILE" 2>&1 &
PID="$!"
cleanup() {
  if kill -0 "$PID" >/dev/null 2>&1; then
    kill "$PID" >/dev/null 2>&1 || true
    sleep 0.5
    kill -9 "$PID" >/dev/null 2>&1 || true
  fi
  rm -f "$BIN_FILE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

STATUS_URL="http://127.0.0.1:${API_PORT}/api/status"
echo "[check] waiting for $STATUS_URL ..."
for _ in $(seq 1 40); do
  if curl -fsS "$STATUS_URL" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

PASS_OK=0
for _ in $(seq 1 60); do
  JSON="$(curl -fsS "$STATUS_URL")"
  echo "$JSON" | jq -e '.market and .risk and .decision' >/dev/null

  STALE="$(echo "$JSON" | jq -r '.market.stale')"
  AGE_MS="$(echo "$JSON" | jq -r '.market.stale_age_ms')"
  UPDATED_AT="$(echo "$JSON" | jq -r '.market.agg_updated_at')"
  PRICE="$(echo "$JSON" | jq -r '.market.agg_price')"

  echo "[check] agg_price=$PRICE agg_updated_at=$UPDATED_AT stale=$STALE stale_age_ms=$AGE_MS"
  if [[ "$STALE" == "false" ]] && [[ "$AGE_MS" =~ ^[0-9]+$ ]] && (( AGE_MS < 5000 )) && awk "BEGIN {exit !($PRICE > 500 && $PRICE < 100000)}"; then
    PASS_OK=1
    break
  fi
  sleep 0.5
done

if (( PASS_OK == 1 )); then
  echo "PASS"
  exit 0
fi

echo "FAIL (expected market.stale=false and market.stale_age_ms<5000 within ~30s)"
echo "[check] last 80 log lines from $LOG_FILE:"
tail -n 80 "$LOG_FILE" || true
exit 1
