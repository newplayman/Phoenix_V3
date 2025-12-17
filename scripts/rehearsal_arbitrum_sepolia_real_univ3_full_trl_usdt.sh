#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[real-univ3-full-trl-usdt] $*" >&2; exit 2; }

[[ -n "${ARBITRUM_SEPOLIA_RPC_URL:-}" ]] || fail "missing ARBITRUM_SEPOLIA_RPC_URL"
[[ -n "${BOT_PRIVATE_KEY_FILE:-}" ]] || fail "missing BOT_PRIVATE_KEY_FILE"
[[ -n "${BOT_WALLET_ADDRESS:-}" ]] || fail "missing BOT_WALLET_ADDRESS"

# Funding is a separate explicit confirm from minting.
[[ "${FUND_TRL_USDT_CONFIRM:-}" == "I_UNDERSTAND_TESTNET_GAS" ]] || fail "missing FUND_TRL_USDT_CONFIRM=I_UNDERSTAND_TESTNET_GAS"
[[ "${REAL_UNIV3_MINT_CONFIRM:-}" == "I_UNDERSTAND_GAS_COSTS" ]] || fail "missing REAL_UNIV3_MINT_CONFIRM=I_UNDERSTAND_GAS_COSTS"

# Default to the known-good pool discovered via cmd/univ3poolscan + cmd/univ3mintscan -trace.
export POOL_ID="${POOL_ID:-arbsepolia-real-univ3-trl-usdt}"
export POOL_ADDRESS="${POOL_ADDRESS:-0x53448a5c2c61da7A797f25cEd6D11BE838E674Fb}"
export POSITION_MANAGER_ADDRESS="${POSITION_MANAGER_ADDRESS:-0x6b2937Bde17889EDCf8fbD8dE31C3C2a70Bc4d65}"
export TOKEN0_ADDRESS="${TOKEN0_ADDRESS:-0x1b46aA4C362788E3b2557CE465487d9E41742Fd9}" # TRL (decimals=9)
export TOKEN1_ADDRESS="${TOKEN1_ADDRESS:-0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E}" # USDT (decimals=6)
export TOKEN0_DECIMALS="${TOKEN0_DECIMALS:-9}"
export TOKEN1_DECIMALS="${TOKEN1_DECIMALS:-6}"
export POOL_FEE="${POOL_FEE:-3000}"

# Price model: treat USDT as stable; treat TRL as CEX-priced token (offline feed).
export STABLE_TOKEN_ADDRESS="${STABLE_TOKEN_ADDRESS:-$TOKEN1_ADDRESS}"
export CEX_PRICE_TOKEN_ADDRESS="${CEX_PRICE_TOKEN_ADDRESS:-$TOKEN0_ADDRESS}"

echo "[real-univ3-full-trl-usdt] 1/2 fund TRL/USDT balances (testnet gas) ..."
FUND_TRL_USDT_CONFIRM="$FUND_TRL_USDT_CONFIRM" \
  ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  BOT_PRIVATE_KEY_FILE="$BOT_PRIVATE_KEY_FILE" \
  BOT_WALLET_ADDRESS="$BOT_WALLET_ADDRESS" \
  make -s rehearsal-testnet-fund-trl-usdt >/dev/null
echo "[real-univ3-full-trl-usdt] funded OK"

echo "[real-univ3-full-trl-usdt] 2/2 real UniV3 mint via Phoenix (approve+mint; testnet gas) ..."
REAL_UNIV3_MINT_CONFIRM="$REAL_UNIV3_MINT_CONFIRM" \
  ARBITRUM_SEPOLIA_RPC_URL="$ARBITRUM_SEPOLIA_RPC_URL" \
  BOT_PRIVATE_KEY_FILE="$BOT_PRIVATE_KEY_FILE" \
  BOT_WALLET_ADDRESS="$BOT_WALLET_ADDRESS" \
  POOL_ID="$POOL_ID" \
  POOL_ADDRESS="$POOL_ADDRESS" \
  POSITION_MANAGER_ADDRESS="$POSITION_MANAGER_ADDRESS" \
  TOKEN0_ADDRESS="$TOKEN0_ADDRESS" \
  TOKEN1_ADDRESS="$TOKEN1_ADDRESS" \
  TOKEN0_DECIMALS="$TOKEN0_DECIMALS" \
  TOKEN1_DECIMALS="$TOKEN1_DECIMALS" \
  POOL_FEE="$POOL_FEE" \
  STABLE_TOKEN_ADDRESS="$STABLE_TOKEN_ADDRESS" \
  CEX_PRICE_TOKEN_ADDRESS="$CEX_PRICE_TOKEN_ADDRESS" \
  make -s rehearsal-testnet-real-univ3-mint

echo "[real-univ3-full-trl-usdt] OK"
