#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require go

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "missing required env: $k" >&2
    exit 2
  fi
}

require_env ARBITRUM_SEPOLIA_RPC_URL

hash="${TX_HASH:-}"
if [[ -z "$hash" ]]; then
  hash="${1:-}"
  if [[ -n "$hash" ]]; then
    shift
  fi
fi
if [[ -z "$hash" ]]; then
  echo "usage: TX_HASH=0x... $0 (or pass hash as first arg)" >&2
  exit 2
fi

BIN="${TXVERIFY_BIN:-/tmp/phoenix_txverify}"
go build -o "$BIN" ./cmd/txverify >/dev/null

exec "$BIN" -chain-id 421614 -hash "$hash" "$@"
