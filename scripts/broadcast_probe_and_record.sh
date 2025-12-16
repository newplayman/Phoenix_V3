#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[probe-record] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require mktemp
require rg
require tail
require make

tmp="$(mktemp)"
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT

# Preserve TTY stdin for interactive key entry; only pipe stdout/stderr for capture.
set -o pipefail
set +e
./scripts/broadcast_probe_arbitrum_sepolia_interactive.sh "$@" 2>&1 | tee "$tmp" >/dev/null
rc=$?
set -e

sent_line="$(rg '^status=sent ' "$tmp" | tail -n 1 || true)"
if [[ -n "$sent_line" ]]; then
  hash="$(echo "$sent_line" | sed -n 's/.* hash=\([^ ]*\).*/\1/p')"
  if [[ -n "$hash" ]]; then
    echo "[probe-record] waiting for mined receipt (hash=$hash)"
    TX_HASH="$hash" ./scripts/wait_tx_mined_arbitrum_sepolia.sh >/dev/null
  fi
  PROBE_LINE="$sent_line" make signoff-record-probe
  echo "[probe-record] recorded: $sent_line"
  exit 0
fi

sim_line="$(rg '^status=simulated ' "$tmp" | tail -n 1 || true)"
if [[ -n "$sim_line" ]]; then
  echo "[probe-record] not recorded (simulated): $sim_line"
  exit 0
fi

if [[ "${rc:-0}" -ne 0 ]]; then
  echo "[probe-record] probe failed (exit=$rc); last output:" >&2
  tail -n 80 "$tmp" >&2 || true
  exit "$rc"
fi

fail "no status line found in probe output"
