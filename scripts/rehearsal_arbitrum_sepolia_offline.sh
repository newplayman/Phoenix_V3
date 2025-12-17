#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }
}

require go
require npm
require curl
require jq
require rg
require ss

# This is an OFFLINE rehearsal: no RPC/pool calls, no tx broadcasts.
# It exists to validate that the control-plane v1 contract + storage + audit logging
# can run end-to-end under dry-run without requiring real chain funds.

export CONFIG_PATH="${CONFIG_PATH:-configs/config_arbitrum_sepolia.template.yaml}"
export CHAIN_ID="${CHAIN_ID:-421614}"
export POOL_ID="${POOL_ID:-offline-arb-sepolia-demo}"

# Template env (safe dummy addresses; only validated, never called when -offline).
export ARBITRUM_SEPOLIA_RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL:-http://127.0.0.1:8545}"
export POOL_ADDRESS="${POOL_ADDRESS:-0x0000000000000000000000000000000000000003}"
export POSITION_MANAGER_ADDRESS="${POSITION_MANAGER_ADDRESS:-0x0000000000000000000000000000000000000004}"
export TOKEN0_ADDRESS="${TOKEN0_ADDRESS:-0x0000000000000000000000000000000000000001}"
export TOKEN1_ADDRESS="${TOKEN1_ADDRESS:-0x0000000000000000000000000000000000000002}"
export STABLE_TOKEN_ADDRESS="${STABLE_TOKEN_ADDRESS:-0x0000000000000000000000000000000000000001}"
export CEX_PRICE_TOKEN_ADDRESS="${CEX_PRICE_TOKEN_ADDRESS:-0x0000000000000000000000000000000000000002}"
export TOKEN0_DECIMALS="${TOKEN0_DECIMALS:-6}"
export TOKEN1_DECIMALS="${TOKEN1_DECIMALS:-18}"
export POOL_FEE="${POOL_FEE:-500}"

export ADMIN_TOKEN="${ADMIN_TOKEN:-testtoken}"
# Non-zero address required to activate preview-time balance reader.
export BOT_WALLET_ADDRESS="${BOT_WALLET_ADDRESS:-0x1111111111111111111111111111111111111111}"

echo "[rehearsal-offline] make ci"
make ci

echo "[rehearsal-offline] validate template"
scripts/validate_arbitrum_sepolia_template.sh

echo "[rehearsal-offline] accept control-plane v1 (offline + fake balances)"
export PHOENIX_CONTROL_PLANE_ENABLED="${PHOENIX_CONTROL_PLANE_ENABLED:-1}"
export PHOENIX_PREVIEW_FAKE_BALANCES="${PHOENIX_PREVIEW_FAKE_BALANCES:-1}"
export BOT_FLAGS="${BOT_FLAGS:- -dry-run -config $CONFIG_PATH -no-monitor -offline -manual-only }"
# shellcheck disable=SC2086
scripts/accept_control_plane_v1.sh

echo "[rehearsal-offline] OK"
