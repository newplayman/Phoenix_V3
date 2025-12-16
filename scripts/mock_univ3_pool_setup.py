#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import pathlib
import sys
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from solcx import compile_source, get_installed_solc_versions, install_solc, set_solc_version
from web3 import Web3


@dataclass
class Env:
    web3: Web3
    account: LocalAccount


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
        raise SystemExit("missing RPC URL (use --rpc or RPC_URL)")
    key_hex = load_key(key_hex, key_file)
    w3 = Web3(Web3.HTTPProvider(rpc_url))
    if not w3.is_connected():
        raise SystemExit("RPC connection failed")
    acct = Account.from_key(key_hex)
    return Env(w3, acct)


def require_confirm():
    if os.getenv("MOCKPOOL_CONFIRM") != "I_UNDERSTAND_TESTNET_GAS":
        raise SystemExit("missing MOCKPOOL_CONFIRM=I_UNDERSTAND_TESTNET_GAS")


def ensure_solc():
    installed = [str(v) for v in get_installed_solc_versions()]
    if "0.8.20" not in installed:
        install_solc("0.8.20")
    set_solc_version("0.8.20")


def compile_mock_pool() -> tuple[str, str]:
    sol_path = pathlib.Path(__file__).resolve().parents[1] / "contracts" / "MockUniV3Pool.sol"
    source = sol_path.read_text(encoding="utf-8")
    ensure_solc()
    compiled = compile_source(source, output_values=["abi", "bin"], solc_version="0.8.20")
    _, artifact = compiled.popitem()
    return json.dumps(artifact["abi"]), artifact["bin"]


def deploy(env: Env, sqrt_price_x96: int, tick: int, liquidity: int) -> str:
    require_confirm()
    abi_json, bytecode_hex = compile_mock_pool()
    abi = json.loads(abi_json)
    bytecode = "0x" + bytecode_hex

    contract = env.web3.eth.contract(abi=abi, bytecode=bytecode)
    nonce = env.web3.eth.get_transaction_count(env.account.address, "pending")

    pending = env.web3.eth.get_block("pending")
    base_fee = pending.get("baseFeePerGas") or env.web3.eth.gas_price
    tip = env.web3.to_wei(0.01, "gwei")
    max_fee = int(base_fee) * 2 + int(tip)

    tx = contract.constructor(int(sqrt_price_x96), int(tick), int(liquidity)).build_transaction(
        {
            "from": env.account.address,
            "nonce": nonce,
            "gas": 1_000_000,
            "maxFeePerGas": max_fee,
            "maxPriorityFeePerGas": tip,
        }
    )
    signed = env.account.sign_transaction(tx)

    tx_hash = None
    last_err: Exception | None = None
    for _ in range(3):
        try:
            tx_hash = env.web3.eth.send_raw_transaction(signed.rawTransaction)
            break
        except ValueError as e:
            last_err = e
            msg = str(e).lower()
            if "max fee per gas less than block base fee" in msg or "fee cap too low" in msg or "underpriced" in msg:
                pending = env.web3.eth.get_block("pending")
                base_fee = pending.get("baseFeePerGas") or env.web3.eth.gas_price
                tip = int(tip) + int(tip // 2)  # +50%
                max_fee = int(base_fee) * 2 + int(tip)
                tx["maxFeePerGas"] = max_fee
                tx["maxPriorityFeePerGas"] = tip
                signed = env.account.sign_transaction(tx)
                continue
            raise
    if tx_hash is None:
        raise SystemExit(f"deploy broadcast failed: {last_err}")

    receipt = env.web3.eth.wait_for_transaction_receipt(tx_hash)
    if receipt.status != 1:
        raise SystemExit(f"deploy failed tx={tx_hash.hex()} status={receipt.status}")

    addr = receipt.contractAddress
    print(addr)
    return addr


def read_state(w3: Web3, pool_addr: str):
    if not Web3.is_address(pool_addr):
        raise SystemExit(f"invalid pool address: {pool_addr}")
    abi_json, _ = compile_mock_pool()
    abi = json.loads(abi_json)
    c = w3.eth.contract(address=Web3.to_checksum_address(pool_addr), abi=abi)
    slot0 = c.functions.slot0().call()
    liq = c.functions.liquidity().call()
    # slot0 tuple: (sqrtPriceX96, tick, ..., unlocked)
    print(json.dumps({"pool": pool_addr, "sqrt_price_x96": int(slot0[0]), "tick": int(slot0[1]), "liquidity": int(liq)}))


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)

    d = sub.add_parser("deploy")
    d.add_argument("--rpc", default=os.getenv("RPC_URL") or os.getenv("ARBITRUM_SEPOLIA_RPC_URL") or "")
    d.add_argument("--key", default=os.getenv("PRIVATE_KEY") or os.getenv("BOT_PRIVATE_KEY") or "")
    d.add_argument("--key-file", default=os.getenv("BOT_PRIVATE_KEY_FILE") or "")
    d.add_argument("--sqrt-price-x96", type=int, default=2**96)
    d.add_argument("--tick", type=int, default=-195_000)
    d.add_argument("--liquidity", type=int, default=10**15)

    r = sub.add_parser("read")
    r.add_argument("--rpc", default=os.getenv("RPC_URL") or os.getenv("ARBITRUM_SEPOLIA_RPC_URL") or "")
    r.add_argument("--pool", required=True)

    args = p.parse_args(argv)
    if args.cmd == "deploy":
        env = load_env(args.rpc, args.key, args.key_file)
        deploy(env, args.sqrt_price_x96, args.tick, args.liquidity)
        return 0
    if args.cmd == "read":
        if not args.rpc:
            raise SystemExit("missing RPC URL (use --rpc or RPC_URL)")
        w3 = Web3(Web3.HTTPProvider(args.rpc))
        if not w3.is_connected():
            raise SystemExit("RPC connection failed")
        read_state(w3, args.pool)
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
