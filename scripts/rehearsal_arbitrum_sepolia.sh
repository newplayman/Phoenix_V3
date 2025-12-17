#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }
}

require go
require npm

CONFIG_PATH="${CONFIG_PATH:-configs/config_arbitrum_sepolia.template.yaml}"

if [[ -z "${ARBITRUM_SEPOLIA_RPC_URL:-}" ]]; then
  echo "ARBITRUM_SEPOLIA_RPC_URL is required" >&2
  exit 2
fi

echo "[rehearsal] make ci"
make ci

echo "[rehearsal] validate template"
scripts/validate_arbitrum_sepolia_template.sh

echo "[rehearsal] integration: chainId=421614"
go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID

if [[ -z "${ADMIN_TOKEN:-}" ]]; then
  echo "ADMIN_TOKEN is required for /api/v1/*" >&2
  exit 2
fi

if [[ -z "${BOT_PRIVATE_KEY:-}" && -z "${BOT_WALLET_ADDRESS:-}" ]]; then
  echo "Either BOT_PRIVATE_KEY or BOT_WALLET_ADDRESS is required (preview balance reads)" >&2
  exit 2
fi

echo "[rehearsal] accept control-plane v1 (dry-run) ..."
PHOENIX_CONTROL_PLANE_ENABLED=1 CONFIG_PATH="$CONFIG_PATH" POOL_ID="${POOL_ID:-}" CHAIN_ID=421614 scripts/accept_control_plane_v1.sh

echo "[rehearsal] OK"
