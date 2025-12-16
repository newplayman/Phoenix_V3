#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require go
require sed

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "missing required env: $k" >&2
    exit 2
  fi
}

# Optional: load secrets from a local file (never commit).
# Example: SECRETS_FILE="$HOME/.config/phoenix/secrets.sh"
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

# Testnet only.
require_env ARBITRUM_SEPOLIA_RPC_URL

# Explicit safety unlock (all must be set by operator).
unlock_broadcast=0
if [[ "${TXPROBE_DRY_RUN:-}" == "false" && "${TXPROBE_KILL_SWITCH:-}" == "false" && "${TXPROBE_ALLOW_BROADCAST:-}" == "true" ]]; then
  unlock_broadcast=1
fi
if [[ "$unlock_broadcast" == "1" ]]; then
  if [[ -z "${BOT_PRIVATE_KEY:-}" && -z "${BOT_PRIVATE_KEY_FILE:-}" ]]; then
    echo "missing required env: BOT_PRIVATE_KEY (or BOT_PRIVATE_KEY_FILE)" >&2
    exit 2
  fi
  if [[ "${TXPROBE_CONFIRM:-}" != "I_UNDERSTAND_GAS_COSTS" ]]; then
    echo "blocked: set TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS" >&2
    exit 2
  fi

  # Preflight: ensure the derived wallet address has some native ETH on Arbitrum Sepolia for gas.
  addr="$(BOT_PRIVATE_KEY="${BOT_PRIVATE_KEY:-}" BOT_PRIVATE_KEY_FILE="${BOT_PRIVATE_KEY_FILE:-}" go run ./cmd/walletaddr -quiet 2>/dev/null || true)"
  if [[ -n "$addr" ]]; then
    bal_line="$(ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/nativebalance -address "$addr" 2>&1 || true)"
    bal_wei="$(echo "$bal_line" | sed -n 's/.* balance_wei=\([^ ]*\).*/\1/p')"
    if [[ -z "$bal_wei" ]]; then
      echo "warn: could not preflight native balance for gas (address=$addr); proceeding to txprobe (details: $bal_line)" >&2
    elif [[ "$bal_wei" == "0" ]]; then
      echo "blocked: insufficient Arbitrum Sepolia ETH for gas (address=$addr balance_wei=0); bridge Sepolia ETH (L1) to Arbitrum Sepolia (L2) or use a faucet" >&2
      exit 2
    fi
  fi
fi

BOT_BIN="${BOT_BIN:-/tmp/phoenix_txprobe}"
go build -o "$BOT_BIN" ./cmd/txprobe >/dev/null

exec "$BOT_BIN" -chain-id 421614 "$@"
