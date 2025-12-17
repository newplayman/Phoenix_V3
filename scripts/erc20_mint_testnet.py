#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import pathlib
import sys
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from web3 import Web3


@dataclass
class Env:
    web3: Web3
    account: LocalAccount


ERC20_MINT_ABI = [
    {
        "inputs": [{"internalType": "address", "name": "to", "type": "address"}, {"internalType": "uint256", "name": "amount", "type": "uint256"}],
        "name": "mint",
        "outputs": [],
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


def send_tx(env: Env, tx: dict) -> str:
    w3 = env.web3
    pending = w3.eth.get_block("pending")
    base_fee = pending.get("baseFeePerGas") or w3.eth.gas_price
    tip = w3.to_wei(0.02, "gwei")
    max_fee = int(base_fee) * 2 + int(tip)

    tx["maxFeePerGas"] = int(max_fee)
    tx["maxPriorityFeePerGas"] = int(tip)

    signed = env.account.sign_transaction(tx)
    h = w3.eth.send_raw_transaction(signed.rawTransaction)
    receipt = w3.eth.wait_for_transaction_receipt(h)
    if receipt.status != 1:
        raise SystemExit(f"tx failed hash={h.hex()} status={receipt.status}")
    return h.hex()


def main() -> int:
    p = argparse.ArgumentParser(description="Testnet-only ERC20 mint helper (Arbitrum Sepolia)")
    p.add_argument("--rpc", default=os.getenv("ARBITRUM_SEPOLIA_RPC_URL") or "")
    p.add_argument("--key", default=os.getenv("BOT_PRIVATE_KEY") or "")
    p.add_argument("--key-file", default=os.getenv("BOT_PRIVATE_KEY_FILE") or "")
    p.add_argument("--token", required=True, help="ERC20 token address")
    p.add_argument("--to", required=True, help="recipient address")
    p.add_argument("--amount", required=True, help="raw amount (uint256, base units)")
    args = p.parse_args()

    require_confirm("ERC20_MINT_CONFIRM", "I_UNDERSTAND_TESTNET_GAS")
    env = load_env(args.rpc, args.key, args.key_file)

    token = Web3.to_checksum_address(args.token)
    to = Web3.to_checksum_address(args.to)
    amount = int(args.amount, 10)
    if amount <= 0:
        raise SystemExit("amount must be > 0")

    code = env.web3.eth.get_code(token)
    if not code or len(code) == 0:
        raise SystemExit(f"token has no code: {token}")

    c = env.web3.eth.contract(address=token, abi=ERC20_MINT_ABI)

    # Preflight: eth_call should not revert for permissionless mint.
    try:
        _ = c.functions.mint(to, amount).call({"from": env.account.address})
    except Exception as e:
        raise SystemExit(f"mint preflight reverted (no tx sent): {e}") from e

    nonce = env.web3.eth.get_transaction_count(env.account.address, "pending")
    tx = c.functions.mint(to, amount).build_transaction(
        {
            "from": env.account.address,
            "nonce": nonce,
        }
    )
    try:
        est = int(env.web3.eth.estimate_gas(tx))
        tx["gas"] = min(max(int(est * 13 // 10) + 30_000, 120_000), 1_500_000)
    except Exception:
        tx["gas"] = 400_000

    h = send_tx(env, tx)
    print(f"status=sent token={token} to={to} amount={amount} hash={h} explorer=https://sepolia.arbiscan.io/tx/{h}")
    return 0


if __name__ == "__main__":
	raise SystemExit(main())
