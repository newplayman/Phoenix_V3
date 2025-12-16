#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[tx-wait] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require date
require sleep
require go

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    fail "missing required env: $k"
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
  fail "usage: TX_HASH=0x... $0 (or pass hash as first arg)"
fi

timeout_s="${TXWAIT_TIMEOUT_S:-120}"
interval_s="${TXWAIT_INTERVAL_S:-3}"

BIN="${TXVERIFY_BIN:-/tmp/phoenix_txverify}"
go build -o "$BIN" ./cmd/txverify >/dev/null

start="$(date +%s)"
deadline="$((start + timeout_s))"

while :; do
  now="$(date +%s)"
  if (( now >= deadline )); then
    fail "timeout waiting for mined receipt hash=$hash (waited ${timeout_s}s)"
  fi

  out="$("$BIN" -chain-id 421614 -hash "$hash" 2>&1 || true)"
  echo "$out"

  if [[ "$out" == status=mined* ]]; then
    if [[ "$out" == *" tx_status=success "* ]]; then
      exit 0
    fi
    exit 3
  fi
  if [[ "$out" != status=pending* ]]; then
    fail "unexpected verifier output (expected status=pending or status=mined)"
  fi

  sleep "$interval_s"
done
