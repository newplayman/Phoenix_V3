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

VENV="${PHOENIX_VENV:-/tmp/phoenix_venv}"
PY="${VENV}/bin/python"
PIP="${VENV}/bin/pip"

if [[ ! -x "$PY" ]]; then
  python3 -m venv "$VENV"
  "$PIP" install -r scripts/requirements.txt >/dev/null
fi

deploy_path="${MOCKLP_STACK_JSON:-/tmp/phoenix_mock_lp_stack.json}"
if [[ "${MOCKLP_REUSE_EXISTING:-}" == "1" && -f "$deploy_path" ]]; then
  echo "[mock-lp-e2e] reusing existing mock LP stack JSON: $deploy_path"
else
  echo "[mock-lp-e2e] deploying mock LP stack (testnet gas) ..."
  deploy_json="$(
    MOCKLP_CONFIRM=I_UNDERSTAND_TESTNET_GAS \
      "$PY" scripts/mock_lp_stack_setup.py deploy \
      --rpc "$ARBITRUM_SEPOLIA_RPC_URL" \
      --key-file "$BOT_PRIVATE_KEY_FILE"
  )"
  echo "$deploy_json" >"$deploy_path"
fi

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

if [[ -z "${ADMIN_TOKEN:-}" ]]; then
  ADMIN_TOKEN="$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)"
  export ADMIN_TOKEN
fi

intent_id_file="/tmp/phoenix_mock_lp_intent_id.txt"
rm -f "$intent_id_file"

BOT_BIN="${BOT_BIN:-/tmp/phoenix_bot_mock_lp_e2e}"
LOG_PATH="${LOG_PATH:-/tmp/phoenix_mock_lp_e2e.log}"
export PHOENIX_DB_PATH="${PHOENIX_DB_PATH:-/tmp/phoenix_mock_lp_e2e.sqlite}"
rm -f "$PHOENIX_DB_PATH"
go build -o "$BOT_BIN" ./cmd/bot >/dev/null

# Keep the bot running while we query intent details/tx hashes after acceptance.
"$BOT_BIN" -config "$exec_cfg" -no-monitor -offline-feed -manual-only >"$LOG_PATH" 2>&1 &
BOT_PID="$!"
trap 'kill "$BOT_PID" >/dev/null 2>&1 || true' EXIT

# Wait for API to listen (accept script requires port ready when SKIP_START_BOT=1).
auth_header=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
for _ in {1..120}; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/health" 2>/dev/null || true)"
  if [[ "$code" == "200" ]]; then
    break
  fi
  sleep 0.2
done

SKIP_START_BOT=1 \
  LOG_PATH="$LOG_PATH" \
  BOT_FLAGS="-config $exec_cfg -no-monitor -offline-feed -manual-only" \
  POOL_ID="$POOL_ID" \
  CHAIN_ID=421614 \
  OUT_INTENT_ID_FILE="$intent_id_file" \
  scripts/accept_control_plane_v1.sh

if [[ ! -f "$intent_id_file" ]]; then
  fail "missing intent id output (OUT_INTENT_ID_FILE)"
fi
intent_id="$(cat "$intent_id_file")"
echo "[mock-lp-e2e] intent_id=$intent_id"

auth_header=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
intent_json=""
for _ in {1..120}; do
  intent_json="$(curl -sS "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/intents/${intent_id}" || true)"
  mint_count="$(echo "$intent_json" | jq -r '[.steps[] | select(.step_type == "mint" and .tx_hash != null and .tx_hash != "")] | length' 2>/dev/null || echo 0)"
  if [[ "${mint_count:-0}" != "0" ]]; then
    break
  fi
  sleep 1
done

echo "$intent_json" | jq -r '.steps[] | "step=\(.step_type) status=\(.status) tx=\(.tx_hash // "")"' || true

position_token_id="$(echo "$intent_json" | jq -r '.intent.metadata.position_token_id // ""' 2>/dev/null || true)"
if [[ -n "${position_token_id}" && "${position_token_id}" != "null" ]]; then
  echo "[mock-lp-e2e] position_token_id=${position_token_id}"
fi

tx_count="$(echo "$intent_json" | jq -r '[.steps[] | select(.tx_hash != null and .tx_hash != "")] | length')"
if [[ "${tx_count:-0}" == "0" ]]; then
  fail "no tx hashes found in intent steps; expected at least 1 broadcast tx (mint)"
fi

mint_count="$(echo "$intent_json" | jq -r '[.steps[] | select(.step_type == "mint" and .tx_hash != null and .tx_hash != "")] | length')"
if [[ "${mint_count:-0}" == "0" ]]; then
  fail "no mint step with tx hash found after wait; expected a mint tx for mock-lp e2e"
fi

echo "[mock-lp-e2e] verifying tx hashes (if present) ..."
for h in $(echo "$intent_json" | jq -r '.steps[] | select(.tx_hash != null and .tx_hash != "") | .tx_hash'); do
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-wait >/dev/null
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-verify >/dev/null
  echo "[mock-lp-e2e] verified $h"
done

echo "[mock-lp-e2e] OK"
