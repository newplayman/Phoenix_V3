#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[real-univ3-mint] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require go
require curl
require jq
require rg
require ss
require timeout

[[ -n "${ARBITRUM_SEPOLIA_RPC_URL:-}" ]] || fail "missing ARBITRUM_SEPOLIA_RPC_URL"
[[ -n "${BOT_PRIVATE_KEY_FILE:-}" ]] || fail "missing BOT_PRIVATE_KEY_FILE (local-only; do not commit)"
[[ "${REAL_UNIV3_MINT_CONFIRM:-}" == "I_UNDERSTAND_GAS_COSTS" ]] || fail "missing REAL_UNIV3_MINT_CONFIRM=I_UNDERSTAND_GAS_COSTS"

[[ -n "${POOL_ID:-}" ]] || fail "missing POOL_ID"
[[ -n "${POOL_ADDRESS:-}" ]] || fail "missing POOL_ADDRESS"
[[ -n "${POSITION_MANAGER_ADDRESS:-}" ]] || fail "missing POSITION_MANAGER_ADDRESS"
[[ -n "${TOKEN0_ADDRESS:-}" ]] || fail "missing TOKEN0_ADDRESS"
[[ -n "${TOKEN1_ADDRESS:-}" ]] || fail "missing TOKEN1_ADDRESS"
[[ -n "${TOKEN0_DECIMALS:-}" ]] || fail "missing TOKEN0_DECIMALS"
[[ -n "${TOKEN1_DECIMALS:-}" ]] || fail "missing TOKEN1_DECIMALS"
[[ -n "${POOL_FEE:-}" ]] || fail "missing POOL_FEE"
[[ -n "${STABLE_TOKEN_ADDRESS:-}" ]] || fail "missing STABLE_TOKEN_ADDRESS (must be TOKEN0_ADDRESS or TOKEN1_ADDRESS)"
[[ -n "${CEX_PRICE_TOKEN_ADDRESS:-}" ]] || fail "missing CEX_PRICE_TOKEN_ADDRESS (must be TOKEN0_ADDRESS or TOKEN1_ADDRESS)"

stable_lower="$(echo "$STABLE_TOKEN_ADDRESS" | tr '[:upper:]' '[:lower:]')"
cex_lower="$(echo "$CEX_PRICE_TOKEN_ADDRESS" | tr '[:upper:]' '[:lower:]')"
t0_lower="$(echo "$TOKEN0_ADDRESS" | tr '[:upper:]' '[:lower:]')"
t1_lower="$(echo "$TOKEN1_ADDRESS" | tr '[:upper:]' '[:lower:]')"
if [[ "$stable_lower" != "$t0_lower" && "$stable_lower" != "$t1_lower" ]]; then
  fail "STABLE_TOKEN_ADDRESS must be TOKEN0_ADDRESS or TOKEN1_ADDRESS"
fi
if [[ "$cex_lower" != "$t0_lower" && "$cex_lower" != "$t1_lower" ]]; then
  fail "CEX_PRICE_TOKEN_ADDRESS must be TOKEN0_ADDRESS or TOKEN1_ADDRESS"
fi
if [[ "$stable_lower" == "$cex_lower" ]]; then
  fail "STABLE_TOKEN_ADDRESS must differ from CEX_PRICE_TOKEN_ADDRESS"
fi

port_in_use() {
  ss -ltn | awk '{print $4}' | rg -q '(:|\\])8081$'
}

try_kill_existing_phoenix_bot() {
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
      echo "[real-univ3-mint] port 8081 in use by phoenix bot (pid=$pid); stopping it ..."
      kill "$pid" >/dev/null 2>&1 || true
      killed=1
    fi
  done
  if [[ "$killed" -eq 0 ]]; then
    return 1
  fi
  for _ in {1..30}; do
    if ! port_in_use; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

if port_in_use; then
  if ! try_kill_existing_phoenix_bot; then
    fail "port 8081 already in use; stop the existing process"
  fi
fi

wallet_addr="${BOT_WALLET_ADDRESS:-}"
if [[ -z "$wallet_addr" ]]; then
  wallet_addr="$(BOT_PRIVATE_KEY_FILE="$BOT_PRIVATE_KEY_FILE" make -s wallet-addr 2>/dev/null | rg -o '0x[0-9a-fA-F]{40}' | head -n 1 || true)"
fi
[[ -n "$wallet_addr" ]] || fail "unable to derive BOT_WALLET_ADDRESS (set BOT_WALLET_ADDRESS or provide readable BOT_PRIVATE_KEY_FILE)"

echo "[real-univ3-mint] wallet=$wallet_addr"

echo "[real-univ3-mint] 1/6 preflight: contract code present on Arbitrum Sepolia"
RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" scripts/check_contract_code.sh \
  "$POOL_ADDRESS" "$POSITION_MANAGER_ADDRESS" "$TOKEN0_ADDRESS" "$TOKEN1_ADDRESS" >/dev/null

ADMIN_TOKEN="${ADMIN_TOKEN:-}"
if [[ -z "$ADMIN_TOKEN" ]]; then
  ADMIN_TOKEN="$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)"
  export ADMIN_TOKEN
fi
auth_header=(-H "Authorization: Bearer ${ADMIN_TOKEN}")

cfg_preview="/tmp/phoenix_real_univ3_preview.yaml"
cat >"$cfg_preview" <<'YAML'
schema_version: "2024-07-01"
strategy_version: "basic-v1"

api:
  enable_legacy: false
  control_plane_enabled: false

safety:
  kill_switch: true
  allow_tx_broadcast: false

wallet:
  min_idle_pct: 0.99

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
    max_cap_pct: 0.001
    max_daily_rebalances: 1
    amount0: "0"
    amount1: "0"
    stable_tokens:
      - "${STABLE_TOKEN_ADDRESS}"

strategy:
  name: "basic_rebalance"
  dry_run: true
  rebalance:
    min_interval: "10m"
  range:
    min_width_pct: 0.01
    max_width_pct: 0.05
    vol_k: 0.05
    vol_window: "10m"

risk:
  max_daily_gas: 0.005
  max_drawdown: 0.10
  consecutive_fails: 3
  max_utilization_pct: 0.25
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

echo "[real-univ3-mint] 2/6 start bot (dry-run preview)"
export PHOENIX_CONTROL_PLANE_ENABLED=1
export CONFIG_PATH="$cfg_preview"
export BOT_WALLET_ADDRESS="$wallet_addr"

BOT_BIN="${BOT_BIN:-/tmp/phoenix_bot_real_univ3_mint}"
LOG_PATH="${LOG_PATH:-/tmp/phoenix_real_univ3_mint.log}"
export PHOENIX_DB_PATH="${PHOENIX_DB_PATH:-/tmp/phoenix_real_univ3_mint.sqlite}"
rm -f "$PHOENIX_DB_PATH"
go build -o "$BOT_BIN" ./cmd/bot >/dev/null

"$BOT_BIN" -config "$cfg_preview" -dry-run -no-monitor -offline-feed -manual-only >"$LOG_PATH" 2>&1 &
BOT_PID="$!"
trap 'kill "$BOT_PID" >/dev/null 2>&1 || true' EXIT

echo "[real-univ3-mint] 3/6 wait for API"
for _ in {1..120}; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/health" 2>/dev/null || true)"
  if [[ "$code" == "200" ]]; then
    break
  fi
  sleep 0.5
done

echo "[real-univ3-mint] wait for pool state (live read, no tx) ..."
state=""
for _ in {1..180}; do
  state="$(curl -sS "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/pools/${POOL_ID}/state" 2>/dev/null || true)"
  if jq -e '.pool_id == "'"$POOL_ID"'" and .dex.price_stable_per_weth > 0 and .cex.price_stable_per_weth > 0' >/dev/null 2>&1 <<<"$state"; then
    break
  fi
  sleep 0.5
done
if ! jq -e '.pool_id == "'"$POOL_ID"'" and .dex.price_stable_per_weth > 0 and .cex.price_stable_per_weth > 0' >/dev/null 2>&1 <<<"$state"; then
  fail "pool state not ready after wait: $state (see $LOG_PATH)"
fi

echo "[real-univ3-mint] 4/6 preview plan + safety prechecks"
idem="real-mint-preview-$(date +%s)-$RANDOM"
preview_payload="$(jq -n \
  --arg action "force_rebalance" \
  --arg pool "$POOL_ID" \
  --argjson chain 421614 \
  --arg ikey "$idem" \
  '{action_type:$action,pool_id:$pool,chain_id:$chain,params:{},idempotency_key:$ikey}')"

preview_resp="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$preview_payload" "http://127.0.0.1:8081/api/v1/operations/preview" || true)"
err_code="$(jq -r '.error.code // empty' <<<"$preview_resp" 2>/dev/null || true)"
err_msg="$(jq -r '.error.message // empty' <<<"$preview_resp" 2>/dev/null || true)"
if [[ -n "$err_code" ]]; then
  if [[ "$err_code" == "plan_failed" && "$err_msg" == "total equity is zero" ]]; then
    echo "[real-univ3-mint] preview failed: total equity is zero; wallet must hold token0/token1 on Arbitrum Sepolia (no tx sent)"
    echo "[real-univ3-mint] token0 balance:"
    ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN0_ADDRESS" -owner "$wallet_addr" || true
    echo "[real-univ3-mint] token1 balance:"
    ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN1_ADDRESS" -owner "$wallet_addr" || true
    exit 2
  fi
  fail "preview failed: $preview_resp"
fi

swap_steps="$(jq -r '[.plan[]? | select(.step_type == "swap")] | length' <<<"$preview_resp" 2>/dev/null || echo 0)"
if [[ "${swap_steps:-0}" != "0" ]]; then
  fail "preview requires swaps (swap_steps=$swap_steps); configure SwapHelper/Router first or fund correct token ratio; response=$preview_resp"
fi

mint_summary="$(jq -r '.plan[]? | select(.step_type == "mint") | .summary' <<<"$preview_resp" 2>/dev/null || true)"
[[ -n "$mint_summary" && "$mint_summary" != "null" ]] || fail "preview missing mint step; response=$preview_resp"

req0="$(rg -o 'amount0=[0-9]+' <<<"$mint_summary" | head -n 1 | cut -d= -f2 || true)"
req1="$(rg -o 'amount1=[0-9]+' <<<"$mint_summary" | head -n 1 | cut -d= -f2 || true)"
if [[ -z "$req0" || -z "$req1" ]]; then
  fail "unable to parse mint amounts from summary: $mint_summary"
fi

bal0="$(ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN0_ADDRESS" -owner "$wallet_addr" -json | jq -r '.balance_raw')"
bal1="$(ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TOKEN1_ADDRESS" -owner "$wallet_addr" -json | jq -r '.balance_raw')"

if [[ "$bal0" -lt "$req0" || "$bal1" -lt "$req1" ]]; then
  fail "insufficient token balances for mint (need amount0=$req0 amount1=$req1; have0=$bal0 have1=$bal1). Fund TOKEN0/TOKEN1 on Arbitrum Sepolia first; no tx sent."
fi

echo "[real-univ3-mint] 5/6 unlock broadcast + execute"
kill "$BOT_PID" >/dev/null 2>&1 || true
wait "$BOT_PID" >/dev/null 2>&1 || true
trap - EXIT

cfg_live="/tmp/phoenix_real_univ3_live_mint.yaml"
sed 's/dry_run: true/dry_run: false/' "$cfg_preview" | sed 's/kill_switch: true/kill_switch: false/' | sed 's/allow_tx_broadcast: false/allow_tx_broadcast: true/' >"$cfg_live"

"$BOT_BIN" -config "$cfg_live" -no-monitor -offline-feed -manual-only >"$LOG_PATH" 2>&1 &
BOT_PID="$!"
trap 'kill "$BOT_PID" >/dev/null 2>&1 || true' EXIT

for _ in {1..120}; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/health" 2>/dev/null || true)"
  if [[ "$code" == "200" ]]; then
    break
  fi
  sleep 0.5
done

echo "[real-univ3-mint] wait for pool state (live read, no tx) ..."
state=""
for _ in {1..180}; do
  state="$(curl -sS "${auth_header[@]}" "http://127.0.0.1:8081/api/v1/pools/${POOL_ID}/state" 2>/dev/null || true)"
  if jq -e '.pool_id == "'"$POOL_ID"'" and .dex.price_stable_per_weth > 0 and .cex.price_stable_per_weth > 0' >/dev/null 2>&1 <<<"$state"; then
    break
  fi
  sleep 0.5
done
if ! jq -e '.pool_id == "'"$POOL_ID"'" and .dex.price_stable_per_weth > 0 and .cex.price_stable_per_weth > 0' >/dev/null 2>&1 <<<"$state"; then
  fail "pool state not ready after restart: $state (see $LOG_PATH)"
fi

preview_resp2="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$preview_payload" "http://127.0.0.1:8081/api/v1/operations/preview" || true)"
op_id="$(jq -r '.operation_id // empty' <<<"$preview_resp2" 2>/dev/null || true)"
[[ -n "$op_id" ]] || fail "missing operation_id from preview: $preview_resp2"

exec_payload="$(jq -n \
  --arg op "$op_id" \
  --arg pool "$POOL_ID" \
  --arg reason "real-univ3-mint-testnet" \
  --arg ikey "exec-$idem" \
  '{operation_id:$op,pool_id:$pool,confirm_text:"CONFIRM",reason:$reason,idempotency_key:$ikey}')"
exec_resp="$(curl -sS "${auth_header[@]}" -H "Content-Type: application/json" -d "$exec_payload" "http://127.0.0.1:8081/api/v1/operations/execute" || true)"
intent_id="$(jq -r '.intent_id // empty' <<<"$exec_resp" 2>/dev/null || true)"
[[ -n "$intent_id" ]] || fail "execute failed: $exec_resp"
echo "[real-univ3-mint] intent_id=$intent_id"

intent_json=""
mint_count=0
for _ in {1..180}; do
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
  echo "[real-univ3-mint] position_token_id=${position_token_id}"
fi

tx_count="$(echo "$intent_json" | jq -r '[.steps[] | select(.tx_hash != null and .tx_hash != "")] | length' 2>/dev/null || echo 0)"
if [[ "${tx_count:-0}" == "0" ]]; then
  fail "no tx hashes found in intent steps; expected at least mint tx"
fi

echo "[real-univ3-mint] 6/6 verify tx hashes (if present) ..."
for h in $(echo "$intent_json" | jq -r '.steps[] | select(.tx_hash != null and .tx_hash != "") | .tx_hash'); do
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-wait >/dev/null
  TX_HASH="$h" ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" make -s tx-verify >/dev/null
  echo "[real-univ3-mint] verified $h"
done

echo "[real-univ3-mint] OK"
