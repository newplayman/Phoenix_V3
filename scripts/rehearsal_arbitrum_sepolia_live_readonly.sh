#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require go
require npm
require curl
require jq
require ss

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "missing required env: $k" >&2
    exit 2
  fi
}

# Live read-only rehearsal:
# - reads pool state from Arbitrum Sepolia RPC (no -offline)
# - does NOT enable control plane and does NOT broadcast any tx
export CONFIG_PATH="${CONFIG_PATH:-configs/config_arbitrum_sepolia.template.yaml}"
export API_BASE="${API_BASE:-http://127.0.0.1:8081}"
export ADMIN_TOKEN="${ADMIN_TOKEN:-testtoken}"

require_env ARBITRUM_SEPOLIA_RPC_URL
require_env POOL_ADDRESS

# Deterministic placeholders for required template env.
export POOL_ID="${POOL_ID:-arbsepolia-live-read}"
export TOKEN0_ADDRESS="${TOKEN0_ADDRESS:-0x1111111111111111111111111111111111111111}"
export TOKEN1_ADDRESS="${TOKEN1_ADDRESS:-0x2222222222222222222222222222222222222222}"
export STABLE_TOKEN_ADDRESS="${STABLE_TOKEN_ADDRESS:-$TOKEN1_ADDRESS}"
export CEX_PRICE_TOKEN_ADDRESS="${CEX_PRICE_TOKEN_ADDRESS:-$TOKEN0_ADDRESS}"
export POSITION_MANAGER_ADDRESS="${POSITION_MANAGER_ADDRESS:-0x4444444444444444444444444444444444444444}"
export TOKEN0_DECIMALS="${TOKEN0_DECIMALS:-18}"
export TOKEN1_DECIMALS="${TOKEN1_DECIMALS:-6}"
export POOL_FEE="${POOL_FEE:-500}"

echo "[rehearsal-live] make ci"
make ci

echo "[rehearsal-live] integration: chainId=421614"
ARBITRUM_SEPOLIA_RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL}" go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID >/dev/null

echo "[rehearsal-live] contract preflight (POOL_ADDRESS must have code)"
RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL}" EXPECTED_CHAIN_ID=421614 scripts/check_contract_code.sh "${POOL_ADDRESS}"

echo "[rehearsal-live] validate template"
scripts/validate_arbitrum_sepolia_template.sh

echo "[rehearsal-live] start bot (dry-run + live pool read)"
BOT_PID=""
cleanup() {
  if [[ -n "${BOT_PID:-}" ]]; then
    kill "${BOT_PID}" >/dev/null 2>&1 || true
    wait "${BOT_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

BOT_BIN="${BOT_BIN:-/tmp/phoenix_bot_rehearsal_live}"
go build -o "$BOT_BIN" ./cmd/bot >/dev/null

PHOENIX_CONTROL_PLANE_ENABLED=0 \
  "$BOT_BIN" -config "${CONFIG_PATH}" -dry-run -offline-feed -manual-only -no-monitor >/dev/null 2>&1 &
BOT_PID=$!

echo "[rehearsal-live] wait for API"
deadline=$((SECONDS + 45))
while true; do
  if curl -sfS -H "Authorization: Bearer ${ADMIN_TOKEN}" "${API_BASE}/api/v1/health" >/dev/null 2>&1; then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "timeout waiting for api at ${API_BASE}" >&2
    ss -ltnp 2>/dev/null || true
    exit 2
  fi
  sleep 1
done

echo "[rehearsal-live] wait for live pool state"
deadline=$((SECONDS + 90))
while true; do
  state="$(curl -sfS -H "Authorization: Bearer ${ADMIN_TOKEN}" "${API_BASE}/api/v1/pools/${POOL_ID}/state" || true)"
  price="$(jq -r '.dex.price_stable_per_weth // 0' <<<"$state" 2>/dev/null || echo 0)"
  if [[ "${price}" != "0" && "${price}" != "0.0" && "${price}" != "null" ]]; then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "timeout waiting for live pool state: ${state}" >&2
    exit 2
  fi
  sleep 2
done

echo "[rehearsal-live] read-only API smoke"
API_BASE="${API_BASE}" ADMIN_TOKEN="${ADMIN_TOKEN}" scripts/smoke_api_v1_readonly.sh

echo "[rehearsal-live] OK"

