#!/usr/bin/env python3
"""
TUSD & Uniswap V3 helper脚本：
 1. 部署 contracts/TestUSD.sol
 2. 增发 TUSD 给任意地址
 3. 创建/初始化 TUSD-WETH 池
 4. 添加初始流动性

依赖：
  pip install -r scripts/requirements.txt

环境变量：
  RPC_URL          - Sepolia RPC 节点
  PRIVATE_KEY      - 执行者私钥（0x 前缀）

示例：
  python scripts/tusd_setup.py deploy-token --rpc $RPC_URL --key $PRIVATE_KEY
  python scripts/tusd_setup.py mint --token 0x... --to 0xWallet --amount 1000
  python scripts/tusd_setup.py create-pool --token 0x... --weth 0x7b79... --fee 3000 --price 1.0
  python scripts/tusd_setup.py add-liquidity --token 0x... --weth 0x7b79... --amount0 100 --amount1 0.05 --tick-lower -46080 --tick-upper -23040
"""
from __future__ import annotations

import argparse
import json
import math
import os
import pathlib
import sys
import time
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from solcx import (
    compile_source,
    get_installed_solc_versions,
    install_solc,
    set_solc_version,
)
from web3 import Web3
from web3.contract import Contract

POSITION_MANAGER = Web3.to_checksum_address("0x1238536071E1c677A632429e3655c799b22cDA52")
DEFAULT_WETH = Web3.to_checksum_address("0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9")

POS_MANAGER_ABI = json.loads(
    """
[
  {
    "inputs":[
      {"internalType":"address","name":"token0","type":"address"},
      {"internalType":"address","name":"token1","type":"address"},
      {"internalType":"uint24","name":"fee","type":"uint24"},
      {"internalType":"uint160","name":"sqrtPriceX96","type":"uint160"}
    ],
    "name":"createAndInitializePoolIfNecessary",
    "outputs":[{"internalType":"address","name":"pool","type":"address"}],
    "stateMutability":"payable",
    "type":"function"
  },
  {
    "inputs":[
      {
        "components":[
          {"internalType":"address","name":"token0","type":"address"},
          {"internalType":"address","name":"token1","type":"address"},
          {"internalType":"uint24","name":"fee","type":"uint24"},
          {"internalType":"int24","name":"tickLower","type":"int24"},
          {"internalType":"int24","name":"tickUpper","type":"int24"},
          {"internalType":"uint256","name":"amount0Desired","type":"uint256"},
          {"internalType":"uint256","name":"amount1Desired","type":"uint256"},
          {"internalType":"uint256","name":"amount0Min","type":"uint256"},
          {"internalType":"uint256","name":"amount1Min","type":"uint256"},
          {"internalType":"address","name":"recipient","type":"address"},
          {"internalType":"uint256","name":"deadline","type":"uint256"}
        ],
        "internalType":"struct INonfungiblePositionManager.MintParams",
        "name":"params",
        "type":"tuple"
      }
    ],
    "name":"mint",
    "outputs":[
      {"internalType":"uint256","name":"tokenId","type":"uint256"},
      {"internalType":"uint128","name":"liquidity","type":"uint128"},
      {"internalType":"uint256","name":"amount0","type":"uint256"},
      {"internalType":"uint256","name":"amount1","type":"uint256"}
    ],
    "stateMutability":"payable",
    "type":"function"
  }
]
"""
)

ERC20_ABI = json.loads(
    """
[
  {"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"value","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},
  {"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},
  {"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
  {"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}
]
"""
)


@dataclass
class Env:
    web3: Web3
    account: LocalAccount


def load_env(rpc_url: str, key_hex: str) -> Env:
    if not rpc_url:
        raise SystemExit("缺少 RPC URL，可通过 --rpc 或 RPC_URL 环境变量传入")
    if not key_hex:
        raise SystemExit("缺少私钥，可通过 --key 或 PRIVATE_KEY 环境变量传入")
    w3 = Web3(Web3.HTTPProvider(rpc_url))
    if not w3.is_connected():
        raise SystemExit("RPC 连接失败，请检查网络")
    acct = Account.from_key(key_hex)
    return Env(w3, acct)


def send_tx(env: Env, fn, value: int = 0, gas: int | None = None) -> str:
    nonce = env.web3.eth.get_transaction_count(env.account.address, "pending")
    gas_price = env.web3.eth.gas_price
    tx_params = {
        "from": env.account.address,
        "nonce": nonce,
        "gas": gas or 800_000,
        "gasPrice": gas_price,
        "value": value,
    }
    tx = fn.build_transaction(tx_params)
    signed = env.account.sign_transaction(tx)
    tx_hash = env.web3.eth.send_raw_transaction(signed.rawTransaction)
    print(f"→ 发送交易: {tx_hash.hex()}")
    receipt = env.web3.eth.wait_for_transaction_receipt(tx_hash)
    if receipt.status != 1:
        raise SystemExit(f"交易失败，hash={tx_hash.hex()} status={receipt.status}")
    print(f"✓ 交易成功，gasUsed={receipt.gasUsed}")
    return tx_hash.hex()


def compile_tusd() -> tuple[str, str]:
    sol_path = pathlib.Path(__file__).resolve().parents[1] / "contracts" / "TestUSD.sol"
    source = sol_path.read_text(encoding="utf-8")
    installed = [str(v) for v in get_installed_solc_versions()]
    if "0.8.20" not in installed:
        custom_solc = os.getenv("SOLCX_BINARY_PATH")
        if custom_solc:
            target = pathlib.Path.home() / ".solcx" / "solc-v0.8.20"
            target.parent.mkdir(parents=True, exist_ok=True)
            if not target.exists():
                pathlib.Path(custom_solc).replace(target)
            os.chmod(target, 0o755)
        else:
            install_solc("0.8.20")
    try:
        set_solc_version("0.8.20")
    except Exception:
        install_solc("0.8.20")
    compiled = compile_source(
        source,
        output_values=["abi", "bin"],
        solc_version="0.8.20",
    )
    _, artifact = compiled.popitem()
    return json.dumps(artifact["abi"]), artifact["bin"]


def handle_deploy(env: Env):
    abi, bytecode = compile_tusd()
    contract = env.web3.eth.contract(abi=json.loads(abi), bytecode=bytecode)
    tx = contract.constructor().build_transaction(
        {
            "from": env.account.address,
            "nonce": env.web3.eth.get_transaction_count(env.account.address),
            "gas": 1_200_000,
            "gasPrice": env.web3.eth.gas_price,
        }
    )
    signed = env.account.sign_transaction(tx)
    tx_hash = env.web3.eth.send_raw_transaction(signed.rawTransaction)
    print(f"→ 部署 TestUSD, tx={tx_hash.hex()}")
    receipt = env.web3.eth.wait_for_transaction_receipt(tx_hash)
    if receipt.status != 1:
        raise SystemExit("部署失败")
    print(f"✓ TestUSD 部署成功 Address={receipt.contractAddress}")


def handle_mint(env: Env, token: str, to: str, amount: float):
    token = Web3.to_checksum_address(token)
    to = Web3.to_checksum_address(to)
    contract = env.web3.eth.contract(address=token, abi=[
        {
            "inputs": [
                {"internalType": "address", "name": "to", "type": "address"},
                {"internalType": "uint256", "name": "amount", "type": "uint256"},
            ],
            "name": "mint",
            "outputs": [],
            "stateMutability": "nonpayable",
            "type": "function",
        }
    ])
    decimals = 6
    amount_wei = int(amount * (10 ** decimals))
    send_tx(env, contract.functions.mint(to, amount_wei))
    print(f"✓ 已增发 {amount} TUSD 给 {to}")


def compute_sqrt_price(price: float) -> int:
    if price <= 0:
        raise SystemExit("price 必须 > 0")
    sqrt_price = math.sqrt(price)
    return int(sqrt_price * (1 << 96))


def handle_create_pool(env: Env, token: str, weth: str, fee: int, price: float):
    token0, token1 = sorted(
        [Web3.to_checksum_address(token), Web3.to_checksum_address(weth)]
    )
    sqrt_price_x96 = compute_sqrt_price(price)
    pos = env.web3.eth.contract(address=POSITION_MANAGER, abi=POS_MANAGER_ABI)
    send_tx(
        env,
        pos.functions.createAndInitializePoolIfNecessary(
            token0, token1, fee, sqrt_price_x96
        ),
        value=0,
        gas=5_000_000,
    )
    pool_address = pos.functions.createAndInitializePoolIfNecessary(
        token0, token1, fee, sqrt_price_x96
    ).call()
    print(f"✓ 池已创建: {pool_address}")


def handle_add_liquidity(
    env: Env,
    token: str,
    weth: str,
    fee: int,
    amount0: float,
    amount1: float,
    tick_lower: int,
    tick_upper: int,
):
    token0, token1 = sorted(
        [Web3.to_checksum_address(token), Web3.to_checksum_address(weth)]
    )
    amount0_desired = int(amount0 * 10**6)
    amount1_desired = int(amount1 * 10**18)
    pos = env.web3.eth.contract(address=POSITION_MANAGER, abi=POS_MANAGER_ABI)

    # 需要预先 approve token0/token1 给 PositionManager
    erc20 = env.web3.eth.contract(address=token0, abi=ERC20_ABI)
    send_tx(env, erc20.functions.approve(POSITION_MANAGER, amount0_desired))
    weth_contract = env.web3.eth.contract(address=token1, abi=ERC20_ABI)
    send_tx(env, weth_contract.functions.approve(POSITION_MANAGER, amount1_desired))

    params = (
        token0,
        token1,
        fee,
        tick_lower,
        tick_upper,
        amount0_desired,
        amount1_desired,
        0,
        0,
        env.account.address,
        int(time.time()) + 600,
    )
    tx_hash = send_tx(env, pos.functions.mint(params), gas=2_000_000)
    print(f"✓ 添加流动性成功，tx={tx_hash}")


def parse_args():
    parser = argparse.ArgumentParser(description="TUSD & Uniswap V3 helper")
    sub = parser.add_subparsers(dest="command", required=True)

    deploy = sub.add_parser("deploy-token", help="部署 TestUSD 合约")
    deploy.add_argument("--rpc", default=os.getenv("RPC_URL"))
    deploy.add_argument("--key", default=os.getenv("PRIVATE_KEY"))

    mint = sub.add_parser("mint", help="为地址增发 TUSD")
    mint.add_argument("--rpc", default=os.getenv("RPC_URL"))
    mint.add_argument("--key", default=os.getenv("PRIVATE_KEY"))
    mint.add_argument("--token", required=True, help="TestUSD 合约地址")
    mint.add_argument("--to", required=True, help="接收地址")
    mint.add_argument("--amount", type=float, required=True, help="增发数量（单位 TUSD）")

    pool = sub.add_parser("create-pool", help="创建并初始化 TUSD/WETH 池")
    pool.add_argument("--rpc", default=os.getenv("RPC_URL"))
    pool.add_argument("--key", default=os.getenv("PRIVATE_KEY"))
    pool.add_argument("--token", required=True)
    pool.add_argument("--weth", default=DEFAULT_WETH)
    pool.add_argument("--fee", type=int, default=3000)
    pool.add_argument("--price", type=float, default=1.0, help="token1/token0 初始价格")

    liq = sub.add_parser("add-liquidity", help="为池添加初始流动性")
    liq.add_argument("--rpc", default=os.getenv("RPC_URL"))
    liq.add_argument("--key", default=os.getenv("PRIVATE_KEY"))
    liq.add_argument("--token", required=True)
    liq.add_argument("--weth", default=DEFAULT_WETH)
    liq.add_argument("--fee", type=int, default=3000)
    liq.add_argument("--amount0", type=float, required=True, help="token0 数量 (TUSD)")
    liq.add_argument("--amount1", type=float, required=True, help="token1 数量 (WETH)")
    liq.add_argument("--tick-lower", type=int, required=True)
    liq.add_argument("--tick-upper", type=int, required=True)

    return parser.parse_args()


def main():
    args = parse_args()
    env = load_env(getattr(args, "rpc"), getattr(args, "key"))
    if args.command == "deploy-token":
        handle_deploy(env)
    elif args.command == "mint":
        handle_mint(env, args.token, args.to, args.amount)
    elif args.command == "create-pool":
        handle_create_pool(env, args.token, args.weth, args.fee, args.price)
    elif args.command == "add-liquidity":
        handle_add_liquidity(
            env,
            args.token,
            args.weth,
            args.fee,
            args.amount0,
            args.amount1,
            args.tick_lower,
            args.tick_upper,
        )
    else:
        raise SystemExit("未知命令")


if __name__ == "__main__":
    main()
