#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require curl
require jq

# Optional: load RPC_URL from a local secrets file (never commit).
if [[ -n "${SECRETS_FILE:-}" ]]; then
  if [[ ! -f "${SECRETS_FILE}" ]]; then
    echo "SECRETS_FILE not found: ${SECRETS_FILE}" >&2
    exit 2
  fi
  set -a
  # shellcheck disable=SC1090
  source "${SECRETS_FILE}"
  set +a
fi

RPC_URL="${RPC_URL:-${ARBITRUM_SEPOLIA_RPC_URL:-}}"
if [[ -z "${RPC_URL}" ]]; then
  echo "missing RPC_URL (or ARBITRUM_SEPOLIA_RPC_URL)" >&2
  exit 2
fi

EXPECTED_CHAIN_ID="${EXPECTED_CHAIN_ID:-421614}"

jsonrpc() {
  local method="$1"
  local params="${2:-[]}"
  curl -sfS -X POST "$RPC_URL" -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

chain_hex="$(jsonrpc eth_chainId '[]' | jq -r '.result')"
if [[ ! "$chain_hex" =~ ^0x[0-9a-fA-F]+$ ]]; then
  echo "failed to read eth_chainId from RPC (got: $chain_hex)" >&2
  exit 2
fi
chain_dec="$((16#${chain_hex#0x}))"
if [[ "${chain_dec}" -ne "${EXPECTED_CHAIN_ID}" ]]; then
  echo "unexpected chain id: got=${chain_dec} (hex=${chain_hex}) want=${EXPECTED_CHAIN_ID}" >&2
  exit 2
fi

if [[ "$#" -lt 1 ]]; then
  echo "usage: RPC_URL=<rpc> $0 <0xaddr> [0xaddr...]" >&2
  exit 2
fi

jsonrpc_get_code() {
  local addr="$1"
  jsonrpc eth_getCode "[\"${addr}\",\"latest\"]" \
    | jq -r '.result'
}

failed=0
for addr in "$@"; do
  if [[ ! "${addr}" =~ ^0x[0-9a-fA-F]{40}$ ]]; then
    echo "invalid address: $addr" >&2
    failed=1
    continue
  fi
  code="$(jsonrpc_get_code "$addr")"
  if [[ "${code}" == "0x" || "${code}" == "0x0" || -z "${code}" ]]; then
    echo "$addr code=missing"
    failed=1
  else
    echo "$addr code=present bytes=$(( (${#code}-2)/2 ))"
  fi
done

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
