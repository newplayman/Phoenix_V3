#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require bash

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "missing required env: $k" >&2
    exit 2
  fi
}

require_env ARBITRUM_SEPOLIA_RPC_URL

unlock_broadcast=0
if [[ "${TXPROBE_DRY_RUN:-}" == "false" && "${TXPROBE_KILL_SWITCH:-}" == "false" && "${TXPROBE_ALLOW_BROADCAST:-}" == "true" ]]; then
  unlock_broadcast=1
fi

if [[ "$unlock_broadcast" == "1" ]]; then
  if [[ "${TXPROBE_CONFIRM:-}" != "I_UNDERSTAND_GAS_COSTS" ]]; then
    echo "blocked: set TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS" >&2
    exit 2
  fi
  if [[ ! -t 0 ]]; then
    echo "blocked: no TTY available for hidden key prompt; run this target in an interactive terminal or set BOT_PRIVATE_KEY/BOT_PRIVATE_KEY_FILE via env/SECRETS_FILE" >&2
    exit 2
  fi
  if [[ -z "${BOT_PRIVATE_KEY:-}" ]]; then
    echo "BOT_PRIVATE_KEY not set; enter testnet private key (hidden input):" >&2
    read -r -s BOT_PRIVATE_KEY
    echo >&2
    export BOT_PRIVATE_KEY
  fi
fi

exec scripts/broadcast_probe_arbitrum_sepolia.sh "$@"
