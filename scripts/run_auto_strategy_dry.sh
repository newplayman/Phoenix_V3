#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

API_PORT="${API_PORT:-18081}"
PHOENIX_CONFIG="${PHOENIX_CONFIG:-$ROOT_DIR/configs/config.yaml}"

mkdir -p "$ROOT_DIR/tmp" >/dev/null 2>&1 || true

PID_FILE="${PID_FILE:-$ROOT_DIR/tmp/auto_strategy.pid}"
LOG_FILE="${LOG_FILE:-$ROOT_DIR/tmp/auto_strategy.log}"
BIN_FILE="${BIN_FILE:-$ROOT_DIR/tmp/auto_strategy.bot}"

STOP="${STOP:-0}"
if [[ "$STOP" == "1" ]]; then
  if [[ -s "$PID_FILE" ]]; then
    PID="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [[ "$PID" =~ ^[0-9]+$ ]] && kill -0 "$PID" >/dev/null 2>&1; then
      echo "[run] stopping bot pid=$PID ..."
      kill "$PID" >/dev/null 2>&1 || true
      sleep 0.5
      kill -9 "$PID" >/dev/null 2>&1 || true
      echo "[run] stopped"
      exit 0
    fi
  fi
  echo "[run] no running bot found (PID_FILE=$PID_FILE)"
  exit 0
fi

echo "[run] building bot ..."
(cd "$ROOT_DIR" && go build -o "$BIN_FILE" ./cmd/bot) >/dev/null

export API_PORT="$API_PORT"
export PHOENIX_CONFIG="$PHOENIX_CONFIG"

# Safety defaults: never broadcast.
export PHOENIX_FORCE_DRY_RUN="${PHOENIX_FORCE_DRY_RUN:-1}"

# Auto Evaluate loop.
export PHOENIX_AUTO_EVAL="${PHOENIX_AUTO_EVAL:-1}"
export PHOENIX_STRATEGY_KIND="${PHOENIX_STRATEGY_KIND:-mock}"
export DECISION_INTERVAL_SEC="${DECISION_INTERVAL_SEC:-5}"

# Optional: enable V3 rebalance strategy (takes priority over mock).
ENABLE_V3="${ENABLE_V3:-0}"
if [[ "$ENABLE_V3" == "1" ]]; then
  export STRAT_V3_ENABLED=1
fi

# Market feed: WS-only by default.
export PRICE_MODE="${PRICE_MODE:-ws_only}"
export PRICE_SYMBOL="${PRICE_SYMBOL:-ETH/USDT}"

echo "[run] starting bot (API_PORT=$API_PORT, AUTO_EVAL=$PHOENIX_AUTO_EVAL, STRATEGY=$PHOENIX_STRATEGY_KIND, DRY_RUN=$PHOENIX_FORCE_DRY_RUN) ..."
nohup "$BIN_FILE" >"$LOG_FILE" 2>&1 &
PID="$!"
printf '%s\n' "$PID" >"$PID_FILE"

echo "[run] pid=$PID (PID_FILE=$PID_FILE)"
echo "[run] log=$LOG_FILE"
echo "[run] status: curl -s http://127.0.0.1:${API_PORT}/api/status | jq '.decision'"
echo "[run] stop: STOP=1 PID_FILE=$PID_FILE bash scripts/run_auto_strategy_dry.sh"
