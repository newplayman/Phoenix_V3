#!/usr/bin/env bash
set -euo pipefail

# Rebuild a fresh Sepolia TUSD/WETH Uniswap V3 pool by deploying a new TestUSD (TUSD2),
# initializing the pool at a target price, and minting an initial LP position.
#
# Requirements:
#   pip install -r scripts/requirements.txt
#
# Environment:
#   RPC_URL        Sepolia RPC URL
#   PRIVATE_KEY    Testnet wallet private key (DO NOT COMMIT / DO NOT PASTE INTO CHAT)
#
# Optional:
#   FEE            V3 fee tier (default: 500)
#   STABLE_PER_WETH  e.g. 3180 (TUSD per 1 WETH) (default: 3180)
#   WIDTH_PCT      e.g. 0.05 for ±5% (default: 0.05)
#   MINT_TUSD      TUSD to mint to wallet (default: 1000)
#   AMOUNT_TUSD    TUSD to add as liquidity (default: 1000)
#   AMOUNT_WETH    WETH to add as liquidity (default: 0.3)
#   SECRETS_FILE   Optional file to source (e.g. /root/.phoenix_secrets) that exports RPC_URL/PRIVATE_KEY
#   TUSD2          Optional existing TestUSD address to reuse (skip deploy-token)
#   POOL2          Optional existing pool address to reuse (skip create-pool)
#   SOLCX_LIST_URL       Optional override for solc list.json URL
#   SOLCX_DOWNLOAD_BASE  Optional override for solc binary base URL

require_env() {
  local k="$1"
  if [[ -z "${!k:-}" ]]; then
    echo "Missing env: $k" >&2
    exit 2
  fi
}

SECRETS_FILE="${SECRETS_FILE:-}"
if [[ -n "$SECRETS_FILE" ]]; then
  # shellcheck source=/dev/null
  source "$SECRETS_FILE"
fi

require_env RPC_URL
require_env PRIVATE_KEY

if [[ ! "${PRIVATE_KEY}" =~ ^(0x)?[0-9A-Fa-f]{64}$ ]]; then
  echo "Invalid PRIVATE_KEY: expected 64 hex chars (optionally prefixed with 0x)" >&2
  exit 2
fi

FEE="${FEE:-500}"
STABLE_PER_WETH="${STABLE_PER_WETH:-3180}"
WIDTH_PCT="${WIDTH_PCT:-0.05}"
MINT_TUSD="${MINT_TUSD:-1000}"
AMOUNT_TUSD="${AMOUNT_TUSD:-1000}"
AMOUNT_WETH="${AMOUNT_WETH:-0.3}"
SKIP_LIQUIDITY="${SKIP_LIQUIDITY:-0}"

WETH="0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9"

# Export values used by embedded Python snippets.
export FEE STABLE_PER_WETH WIDTH_PCT MINT_TUSD AMOUNT_TUSD AMOUNT_WETH WETH

python3 -c 'import web3, solcx, eth_account' >/dev/null 2>&1 || {
  echo "Missing python deps. Run: pip install -r scripts/requirements.txt" >&2
  exit 2
}

WALLET="$(python3 -c 'from eth_account import Account; import os; print(Account.from_key(os.environ["PRIVATE_KEY"]).address)')"

echo "[rebuild] wallet=$WALLET"
echo "[rebuild] rpc=$RPC_URL fee=$FEE stable_per_weth=$STABLE_PER_WETH width_pct=$WIDTH_PCT"

WETH_PER_TUSD="$(python3 -c 'import os; stable=float(os.environ["STABLE_PER_WETH"]); print(1.0/stable)')"
echo "[rebuild] init price (WETH/TUSD)=$WETH_PER_TUSD"

TUSD2="${TUSD2:-}"
if [[ -z "$TUSD2" ]]; then
  echo "[rebuild] deploy TestUSD (TUSD2)..."
  export SOLCX_LIST_URL="${SOLCX_LIST_URL:-https://binaries.soliditylang.org/linux-amd64/list.json}"
  export SOLCX_DOWNLOAD_BASE="${SOLCX_DOWNLOAD_BASE:-https://binaries.soliditylang.org}"
  DEPLOY_OUT="$(python3 scripts/tusd_setup.py deploy-token --rpc "$RPC_URL")"
  echo "$DEPLOY_OUT"
  TUSD2="$(echo "$DEPLOY_OUT" | grep -Eo '0x[0-9A-Fa-f]{40}' | head -n1)"
  if [[ -z "$TUSD2" ]]; then
    echo "Failed to parse TUSD2 address from deploy output" >&2
    exit 1
  fi
  echo "[rebuild] TUSD2=$TUSD2"
fi
export TUSD2

echo "[rebuild] mint $MINT_TUSD TUSD2 to wallet..."
python3 scripts/tusd_setup.py mint --rpc "$RPC_URL" --token "$TUSD2" --to "$WALLET" --amount "$MINT_TUSD" >/dev/null

POOL2="${POOL2:-}"
if [[ -z "$POOL2" ]]; then
  echo "[rebuild] create pool (may return existing address if already created)..."
  POOL_OUT="$(python3 scripts/tusd_setup.py create-pool --rpc "$RPC_URL" --token "$TUSD2" --weth "$WETH" --fee "$FEE" --price "$WETH_PER_TUSD")"
  echo "$POOL_OUT"
  POOL2="$(echo "$POOL_OUT" | grep -Eo '0x[0-9A-Fa-f]{40}' | head -n1)"
  if [[ -z "$POOL2" ]]; then
    echo "Failed to parse POOL2 address from create-pool output" >&2
    exit 1
  fi
  echo "[rebuild] POOL2=$POOL2"
fi
export POOL2

echo "[rebuild] calc ticks..."
TICKS_OUT="$(python3 scripts/tusd_setup.py calc-ticks --rpc "$RPC_URL" --token "$TUSD2" --weth "$WETH" --fee "$FEE" --stable-per-weth "$STABLE_PER_WETH" --width-pct "$WIDTH_PCT")"
echo "$TICKS_OUT"
LOWER_TICK="$(echo "$TICKS_OUT" | grep -E '^  tick_lower=' | cut -d= -f2)"
UPPER_TICK="$(echo "$TICKS_OUT" | grep -E '^  tick_upper=' | cut -d= -f2)"
if [[ -z "$LOWER_TICK" || -z "$UPPER_TICK" ]]; then
  echo "Failed to parse ticks" >&2
  exit 1
fi
echo "[rebuild] tick_lower=$LOWER_TICK tick_upper=$UPPER_TICK"

if [[ "$SKIP_LIQUIDITY" == "1" ]]; then
  echo "[rebuild] SKIP_LIQUIDITY=1, skip initial mint"
else
  echo "[rebuild] preflight: check wallet balances for initial mint..."
  python3 - <<'PY'
import os
from web3 import Web3
from eth_account import Account

RPC_URL = os.environ["RPC_URL"]
PRIVATE_KEY = os.environ["PRIVATE_KEY"]
TUSD2 = os.environ["TUSD2"]
WETH = os.environ["WETH"]
AMOUNT_TUSD = float(os.environ["AMOUNT_TUSD"])
AMOUNT_WETH = float(os.environ["AMOUNT_WETH"])

w3 = Web3(Web3.HTTPProvider(RPC_URL))
wallet = Account.from_key(PRIVATE_KEY).address

ERC20_ABI = [
  {"constant": True, "inputs": [{"name": "owner", "type": "address"}], "name": "balanceOf", "outputs": [{"name": "", "type": "uint256"}], "type": "function"},
  {"constant": True, "inputs": [], "name": "decimals", "outputs": [{"name": "", "type": "uint8"}], "type": "function"},
]

def bal(token: str):
    c = w3.eth.contract(address=Web3.to_checksum_address(token), abi=ERC20_ABI)
    d = int(c.functions.decimals().call())
    b = int(c.functions.balanceOf(wallet).call())
    return b, d

tusd_bal, tusd_dec = bal(TUSD2)
weth_bal, weth_dec = bal(WETH)

need_tusd = int(AMOUNT_TUSD * (10 ** tusd_dec))
need_weth = int(AMOUNT_WETH * (10 ** weth_dec))

def fmt_amt(raw: int, dec: int) -> str:
    return f"{raw / (10 ** dec):.6f}"

print(f"[preflight] wallet={wallet}")
print(f"[preflight] TUSD2 balance={fmt_amt(tusd_bal, tusd_dec)} need={fmt_amt(need_tusd, tusd_dec)}")
print(f"[preflight] WETH  balance={fmt_amt(weth_bal, weth_dec)} need={fmt_amt(need_weth, weth_dec)}")

if tusd_bal < need_tusd:
    raise SystemExit("insufficient TUSD2 balance; reduce AMOUNT_TUSD or mint more")
if weth_bal < need_weth:
    raise SystemExit("insufficient WETH balance; reduce AMOUNT_WETH or deposit more WETH")
PY

  echo "[rebuild] add liquidity (amount0=TUSD, amount1=WETH)..."
  LIQ_OUT="$(python3 scripts/tusd_setup.py add-liquidity --rpc "$RPC_URL" \
    --token "$TUSD2" --weth "$WETH" --fee "$FEE" \
    --amount0 "$AMOUNT_TUSD" --amount1 "$AMOUNT_WETH" \
    --tick-lower "$LOWER_TICK" --tick-upper "$UPPER_TICK")"
  echo "$LIQ_OUT"
  POSITION_TOKEN_ID="$(echo "$LIQ_OUT" | grep -Eo 'position_token_id=[0-9]+' | head -n1 | cut -d= -f2)"
  if [[ -n "${POSITION_TOKEN_ID:-}" ]]; then
    echo "[rebuild] position_token_id=$POSITION_TOKEN_ID"
    export POSITION_TOKEN_ID
  fi
fi

export TUSD2 POOL2 FEE WETH

echo
echo "[rebuild] config snippet (paste into configs/config.yaml pools[0]):"
python3 - <<'PY'
import os
from web3 import Web3

tusd = Web3.to_checksum_address(os.environ["TUSD2"])
weth = Web3.to_checksum_address(os.environ["WETH"])
token0, token1 = sorted([tusd, weth])
decimals = {
    tusd: 6,   # TestUSD is 6
    weth: 18,  # WETH is 18
}

print(f"token0: \"{token0}\"")
print(f"token1: \"{token1}\"")
print(f"cex_price_token: \"{weth}\"")
print(f"token0_decimals: {decimals[token0]}")
print(f"token1_decimals: {decimals[token1]}")
print(f"fee: {os.environ['FEE']}")
print(f"address: \"{os.environ['POOL2']}\"")
if os.getenv("POSITION_TOKEN_ID"):
    print(f"position_token_id: \"{os.environ['POSITION_TOKEN_ID']}\"")
print("stable_tokens:")
print(f"  - \"{tusd}\"")
PY
