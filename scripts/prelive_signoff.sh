#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[prelive-signoff] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require make
require go
require rg

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    fail "missing required env: $k"
  fi
}

# Optional: load secrets from a local file (never commit).
# Example: SECRETS_FILE="$HOME/.config/phoenix/secrets.sh"
if [[ -n "${SECRETS_FILE:-}" ]]; then
  if [[ ! -f "${SECRETS_FILE}" ]]; then
    fail "SECRETS_FILE not found: ${SECRETS_FILE}"
  fi
  set -a
  # shellcheck disable=SC1090
  source "${SECRETS_FILE}"
  set +a
fi

require_env ARBITRUM_SEPOLIA_RPC_URL

echo "[prelive-signoff] 1/5 make ci"
make ci

echo "[prelive-signoff] 2/5 offline rehearsal"
make rehearsal-offline

echo "[prelive-signoff] 3/5 testnet dry-run rehearsal (read-only)"
make rehearsal-testnet-dryrun

echo "[prelive-signoff] 4/5 chainId integration test"
go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID

echo "[prelive-signoff] 5/5 broadcast-probe sanity (should be simulated unless unlocked)"
make broadcast-probe

unlock_broadcast=0
if [[ "${TXPROBE_DRY_RUN:-}" == "false" && "${TXPROBE_KILL_SWITCH:-}" == "false" && "${TXPROBE_ALLOW_BROADCAST:-}" == "true" ]]; then
  unlock_broadcast=1
fi

if [[ "$unlock_broadcast" != "1" ]]; then
  echo "[prelive-signoff] broadcast not unlocked; skipping real broadcast probe (set TXPROBE_* unlock + TXPROBE_CONFIRM to run)"
  exit 0
fi

if [[ "${TXPROBE_CONFIRM:-}" != "I_UNDERSTAND_GAS_COSTS" ]]; then
  fail "broadcast unlocked but missing TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS"
fi
if [[ -z "${BOT_PRIVATE_KEY:-}" && -z "${BOT_PRIVATE_KEY_FILE:-}" ]]; then
  fail "broadcast unlocked but missing BOT_PRIVATE_KEY (or BOT_PRIVATE_KEY_FILE); use SECRETS_FILE or set env"
fi

echo "[prelive-signoff] running REAL broadcast probe (gas-only, tx to self)"
probe_line="$(./scripts/broadcast_probe_arbitrum_sepolia.sh | tail -n 1)"
echo "$probe_line"

if [[ "$probe_line" != status=sent* ]]; then
  fail "expected status=sent output; got: $probe_line"
fi

hash="$(echo "$probe_line" | sed -n 's/.* hash=\([^ ]*\).*/\1/p')"
if [[ -n "$hash" ]]; then
  echo "[prelive-signoff] waiting for mined receipt (hash=$hash)"
  TX_HASH="$hash" ./scripts/wait_tx_mined_arbitrum_sepolia.sh >/dev/null
fi

PROBE_LINE="$probe_line" make signoff-record-probe
echo "[prelive-signoff] OK (recorded broadcast probe into docs/runbook/prelive_signoff.md)"
