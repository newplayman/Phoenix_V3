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

# This is a TESTNET dry-run rehearsal:
# - Validates Arbitrum Sepolia RPC connectivity (chainId integration test)
# - Runs read-only /api/v1/* smoke against an offline pool-state (no tx broadcast, no pool dependency)
# - Uses offline feed to avoid external price providers

export CONFIG_PATH="${CONFIG_PATH:-configs/config_arbitrum_sepolia.template.yaml}"
export API_BASE="${API_BASE:-http://127.0.0.1:8081}"
export ADMIN_TOKEN="${ADMIN_TOKEN:-testtoken}"

require_env ARBITRUM_SEPOLIA_RPC_URL

# Provide deterministic placeholder pool/token params so this rehearsal only needs a live RPC.
# These values are used only to satisfy config validation and to make read-only APIs return data.
export POOL_ID="${POOL_ID:-arbsepolia-dryrun}"
export TOKEN0_ADDRESS="${TOKEN0_ADDRESS:-0x1111111111111111111111111111111111111111}"
export TOKEN1_ADDRESS="${TOKEN1_ADDRESS:-0x2222222222222222222222222222222222222222}"
export STABLE_TOKEN_ADDRESS="${STABLE_TOKEN_ADDRESS:-$TOKEN1_ADDRESS}"
export CEX_PRICE_TOKEN_ADDRESS="${CEX_PRICE_TOKEN_ADDRESS:-$TOKEN0_ADDRESS}"
export POOL_ADDRESS="${POOL_ADDRESS:-0x3333333333333333333333333333333333333333}"
export POSITION_MANAGER_ADDRESS="${POSITION_MANAGER_ADDRESS:-0x4444444444444444444444444444444444444444}"
export TOKEN0_DECIMALS="${TOKEN0_DECIMALS:-18}"
export TOKEN1_DECIMALS="${TOKEN1_DECIMALS:-6}"
export POOL_FEE="${POOL_FEE:-500}"

# Preview-time balance reads can be done with address only (no private key required).
# Default to a non-zero dummy address so read-only rehearsals don't require wallet material.
export BOT_WALLET_ADDRESS="${BOT_WALLET_ADDRESS:-0x5555555555555555555555555555555555555555}"

echo "[rehearsal-testnet] make ci"
make ci

echo "[rehearsal-testnet] integration: chainId=421614"
ARBITRUM_SEPOLIA_RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL}" go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID >/dev/null
if [[ -n "${ARBITRUM_SEPOLIA_POOL_ADDRESS:-}" ]]; then
  echo "[rehearsal-testnet] integration: pool state (optional)"
  ARBITRUM_SEPOLIA_RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL}" ARBITRUM_SEPOLIA_POOL_ADDRESS="${ARBITRUM_SEPOLIA_POOL_ADDRESS}" \
    go test ./internal/dexstate -tags=integration -run TestArbitrumSepoliaPoolState_Slot0AndLiquidity >/dev/null || true
fi

echo "[rehearsal-testnet] validate template"
scripts/validate_arbitrum_sepolia_template.sh

echo "[rehearsal-testnet] start bot (dry-run + offline feed)"
BOT_PID=""
cleanup() {
  if [[ -n "${BOT_PID:-}" ]]; then
    kill "${BOT_PID}" >/dev/null 2>&1 || true
    wait "${BOT_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

BOT_BIN="${BOT_BIN:-/tmp/phoenix_bot_rehearsal_testnet}"
go build -o "$BOT_BIN" ./cmd/bot >/dev/null

PHOENIX_CONTROL_PLANE_ENABLED="${PHOENIX_CONTROL_PLANE_ENABLED:-0}" \
  "$BOT_BIN" -config "${CONFIG_PATH}" -dry-run -offline -offline-feed -manual-only -no-monitor >/dev/null 2>&1 &
BOT_PID=$!

echo "[rehearsal-testnet] wait for API"
deadline=$((SECONDS + 30))
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

echo "[rehearsal-testnet] read-only API smoke"
API_BASE="${API_BASE}" ADMIN_TOKEN="${ADMIN_TOKEN}" scripts/smoke_api_v1_readonly.sh

echo "[rehearsal-testnet] OK"
