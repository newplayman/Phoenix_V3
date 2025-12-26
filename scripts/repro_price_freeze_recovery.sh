#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${API_PORT:-18081}"

export API_PORT="$PORT"
export PHOENIX_CONFIG="${PHOENIX_CONFIG:-$ROOT_DIR/configs/config.yaml}"
export PHOENIX_FORCE_DRY_RUN="${PHOENIX_FORCE_DRY_RUN:-1}"

# WS-only (no REST polling unless explicitly requested)
export PRICE_MODE="${PRICE_MODE:-ws_only}"
export PRICE_SYMBOL="${PRICE_SYMBOL:-ETH/USDT}"

# Make freeze/recovery easy to observe locally.
export PRICE_STALE_SEC="${PRICE_STALE_SEC:-2}"
export PRICE_FREEZE_SEC="${PRICE_FREEZE_SEC:-6}"
export DIVERGENCE_PCT="${DIVERGENCE_PCT:-0.003}"

BIN_FILE="${BIN_FILE:-$ROOT_DIR/.repro_price_freeze_recovery.bot}"
LOG_FILE="${LOG_FILE:-$ROOT_DIR/.repro_price_freeze_recovery.bot.log}"
WS_BLOCK_PORTS="${WS_BLOCK_PORTS:-443,8443,9443}"
IPTABLES_COMMENT="${IPTABLES_COMMENT:-phoenix-repro-freeze}"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }
}
need curl
need jq
need go

has_nopass_sudo() {
  command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1
}

split_csv() {
  local raw="$1"
  raw="${raw// /}"
  IFS=',' read -r -a parts <<<"$raw"
  printf '%s\n' "${parts[@]}" | awk 'NF'
}

iptables_insert() {
  local cmd="$1" port="$2"
  # Prefer a tagged rule; fall back if comment match isn't available.
  if sudo -n "$cmd" -I OUTPUT -p tcp --dport "$port" -j REJECT -m comment --comment "$IPTABLES_COMMENT" >/dev/null 2>&1; then
    return 0
  fi
  sudo -n "$cmd" -I OUTPUT -p tcp --dport "$port" -j REJECT >/dev/null 2>&1
}

iptables_delete_all() {
  local cmd="$1" port="$2"
  while sudo -n "$cmd" -D OUTPUT -p tcp --dport "$port" -j REJECT -m comment --comment "$IPTABLES_COMMENT" >/dev/null 2>&1; do
    :
  done
  while sudo -n "$cmd" -D OUTPUT -p tcp --dport "$port" -j REJECT >/dev/null 2>&1; do
    :
  done
}

echo "[repro] build bot ..."
(cd "$ROOT_DIR" && go build -o "$BIN_FILE" ./cmd/bot) >/dev/null

echo "[repro] start bot (API_PORT=$API_PORT, PRICE_MODE=$PRICE_MODE, PRICE_SYMBOL=$PRICE_SYMBOL, STALE=$PRICE_STALE_SEC, FREEZE=$PRICE_FREEZE_SEC) ..."
"$BIN_FILE" >"$LOG_FILE" 2>&1 &
PID="$!"
RULES_INSTALLED="0"

cleanup() {
  if has_nopass_sudo; then
    if command -v iptables >/dev/null 2>&1; then
      while read -r p; do
        iptables_delete_all iptables "$p"
      done < <(split_csv "$WS_BLOCK_PORTS")
    fi
    if command -v ip6tables >/dev/null 2>&1; then
      while read -r p; do
        iptables_delete_all ip6tables "$p"
      done < <(split_csv "$WS_BLOCK_PORTS")
    fi
  fi
  if kill -0 "$PID" >/dev/null 2>&1; then
    kill "$PID" >/dev/null 2>&1 || true
    sleep 0.5
    kill -9 "$PID" >/dev/null 2>&1 || true
  fi
  rm -f "$BIN_FILE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

STATUS_URL="http://127.0.0.1:${API_PORT}/api/status"
echo "[repro] waiting for $STATUS_URL ..."
for _ in $(seq 1 80); do
  if curl -fsS "$STATUS_URL" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

echo "[repro] step 1/3: wait for healthy price (stale=false) ..."
for _ in $(seq 1 120); do
  JSON="$(curl -fsS "$STATUS_URL")"
  STALE="$(echo "$JSON" | jq -r '.market.stale')"
  PRICE="$(echo "$JSON" | jq -r '.market.agg_price')"
  AGE_MS="$(echo "$JSON" | jq -r '.market.stale_age_ms')"
  if [[ "$STALE" == "false" ]] && awk "BEGIN{exit !($PRICE > 500)}"; then
    echo "[repro] healthy: agg_price=$PRICE stale_age_ms=$AGE_MS"
    echo "$JSON" | jq '{market:.market,risk:.risk,decision:.decision}'
    break
  fi
  sleep 0.25
done

echo
echo "[repro] step 2/3: induce stale/frozen by blocking outbound WS traffic"
echo "[repro] - preferred: use sudo iptables/ip6tables to temporarily block outbound WS ports"
echo "[repro] - fallback: manually disable network for ~${PRICE_FREEZE_SEC}s (WiFi off / unplug), then re-enable"
echo "[repro] WS_BLOCK_PORTS=$WS_BLOCK_PORTS"

if has_nopass_sudo && command -v iptables >/dev/null 2>&1; then
  while read -r p; do
    iptables_insert iptables "$p"
  done < <(split_csv "$WS_BLOCK_PORTS")
  RULES_INSTALLED="1"

  if command -v ip6tables >/dev/null 2>&1; then
    while read -r p; do
      iptables_insert ip6tables "$p"
    done < <(split_csv "$WS_BLOCK_PORTS")
  else
    echo "[repro] warn: ip6tables not found; IPv6 may keep a feed alive"
  fi

  echo "[repro] iptables rules installed (OUTPUT REJECT tcp/{$WS_BLOCK_PORTS})"
else
  echo "[repro] no passwordless sudo/iptables; please disable network now, then press Enter to continue ..."
  read -r _
fi

echo "[repro] waiting until decision.blocked=true and risk.mode=frozen ..."
FROZEN_OK="0"
for _ in $(seq 1 160); do
  JSON="$(curl -fsS "$STATUS_URL")"
  MODE="$(echo "$JSON" | jq -r '.risk.mode')"
  DECISION_BLOCKED="$(echo "$JSON" | jq -r '.decision.blocked')"
  REASON="$(echo "$JSON" | jq -r '.decision.block_reason')"
  AGE_MS="$(echo "$JSON" | jq -r '.market.stale_age_ms')"
  echo "[repro] mode=$MODE blocked=$DECISION_BLOCKED reason=$REASON age_ms=$AGE_MS"
  if [[ "$MODE" == "frozen" ]] && [[ "$DECISION_BLOCKED" == "true" ]] && [[ "$REASON" == "price_frozen" ]]; then
    FROZEN_OK="1"
    echo "[repro] frozen reached."
    break
  fi
  sleep 0.25
done

if [[ "$FROZEN_OK" != "1" ]]; then
  echo "[repro] FAIL: did not reach frozen within expected time window." >&2
  tail -n 80 "$LOG_FILE" >&2 || true
  exit 1
fi

echo
echo "[repro] step 3/3: recover (remove block / re-enable network) ..."
if [[ "$RULES_INSTALLED" == "1" ]]; then
  if command -v iptables >/dev/null 2>&1; then
    while read -r p; do
      iptables_delete_all iptables "$p"
    done < <(split_csv "$WS_BLOCK_PORTS")
  fi
  if command -v ip6tables >/dev/null 2>&1; then
    while read -r p; do
      iptables_delete_all ip6tables "$p"
    done < <(split_csv "$WS_BLOCK_PORTS")
  fi
  echo "[repro] iptables rules removed"
else
  echo "[repro] please re-enable network now, then press Enter to continue ..."
  read -r _
fi

echo "[repro] waiting for stale=false and risk.mode!=frozen ..."
for _ in $(seq 1 200); do
  JSON="$(curl -fsS "$STATUS_URL")"
  MODE="$(echo "$JSON" | jq -r '.risk.mode')"
  STALE="$(echo "$JSON" | jq -r '.market.stale')"
  PRICE="$(echo "$JSON" | jq -r '.market.agg_price')"
  AGE_MS="$(echo "$JSON" | jq -r '.market.stale_age_ms')"
  echo "[repro] mode=$MODE stale=$STALE price=$PRICE age_ms=$AGE_MS"
  if [[ "$STALE" == "false" ]] && [[ "$MODE" != "frozen" ]] && awk "BEGIN{exit !($PRICE > 500)}"; then
    echo "[repro] recovered."
    echo "$JSON" | jq '{market:.market,risk:.risk,decision:.decision}'
    echo
    echo "[repro] market log snippet:"
    grep "\\[Market\\]" "$LOG_FILE" | tail -n 20 || true
    echo "PASS"
    exit 0
  fi
  sleep 0.25
done

echo "[repro] FAIL: did not recover within expected time window." >&2
tail -n 120 "$LOG_FILE" >&2 || true
exit 1
