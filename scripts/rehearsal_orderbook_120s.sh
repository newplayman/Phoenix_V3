#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[orderbook-120s] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require go

SYMBOL="${SYMBOL:-ETHUSDT}"
DURATION="${DURATION:-120s}"
OUT_PATH="${OUT_PATH:-/tmp/orderbook_raw_120s.jsonl}"

rm -f "$OUT_PATH"

echo "[orderbook-120s] runner: symbol=$SYMBOL duration=$DURATION out=$OUT_PATH"
go run ./cmd/orderbookrunner -symbol "$SYMBOL" -duration "$DURATION" -out "$OUT_PATH" -log-every 30s

echo "[orderbook-120s] replay:"
go run ./cmd/orderbookreplay -path "$OUT_PATH" -symbol "$SYMBOL"

echo "[orderbook-120s] OK"
