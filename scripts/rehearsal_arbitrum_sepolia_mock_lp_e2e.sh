#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[mock-lp-e2e] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require go
require curl
require jq

[[ -n "${ARBITRUM_SEPOLIA_RPC_URL:-}" ]] || fail "missing ARBITRUM_SEPOLIA_RPC_URL"
[[ -n "${BOT_PRIVATE_KEY_FILE:-}" ]] || fail "missing BOT_PRIVATE_KEY_FILE"
[[ "${MOCKLP_E2E_CONFIRM:-}" == "I_UNDERSTAND_GAS_COSTS" ]] || fail "missing MOCKLP_E2E_CONFIRM=I_UNDERSTAND_GAS_COSTS"

if [[ -z "${ADMIN_TOKEN:-}" ]]; then
  ADMIN_TOKEN="$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)"
  export ADMIN_TOKEN
fi

VENV="${PHOENIX_VENV:-/tmp/phoenix_venv}"
PY="${VENV}/bin/python"
PIP="${VENV}/bin/pip"

if [[ ! -x "$PY" ]]; then
  python3 -m venv "$VENV"
  "$PIP" install -r scripts/requirements.txt >/dev/null
fi

echo "[mock-lp-e2e] deploying mock LP stack (testnet gas) ..."
deploy_json="$(
  MOCKLP_CONFIRM=I_UNDERSTAND_TESTNET_GAS \
    "$PY" scripts/mock_lp_stack_setup.py deploy \
    --rpc "$ARBITRUM_SEPOLIA_RPC_URL" \
    --key-file "$BOT_PRIVATE_KEY_FILE"
)"

deploy_path="/tmp/phoenix_mock_lp_stack.json"
echo "$deploy_json" >"$deploy_path"

export POOL_ID
POOL_ID="$(jq -r '.exports.POOL_ID' "$deploy_path")"
export POOL_ADDRESS POSITION_MANAGER_ADDRESS TOKEN0_ADDRESS TOKEN1_ADDRESS TOKEN0_DECIMALS TOKEN1_DECIMALS POOL_FEE STABLE_TOKEN_ADDRESS CEX_PRICE_TOKEN_ADDRESS
POOL_ADDRESS="$(jq -r '.exports.POOL_ADDRESS' "$deploy_path")"
POSITION_MANAGER_ADDRESS="$(jq -r '.exports.POSITION_MANAGER_ADDRESS' "$deploy_path")"
TOKEN0_ADDRESS="$(jq -r '.exports.TOKEN0_ADDRESS' "$deploy_path")"
TOKEN1_ADDRESS="$(jq -r '.exports.TOKEN1_ADDRESS' "$deploy_path")"
TOKEN0_DECIMALS="$(jq -r '.exports.TOKEN0_DECIMALS' "$deploy_path")"
TOKEN1_DECIMALS="$(jq -r '.exports.TOKEN1_DECIMALS' "$deploy_path")"
POOL_FEE="$(jq -r '.exports.POOL_FEE' "$deploy_path")"
STABLE_TOKEN_ADDRESS="$(jq -r '.exports.STABLE_TOKEN_ADDRESS' "$deploy_path")"
CEX_PRICE_TOKEN_ADDRESS="$(jq -r '.exports.CEX_PRICE_TOKEN_ADDRESS' "$deploy_path")"

echo "[mock-lp-e2e] deployed:"
echo "  pool=$POOL_ADDRESS"
echo "  position_manager=$POSITION_MANAGER_ADDRESS"
echo "  token0=$TOKEN0_ADDRESS (decimals=$TOKEN0_DECIMALS)"
echo "  token1=$TOKEN1_ADDRESS (decimals=$TOKEN1_DECIMALS)"

exec_cfg="/tmp/phoenix_mock_lp_execute.yaml"
cat >"$exec_cfg" <<'YAML'
schema_version: "2024-07-01"
strategy_version: "basic-v1"

api:
  enable_legacy: false
  control_plane_enabled: false

safety:
  kill_switch: false
  allow_tx_broadcast: true

events:
  driver: "memory"
  file_path: "logs/events.jsonl"
  replay_retention: "24h"
  acks_required: false

wallet:
  min_idle_pct: 0.80

chains:
  - id: 421614
    name: "arbitrum-sepolia"
    rpc: "${ARBITRUM_SEPOLIA_RPC_URL}"
    quoter_address: ""
    swap_helper_address: ""

pools:
  - id: "${POOL_ID}"
    chain_id: 421614
    token0: "${TOKEN0_ADDRESS}"
    token1: "${TOKEN1_ADDRESS}"
    cex_price_token: "${CEX_PRICE_TOKEN_ADDRESS}"
    token0_decimals: ${TOKEN0_DECIMALS}
    token1_decimals: ${TOKEN1_DECIMALS}
    fee: ${POOL_FEE}
    address: "${POOL_ADDRESS}"
    position_manager: "${POSITION_MANAGER_ADDRESS}"
    position_token_id: ""
    max_investment: "0.0"
    max_cap_pct: 0.01
    max_daily_rebalances: 5
    amount0: "0"
    amount1: "0"
    stable_tokens:
      - "${STABLE_TOKEN_ADDRESS}"

strategy:
  name: "basic_rebalance"
  dry_run: false
  rebalance:
    min_interval: "30s"
  range:
    min_width_pct: 0.01
    max_width_pct: 0.05
    vol_k: 0.05
    vol_window: "10m"

risk:
  max_daily_gas: 0.02
  max_drawdown: 0.10
  consecutive_fails: 5
  max_utilization_pct: 0.5
  max_swap_slippage_pct: 0.01

gateway:
  gas_multiplier: 1.0
  max_retries: 3
  retry_backoff_ms: 1500
  gas_bump_pct: 0.15
  approval_multiplier: 1.05
  preflight: true

monitoring:
  port: 8082
YAML

echo "[mock-lp-e2e] starting Phoenix (manual-only, live execute) ..."
export PHOENIX_CONTROL_PLANE_ENABLED=1
export CONFIG_PATH="$exec_cfg"

intent_id_file="/tmp/phoenix_mock_lp_intent_id.txt"
rm -f "$intent_id_file"
pid_file="/tmp/phoenix_mock_lp_bot.pid"
rm -f "$pid_file"

BOT_FLAGS="-config $exec_cfg -no-monitor -offline-feed -manual-only" \
  POOL_ID="$POOL_ID" \
  CHAIN_ID=421614 \
  OUT_INTENT_ID_FILE="$intent_id_file" \
  OUT_BOT_PID_FILE="$pid_file" \
  KEEP_BOT_RUNNING=1 \
  scripts/accept_control_plane_v1.sh

if [[ ! -f "$intent_id_file" ]]; then
  fail "missing intent id output (OUT_INTENT_ID_FILE)"
fi
intent_id="$(cat "$intent_id_file")"
echo "[mock-lp-e2e] intent_id=$intent_id"

auth_header=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
echo "[mock-lp-e2e] waiting for intent completion (mint step mined/failed) ..."
intent_json=""
for _ in {1..240}; do
  intent_json="$(curl -sS "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/intents/${intent_id}" || true)"
  intent_status="$(jq -r '.intent.status // ""' <<<"$intent_json" 2>/dev/null || true)"
  mint_status="$(jq -r '.steps[]? | select(.step_type == "mint") | .status' <<<"$intent_json" 2>/dev/null | head -n 1 || true)"
  if [[ "$mint_status" == "mined" || "$mint_status" == "failed" ]]; then
    break
  fi
  if [[ "$intent_status" == "succeeded" || "$intent_status" == "failed" || "$intent_status" == "simulated" ]]; then
    break
  fi
  sleep 1
done

intent_json_path="/tmp/phoenix_mock_lp_intent.json"
echo "$intent_json" >"$intent_json_path"

echo "[mock-lp-e2e] intent status:"
jq -r '.intent | {intent_id, status, pool_id, chain_id}' "$intent_json_path" || true
echo "[mock-lp-e2e] steps:"
jq -r '.steps[]? | "step=" + .step_type + " status=" + .status + " tx=" + (.tx_hash // "")' "$intent_json_path" || true

echo "[mock-lp-e2e] verifying tx hashes (if present) ..."
for h in $(jq -r '.steps[]? | select(.tx_hash != null and .tx_hash != "") | .tx_hash' "$intent_json_path"); do
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-wait >/dev/null || true
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-verify || true
done

if [[ -f "$pid_file" ]]; then
  pid="$(cat "$pid_file" || true)"
  if [[ -n "$pid" ]]; then
    kill "$pid" >/dev/null 2>&1 || true
  fi
fi

echo "[mock-lp-e2e] OK"
