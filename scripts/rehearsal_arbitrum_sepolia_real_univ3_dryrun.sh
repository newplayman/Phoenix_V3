#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[real-univ3-dryrun] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require go
require curl
require jq
require rg
require ss

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    fail "missing required env: $k"
  fi
}

require_env ARBITRUM_SEPOLIA_RPC_URL
require_env POOL_ID
require_env POOL_ADDRESS
require_env POSITION_MANAGER_ADDRESS
require_env TOKEN0_ADDRESS
require_env TOKEN1_ADDRESS
require_env TOKEN0_DECIMALS
require_env TOKEN1_DECIMALS
require_env POOL_FEE
require_env STABLE_TOKEN_ADDRESS
require_env CEX_PRICE_TOKEN_ADDRESS

if [[ -z "${BOT_WALLET_ADDRESS:-}" && -z "${BOT_PRIVATE_KEY:-}" && -z "${BOT_PRIVATE_KEY_FILE:-}" ]]; then
  fail "set BOT_WALLET_ADDRESS (recommended) or BOT_PRIVATE_KEY(_FILE) for preview-time balance reads"
fi

echo "[real-univ3-dryrun] 1/4 preflight: contract code present on Arbitrum Sepolia"
RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" scripts/check_contract_code.sh \
  "$POOL_ADDRESS" "$POSITION_MANAGER_ADDRESS" "$TOKEN0_ADDRESS" "$TOKEN1_ADDRESS" >/dev/null

echo "[real-univ3-dryrun] 2/4 validate template"
CONFIG_PATH="configs/config_arbitrum_sepolia_real_univ3.template.yaml" \
  POOL_ID="$POOL_ID" \
  POOL_ADDRESS="$POOL_ADDRESS" \
  POSITION_MANAGER_ADDRESS="$POSITION_MANAGER_ADDRESS" \
  TOKEN0_ADDRESS="$TOKEN0_ADDRESS" \
  TOKEN1_ADDRESS="$TOKEN1_ADDRESS" \
  TOKEN0_DECIMALS="$TOKEN0_DECIMALS" \
  TOKEN1_DECIMALS="$TOKEN1_DECIMALS" \
  POOL_FEE="$POOL_FEE" \
  STABLE_TOKEN_ADDRESS="$STABLE_TOKEN_ADDRESS" \
  CEX_PRICE_TOKEN_ADDRESS="$CEX_PRICE_TOKEN_ADDRESS" \
  ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  scripts/validate_arbitrum_sepolia_template.sh >/dev/null

echo "[real-univ3-dryrun] 3/4 start bot (dry-run + live pool read) and run control-plane acceptance"
export PHOENIX_CONTROL_PLANE_ENABLED=1
export CONFIG_PATH="configs/config_arbitrum_sepolia_real_univ3.template.yaml"

# Ensure the acceptance script uses the same pool/chain and does not require a key if BOT_WALLET_ADDRESS is provided.
POOL_ID="$POOL_ID" \
  CHAIN_ID=421614 \
  BOT_FLAGS="-config $CONFIG_PATH -no-monitor -offline-feed -manual-only" \
  scripts/accept_control_plane_v1.sh

echo "[real-univ3-dryrun] 4/4 OK"

