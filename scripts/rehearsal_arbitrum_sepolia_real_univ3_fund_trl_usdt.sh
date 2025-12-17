#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[fund-trl-usdt] $*" >&2; exit 2; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"; }

require bash
require python3
require go
require jq

[[ -n "${ARBITRUM_SEPOLIA_RPC_URL:-}" ]] || fail "missing ARBITRUM_SEPOLIA_RPC_URL"
[[ -n "${BOT_PRIVATE_KEY_FILE:-}" ]] || fail "missing BOT_PRIVATE_KEY_FILE"
[[ -n "${BOT_WALLET_ADDRESS:-}" ]] || fail "missing BOT_WALLET_ADDRESS"
[[ "${FUND_TRL_USDT_CONFIRM:-}" == "I_UNDERSTAND_TESTNET_GAS" ]] || fail "missing FUND_TRL_USDT_CONFIRM=I_UNDERSTAND_TESTNET_GAS"

# Discovered pool on Arbitrum Sepolia (recorded in docs/runbook/real_univ3_e2e_testnet.md).
POOL_ADDRESS="${TRL_USDT_POOL_ADDRESS:-0x53448a5c2c61da7A797f25cEd6D11BE838E674Fb}"
TRL_ADDRESS="${TRL_TOKEN_ADDRESS:-0x1b46aA4C362788E3b2557CE465487d9E41742Fd9}"   # token0, decimals=9
USDT_ADDRESS="${USDT_TOKEN_ADDRESS:-0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E}" # token1, decimals=6

USDT_MINT_AMOUNT_RAW="${USDT_MINT_AMOUNT_RAW:-200000000}" # 200 * 1e6
USDT_SWAP_IN_RAW="${USDT_SWAP_IN_RAW:-50000000}"          # 50 * 1e6

VENV="${PHOENIX_VENV:-/tmp/phoenix_venv}"
PY="${VENV}/bin/python"
PIP="${VENV}/bin/pip"

if [[ ! -x "$PY" ]]; then
  python3 -m venv "$VENV"
  "$PIP" install -r scripts/requirements.txt >/dev/null
fi

echo "[fund-trl-usdt] 1/4 mint USDT (testnet gas) ..."
ERC20_MINT_CONFIRM=I_UNDERSTAND_TESTNET_GAS \
  ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  BOT_PRIVATE_KEY_FILE="$BOT_PRIVATE_KEY_FILE" \
  "$PY" scripts/erc20_mint_testnet.py \
  --rpc "$ARBITRUM_SEPOLIA_RPC_URL" \
  --key-file "$BOT_PRIVATE_KEY_FILE" \
  --token "$USDT_ADDRESS" \
  --to "$BOT_WALLET_ADDRESS" \
  --amount "$USDT_MINT_AMOUNT_RAW"

echo "[fund-trl-usdt] 2/4 swap USDT -> TRL via SwapHelper (deploy+approve+swap; testnet gas) ..."
SWAPHELPER_SWAP_CONFIRM=I_UNDERSTAND_TESTNET_GAS \
  SWAPHELPER_MAX_AMOUNT_IN="$USDT_SWAP_IN_RAW" \
  ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  BOT_PRIVATE_KEY_FILE="$BOT_PRIVATE_KEY_FILE" \
  "$PY" scripts/swaphelper_swap_arbitrum_sepolia.py \
  --rpc "$ARBITRUM_SEPOLIA_RPC_URL" \
  --key-file "$BOT_PRIVATE_KEY_FILE" \
  --pool "$POOL_ADDRESS" \
  --token-in "$USDT_ADDRESS" \
  --token-out "$TRL_ADDRESS" \
  --amount-in "$USDT_SWAP_IN_RAW"

echo "[fund-trl-usdt] 3/4 balances ..."
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$USDT_ADDRESS" -owner "$BOT_WALLET_ADDRESS"
ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" go run ./cmd/erc20balance -token "$TRL_ADDRESS" -owner "$BOT_WALLET_ADDRESS"

echo "[fund-trl-usdt] 4/4 OK"
