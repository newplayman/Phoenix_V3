#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import pathlib
import time
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from solcx import compile_files, get_installed_solc_versions, install_solc, set_solc_version
from web3 import Web3


@dataclass
class Env:
    web3: Web3
    account: LocalAccount


ERC20_ABI = [
    {
        "inputs": [{"internalType": "address", "name": "owner", "type": "address"}],
        "name": "balanceOf",
        "outputs": [{"type": "uint256"}],
        "stateMutability": "view",
        "type": "function",
    },
    {
        "inputs": [{"internalType": "address", "name": "spender", "type": "address"}, {"internalType": "uint256", "name": "amount", "type": "uint256"}],
        "name": "approve",
        "outputs": [{"type": "bool"}],
        "stateMutability": "nonpayable",
        "type": "function",
    },
]

POOL_ABI = [
    {"inputs": [], "name": "token0", "outputs": [{"type": "address"}], "stateMutability": "view", "type": "function"},
    {"inputs": [], "name": "token1", "outputs": [{"type": "address"}], "stateMutability": "view", "type": "function"},
]

SWAPHELPER_ABI = [
    {
        "inputs": [
            {"internalType": "address", "name": "pool", "type": "address"},
            {"internalType": "address", "name": "tokenIn", "type": "address"},
            {"internalType": "address", "name": "tokenOut", "type": "address"},
            {"internalType": "uint256", "name": "amountIn", "type": "uint256"},
            {"internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160"},
        ],
        "name": "swapExactInputSingle",
        "outputs": [{"internalType": "uint256", "name": "amountOut", "type": "uint256"}],
        "stateMutability": "nonpayable",
        "type": "function",
    }
]


def require_confirm(var_name: str, expected: str):
    if os.getenv(var_name) != expected:
        raise SystemExit(f"missing {var_name}={expected}")


def load_key(key_hex: str, key_file: str) -> str:
    key_hex = (key_hex or "").strip()
    key_file = (key_file or "").strip()
    if not key_hex and key_file:
        try:
            key_hex = pathlib.Path(key_file).read_text(encoding="utf-8").strip()
        except FileNotFoundError as e:
            raise SystemExit(f"missing private key file: {key_file}") from e
        except OSError as e:
            raise SystemExit(f"failed reading key file: {key_file}") from e
    if not key_hex:
        raise SystemExit("missing private key (use --key/PRIVATE_KEY/BOT_PRIVATE_KEY or --key-file/BOT_PRIVATE_KEY_FILE)")
    return key_hex


def load_env(rpc_url: str, key_hex: str, key_file: str) -> Env:
    if not rpc_url:
        raise SystemExit("missing RPC URL (use --rpc or ARBITRUM_SEPOLIA_RPC_URL)")
    key_hex = load_key(key_hex, key_file)
    w3 = Web3(Web3.HTTPProvider(rpc_url))
    if not w3.is_connected():
        raise SystemExit("RPC connection failed")
    chain_id = int(w3.eth.chain_id)
    if chain_id != 421614:
        raise SystemExit(f"blocked: expected chainId=421614 (Arbitrum Sepolia), got {chain_id}")
    acct = Account.from_key(key_hex)
    return Env(w3, acct)


def ensure_solc():
    installed = [str(v) for v in get_installed_solc_versions()]
    if "0.8.20" not in installed:
        install_solc("0.8.20")
    set_solc_version("0.8.20")


def send_tx(env: Env, tx: dict, *, max_attempts: int = 3) -> str:
    w3 = env.web3
    last_err = None
    for attempt in range(max_attempts):
        pending = w3.eth.get_block("pending")
        base_fee = pending.get("baseFeePerGas") or w3.eth.gas_price
        tip = w3.to_wei(0.02, "gwei")
        max_fee = int(base_fee) * 2 + int(tip)

        tx["maxFeePerGas"] = int(max_fee)
        tx["maxPriorityFeePerGas"] = int(tip)
        signed = env.account.sign_transaction(tx)
        try:
            h = w3.eth.send_raw_transaction(signed.rawTransaction)
            receipt = w3.eth.wait_for_transaction_receipt(h)
            if receipt.status != 1:
                raise SystemExit(f"tx failed hash={h.hex()} status={receipt.status}")
            return h.hex()
        except ValueError as e:
            last_err = e
            msg = str(e).lower()
            if "underpriced" in msg or "fee cap too low" in msg or "max fee per gas less than block base fee" in msg:
                time.sleep(0.4 + 0.4 * attempt)
                continue
            raise
    raise SystemExit(f"tx failed after retries: {last_err}")


def deploy_swaphelper(env: Env) -> str:
    ensure_solc()
    root = pathlib.Path(__file__).resolve().parents[1]
    src = root / "contracts" / "SwapHelper.sol"
    if not src.exists():
        raise SystemExit(f"missing contracts/SwapHelper.sol at {src}")

    compiled = compile_files([str(src)], output_values=["abi", "bin"], solc_version="0.8.20")
    key = None
    for k in compiled.keys():
        if k.endswith("SwapHelper.sol:SwapHelper"):
            key = k
            break
    if key is None:
        raise SystemExit("failed to locate SwapHelper artifact in solcx output")
    abi = compiled[key]["abi"]
    bytecode = "0x" + compiled[key]["bin"]

    c = env.web3.eth.contract(abi=abi, bytecode=bytecode)
    nonce = env.web3.eth.get_transaction_count(env.account.address, "pending")
    tx = c.constructor().build_transaction({"from": env.account.address, "nonce": nonce})
    # Deployment transactions can fail with "intrinsic gas too low" on some RPCs if gas is underspecified.
    # Use a conservative fixed cap to avoid brittle estimation.
    tx["gas"] = 8_000_000
    h = send_tx(env, tx)
    receipt = env.web3.eth.get_transaction_receipt(h)
    addr = receipt.contractAddress
    if not addr:
        raise SystemExit(f"deploy missing contractAddress (hash={h})")
    print(f"status=sent action=deploy_swaphelper hash={h} contract={addr} explorer=https://sepolia.arbiscan.io/tx/{h}")
    return addr


def main() -> int:
    p = argparse.ArgumentParser(description="Deploy SwapHelper + approve + swap (Arbitrum Sepolia only)")
    p.add_argument("--rpc", default=os.getenv("ARBITRUM_SEPOLIA_RPC_URL") or "")
    p.add_argument("--key", default=os.getenv("BOT_PRIVATE_KEY") or "")
    p.add_argument("--key-file", default=os.getenv("BOT_PRIVATE_KEY_FILE") or "")
    p.add_argument("--swaphelper", default=os.getenv("SWAPHELPER_ADDRESS") or "", help="existing SwapHelper address (optional)")
    p.add_argument("--pool", required=True, help="UniswapV3 pool address")
    p.add_argument("--token-in", required=True, help="tokenIn address")
    p.add_argument("--token-out", required=True, help="tokenOut address")
    p.add_argument("--amount-in", required=True, help="amountIn raw (uint256, base units)")
    p.add_argument("--max-amount-in", default=os.getenv("SWAPHELPER_MAX_AMOUNT_IN") or "", help="safety cap for amountIn raw")
    p.add_argument("--out-json", default=os.getenv("SWAPHELPER_OUT_JSON") or "/tmp/phoenix_swaphelper_swap.json")
    args = p.parse_args()

    require_confirm("SWAPHELPER_SWAP_CONFIRM", "I_UNDERSTAND_TESTNET_GAS")
    env = load_env(args.rpc, args.key, args.key_file)

    pool = Web3.to_checksum_address(args.pool)
    token_in = Web3.to_checksum_address(args.token_in)
    token_out = Web3.to_checksum_address(args.token_out)
    amount_in = int(args.amount_in, 10)
    if amount_in <= 0:
        raise SystemExit("amount-in must be > 0")

    if args.max_amount_in:
        cap = int(args.max_amount_in, 10)
        if cap <= 0:
            raise SystemExit("max-amount-in must be > 0 if set")
        if amount_in > cap:
            raise SystemExit(f"refuse amount-in {amount_in} > cap {cap} (set SWAPHELPER_MAX_AMOUNT_IN to override)")

    # Validate pool token ordering.
    poolc = env.web3.eth.contract(address=pool, abi=POOL_ABI)
    t0 = Web3.to_checksum_address(poolc.functions.token0().call())
    t1 = Web3.to_checksum_address(poolc.functions.token1().call())
    if not ((token_in == t0 and token_out == t1) or (token_in == t1 and token_out == t0)):
        raise SystemExit(f"token mismatch: pool token0={t0} token1={t1} (got in={token_in} out={token_out})")

    swaphelper_addr = args.swaphelper.strip()
    if not swaphelper_addr:
        swaphelper_addr = deploy_swaphelper(env)
    swaphelper = Web3.to_checksum_address(swaphelper_addr)

    # Balance preflight
    erc20_in = env.web3.eth.contract(address=token_in, abi=ERC20_ABI)
    bal_in = int(erc20_in.functions.balanceOf(env.account.address).call())
    if bal_in < amount_in:
        raise SystemExit(f"insufficient token-in balance: have={bal_in} need={amount_in}")

    # Approve
    try:
        _ = erc20_in.functions.approve(swaphelper, amount_in).call({"from": env.account.address})
    except Exception as e:
        raise SystemExit(f"approve preflight reverted (no tx sent): {e}") from e
    nonce = env.web3.eth.get_transaction_count(env.account.address, "pending")
    tx = erc20_in.functions.approve(swaphelper, amount_in).build_transaction({"from": env.account.address, "nonce": nonce})
    try:
        est = int(env.web3.eth.estimate_gas(tx))
        tx["gas"] = min(max(int(est * 13 // 10) + 40_000, 120_000), 800_000)
    except Exception:
        tx["gas"] = 300_000
    h1 = send_tx(env, tx)
    print(f"status=sent action=approve token={token_in} spender={swaphelper} amount={amount_in} hash={h1} explorer=https://sepolia.arbiscan.io/tx/{h1}")

    # Swap
    sc = env.web3.eth.contract(address=swaphelper, abi=SWAPHELPER_ABI)
    try:
        _ = sc.functions.swapExactInputSingle(pool, token_in, token_out, amount_in, 0).call({"from": env.account.address})
    except Exception as e:
        raise SystemExit(f"swap preflight reverted (no tx sent): {e}") from e
    nonce2 = env.web3.eth.get_transaction_count(env.account.address, "pending")
    tx2 = sc.functions.swapExactInputSingle(pool, token_in, token_out, amount_in, 0).build_transaction({"from": env.account.address, "nonce": nonce2})
    try:
        est2 = int(env.web3.eth.estimate_gas(tx2))
        tx2["gas"] = min(max(int(est2 * 13 // 10) + 60_000, 200_000), 2_000_000)
    except Exception:
        tx2["gas"] = 900_000
    h2 = send_tx(env, tx2)
    print(f"status=sent action=swap pool={pool} token_in={token_in} token_out={token_out} amount_in={amount_in} hash={h2} explorer=https://sepolia.arbiscan.io/tx/{h2}")

    # Post balances
    erc20_out = env.web3.eth.contract(address=token_out, abi=ERC20_ABI)
    bal_in2 = int(erc20_in.functions.balanceOf(env.account.address).call())
    bal_out2 = int(erc20_out.functions.balanceOf(env.account.address).call())
    out = {
        "chain_id": int(env.web3.eth.chain_id),
        "wallet": env.account.address,
        "swaphelper": swaphelper,
        "pool": pool,
        "token_in": token_in,
        "token_out": token_out,
        "amount_in": str(amount_in),
        "tx_approve": h1,
        "tx_swap": h2,
        "balance_in_after": str(bal_in2),
        "balance_out_after": str(bal_out2),
    }
    pathlib.Path(args.out_json).write_text(json.dumps(out, indent=2), encoding="utf-8")
    print(f"status=ok out_json={args.out_json}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
