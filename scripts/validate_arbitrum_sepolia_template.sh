#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }
}

require_cmd go

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "missing required env: $k" >&2
    exit 2
  fi
}

# Required by configs/config_arbitrum_sepolia.template.yaml
require_env ARBITRUM_SEPOLIA_RPC_URL
require_env POOL_ID
require_env POOL_ADDRESS
require_env POSITION_MANAGER_ADDRESS
require_env TOKEN0_ADDRESS
require_env TOKEN1_ADDRESS
require_env STABLE_TOKEN_ADDRESS
require_env CEX_PRICE_TOKEN_ADDRESS
require_env TOKEN0_DECIMALS
require_env TOKEN1_DECIMALS
require_env POOL_FEE

echo "[validate] config template expansion + schema checks"
go run ./cmd/configcheck -config configs/config_arbitrum_sepolia.template.yaml

if [[ "${VALIDATE_ONCHAIN_CODE:-}" == "1" ]]; then
  echo "[validate] on-chain code presence (requires real addresses)"
  RPC_URL="${ARBITRUM_SEPOLIA_RPC_URL}" EXPECTED_CHAIN_ID=421614 \
    ./scripts/check_contract_code.sh "${POOL_ADDRESS}" "${POSITION_MANAGER_ADDRESS}"
fi
