#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
import os
import pathlib
import sys
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from solcx import compile_files, get_installed_solc_versions, install_solc, set_solc_version
from web3 import Web3


@dataclass
class Env:
    web3: Web3
    account: LocalAccount


def require_confirm(var_name: str, expected: str):
    if os.getenv(var_name) != expected:
        raise SystemExit(f"missing {var_name}={expected}")


def ensure_solc():
    installed = [str(v) for v in get_installed_solc_versions()]
    if "0.8.20" not in installed:
        install_solc("0.8.20")
    set_solc_version("0.8.20")


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
    chain_id = int(w3.eth.chain_id)
    if chain_id != 421614:
        raise SystemExit(f"blocked: expected chainId=421614 (Arbitrum Sepolia), got {chain_id}")
    acct = Account.from_key(key_hex)
    return Env(w3, acct)


def compile_all() -> dict:
    ensure_solc()
    root = pathlib.Path(__file__).resolve().parents[1]
    files = [
        root / "contracts" / "TestUSD.sol",
        root / "contracts" / "TestWETH.sol",
        root / "contracts" / "MockUniV3Pool.sol",
        root / "contracts" / "MockNonfungiblePositionManager.sol",
    ]
    missing = [str(p) for p in files if not p.exists()]
    if missing:
        raise SystemExit(f"missing contract files: {missing}")
    compiled = compile_files([str(p) for p in files], output_values=["abi", "bin"], solc_version="0.8.20")
    return compiled


def artifact(compiled: dict, file_path: pathlib.Path, contract_name: str) -> tuple[list, str]:
    key = f"{file_path}:{contract_name}"
    if key not in compiled:
        # solcx uses absolute paths as keys
        for k in compiled.keys():
            if k.endswith(f"{file_path.name}:{contract_name}") or k.endswith(f"{file_path}:{contract_name}"):
                key = k
                break
    a = compiled[key]
    return a["abi"], "0x" + a["bin"]


def addr_to_int(a: str) -> int:
    return int(a.lower().replace("0x", ""), 16)


def send_tx(env: Env, tx: dict, *, max_attempts: int = 3) -> str:
    w3 = env.web3
    pending = w3.eth.get_block("pending")
    base_fee = pending.get("baseFeePerGas") or w3.eth.gas_price
    tip = w3.to_wei(0.01, "gwei")
    max_fee = int(base_fee) * 2 + int(tip)

    tx["maxFeePerGas"] = max_fee
    tx["maxPriorityFeePerGas"] = tip
    signed = env.account.sign_transaction(tx)

    last_err = None
    for _ in range(max_attempts):
        try:
            h = w3.eth.send_raw_transaction(signed.rawTransaction)
            receipt = w3.eth.wait_for_transaction_receipt(h)
            if receipt.status != 1:
                raise SystemExit(f"tx failed hash={h.hex()} status={receipt.status}")
            return h.hex()
        except ValueError as e:
            last_err = e
            msg = str(e).lower()
            if "max fee per gas less than block base fee" in msg or "fee cap too low" in msg or "underpriced" in msg:
                pending = w3.eth.get_block("pending")
                base_fee = pending.get("baseFeePerGas") or w3.eth.gas_price
                tip = int(tip) + int(tip // 2)
                max_fee = int(base_fee) * 2 + int(tip)
                tx["maxFeePerGas"] = max_fee
                tx["maxPriorityFeePerGas"] = tip
                signed = env.account.sign_transaction(tx)
                continue
            raise
    raise SystemExit(f"tx broadcast failed: {last_err}")


def deploy_contract(env: Env, abi: list, bytecode: str, args: list) -> tuple[str, str]:
    w3 = env.web3
    c = w3.eth.contract(abi=abi, bytecode=bytecode)
    nonce = w3.eth.get_transaction_count(env.account.address, "pending")
    ctor = c.constructor(*args)
    gas = 5_000_000
    try:
        est = int(ctor.estimate_gas({"from": env.account.address}))
        gas = max(gas, int(est * 13 // 10))
    except Exception:
        pass
    tx = ctor.build_transaction({"from": env.account.address, "nonce": nonce, "gas": gas})
    tx_hash = send_tx(env, tx)
    receipt = w3.eth.get_transaction_receipt(tx_hash)
    return receipt.contractAddress, tx_hash


def call_contract(env: Env, contract, fn_name: str, args: list) -> str:
    w3 = env.web3
    fn = getattr(contract.functions, fn_name)(*args)
    nonce = w3.eth.get_transaction_count(env.account.address, "pending")
    gas = 800_000
    try:
        est = int(fn.estimate_gas({"from": env.account.address}))
        gas = max(gas, int(est * 13 // 10))
    except Exception:
        pass
    tx = fn.build_transaction({"from": env.account.address, "nonce": nonce, "gas": gas})
    return send_tx(env, tx)


def tick_and_sqrt_price_x96(price_stable_per_priced: float, stable_is_token0: bool, token0_decimals: int, token1_decimals: int) -> tuple[int, int]:
    shift = token1_decimals - token0_decimals
    if stable_is_token0:
        raw_price = (10**shift) / price_stable_per_priced
    else:
        raw_price = price_stable_per_priced * (10**shift)

    tick = int(round(math.log(raw_price) / math.log(1.0001)))
    sqrt_price = math.sqrt(raw_price)
    sqrt_price_x96 = int(sqrt_price * (2**96))
    return tick, sqrt_price_x96


def deploy_stack(env: Env, compiled: dict, price_stable_per_weth: float) -> dict:
    root = pathlib.Path(__file__).resolve().parents[1] / "contracts"
    abi_usd, bin_usd = artifact(compiled, root / "TestUSD.sol", "TestUSD")
    abi_weth, bin_weth = artifact(compiled, root / "TestWETH.sol", "TestWETH")
    abi_pool, bin_pool = artifact(compiled, root / "MockUniV3Pool.sol", "MockUniV3Pool")
    abi_pm, bin_pm = artifact(compiled, root / "MockNonfungiblePositionManager.sol", "MockNonfungiblePositionManager")

    token_usd, tx_usd = deploy_contract(env, abi_usd, bin_usd, [])
    token_weth, tx_weth = deploy_contract(env, abi_weth, bin_weth, [])

    # Enforce token0<token1 ordering for Phoenix config.
    token0 = token_usd
    token1 = token_weth
    stable = token_usd
    priced = token_weth
    if addr_to_int(token0) > addr_to_int(token1):
        token0, token1 = token1, token0

    token0_decimals = 6 if Web3.to_checksum_address(token0) == Web3.to_checksum_address(token_usd) else 18
    token1_decimals = 6 if Web3.to_checksum_address(token1) == Web3.to_checksum_address(token_usd) else 18
    stable_is_token0 = Web3.to_checksum_address(stable) == Web3.to_checksum_address(token0)

    tick, sqrt_x96 = tick_and_sqrt_price_x96(price_stable_per_weth, stable_is_token0, token0_decimals, token1_decimals)

    pool, tx_pool = deploy_contract(env, abi_pool, bin_pool, [int(sqrt_x96), int(tick), int(10**15)])
    pm, tx_pm = deploy_contract(env, abi_pm, bin_pm, [])

    # Mint tokens to wallet for rehearsal.
    c_usd = env.web3.eth.contract(address=Web3.to_checksum_address(token_usd), abi=abi_usd)
    c_weth = env.web3.eth.contract(address=Web3.to_checksum_address(token_weth), abi=abi_weth)
    mint_usd = call_contract(env, c_usd, "mint", [env.account.address, int(100 * (10**6))])
    mint_weth = call_contract(env, c_weth, "mint", [env.account.address, int(5 * (10**16))])  # 0.05

    exports = {
        "POOL_ID": "arbsepolia-mock-lp",
        "POOL_ADDRESS": pool,
        "POSITION_MANAGER_ADDRESS": pm,
        "TOKEN0_ADDRESS": token0,
        "TOKEN1_ADDRESS": token1,
        "TOKEN0_DECIMALS": str(token0_decimals),
        "TOKEN1_DECIMALS": str(token1_decimals),
        "POOL_FEE": "3000",
        "STABLE_TOKEN_ADDRESS": stable,
        "CEX_PRICE_TOKEN_ADDRESS": priced,
    }

    return {
        "chain_id": int(env.web3.eth.chain_id),
        "deployer": env.account.address,
        "token_usd": token_usd,
        "token_weth": token_weth,
        "pool": pool,
        "position_manager": pm,
        "pool_tick": tick,
        "pool_sqrt_price_x96": str(sqrt_x96),
        "tx": {
            "deploy_token_usd": tx_usd,
            "deploy_token_weth": tx_weth,
            "deploy_pool": tx_pool,
            "deploy_position_manager": tx_pm,
            "mint_token_usd": mint_usd,
            "mint_token_weth": mint_weth,
        },
        "exports": exports,
    }


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)

    d = sub.add_parser("deploy")
    d.add_argument("--rpc", default=os.getenv("RPC_URL") or os.getenv("ARBITRUM_SEPOLIA_RPC_URL") or "")
    d.add_argument("--key", default=os.getenv("PRIVATE_KEY") or os.getenv("BOT_PRIVATE_KEY") or "")
    d.add_argument("--key-file", default=os.getenv("BOT_PRIVATE_KEY_FILE") or "")
    d.add_argument("--price", type=float, default=2000.0, help="target stable-per-priced token price used to seed mock pool tick")

    args = p.parse_args(argv)
    if args.cmd == "deploy":
        require_confirm("MOCKLP_CONFIRM", "I_UNDERSTAND_TESTNET_GAS")
        env = load_env(args.rpc, args.key, args.key_file)
        compiled = compile_all()
        out = deploy_stack(env, compiled, args.price)
        print(json.dumps(out))
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
