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
  # --price 为 WETH/TUSD（人类单位），脚本会自动处理 token0/token1 地址排序与 decimals
  python scripts/tusd_setup.py create-pool --token 0x... --weth 0x7b79... --fee 3000 --price 0.000314
  python scripts/tusd_setup.py calc-ticks --token 0x... --weth 0x7b79... --fee 500 --stable-per-weth 3180 --width-pct 0.05
  python scripts/tusd_setup.py add-liquidity --token 0x... --weth 0x7b79... --amount0 1000 --amount1 0.3 --tick-lower <LOWER> --tick-upper <UPPER>
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
            import shutil

            target = pathlib.Path.home() / ".solcx" / "solc-v0.8.20"
            target.parent.mkdir(parents=True, exist_ok=True)
            if not target.exists():
                shutil.copy2(custom_solc, target)
            os.chmod(target, 0o755)
        else:
            # solcx defaults to solc-bin.ethereum.org, which can be blocked (HTTP 403) on some hosts.
            # Try default first, then fall back to binaries.soliditylang.org.
            try:
                install_solc("0.8.20")
            except Exception as e:
                try:
                    import solcx.install as inst  # type: ignore

                    # New canonical host for solc binaries and list.json.
                    alt_list = os.getenv(
                        "SOLCX_LIST_URL",
                        "https://binaries.soliditylang.org/linux-amd64/list.json",
                    )
                    alt_base = os.getenv(
                        "SOLCX_DOWNLOAD_BASE",
                        "https://binaries.soliditylang.org",
                    )

                    # py-solc-x 2.x hardcodes `BINARY_DOWNLOAD_BASE` as a *template*
                    # like "https://solc-bin.ethereum.org/{}-amd64/{}". If you set it
                    # to a plain directory URL it will keep requesting the wrong URL
                    # (e.g. missing /list.json). We normalize env values into a template.
                    base_prefix = alt_base.rstrip("/")
                    for suffix in ("/linux-amd64", "/macosx-amd64", "/windows-amd64"):
                        if base_prefix.endswith(suffix):
                            base_prefix = base_prefix[: -len(suffix)]
                            break
                    if base_prefix.endswith("/"):
                        base_prefix = base_prefix[:-1]

                    # If user only provided list.json URL, derive host prefix from it.
                    if base_prefix == "https://binaries.soliditylang.org" and "binaries.soliditylang.org" not in alt_base:
                        # keep base_prefix from alt_base; nothing to do
                        pass
                    if "list.json" in alt_list and "://" in alt_list:
                        # trim ".../<os>-amd64/list.json" => "https://...".
                        maybe = alt_list.rsplit("/", 2)[0]  # ".../<os>-amd64"
                        if maybe.endswith(("-amd64",)):
                            base_prefix_from_list = maybe.rsplit("/", 1)[0]
                            if base_prefix_from_list:
                                base_prefix = base_prefix_from_list

                    template = f"{base_prefix}/{{}}-amd64/{{}}"
                    if hasattr(inst, "BINARY_DOWNLOAD_BASE"):
                        inst.BINARY_DOWNLOAD_BASE = template

                    install_solc("0.8.20")
                except Exception:
                    raise SystemExit(
                        "solcx 无法下载 solc 0.8.20（可能被 403/网络策略拦截）。\n"
                        "可选解决方案：\n"
                        "  1) 设置环境变量改用 binaries.soliditylang.org：\n"
                        "     export SOLCX_LIST_URL=https://binaries.soliditylang.org/linux-amd64/list.json\n"
                        "     export SOLCX_DOWNLOAD_BASE=https://binaries.soliditylang.org/linux-amd64\n"
                        "  2) 或手动下载 solc 0.8.20 二进制并指定：\n"
                        "     export SOLCX_BINARY_PATH=/path/to/solc-v0.8.20\n"
                        f"原始错误：{e}\n"
                    )
    try:
        set_solc_version("0.8.20")
    except Exception:
        try:
            install_solc("0.8.20")
        except Exception:
            # Let the earlier error handler provide guidance; re-raise a generic instruction.
            raise SystemExit("solc 0.8.20 不可用；请先按上方提示安装/配置 solc。")
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


def get_token_decimals(env: Env, token: str) -> int:
    try:
        c = env.web3.eth.contract(address=Web3.to_checksum_address(token), abi=ERC20_ABI)
        d = c.functions.decimals().call()
        return int(d)
    except Exception:
        return 18


def compute_sqrt_price_x96(price_token1_per_token0: float, token0_decimals: int, token1_decimals: int) -> int:
    if price_token1_per_token0 <= 0:
        raise SystemExit("price 必须 > 0")
    if token0_decimals <= 0:
        token0_decimals = 18
    if token1_decimals <= 0:
        token1_decimals = 18

    # UniswapV3 `sqrtPriceX96` expects the RAW price:
    #   rawPrice = token1Raw / token0Raw
    # For a human price (token1/token0):
    #   rawPrice = (token1/token0) * 10^(dec1 - dec0)
    raw_price = price_token1_per_token0 * (10 ** (token1_decimals - token0_decimals))
    if raw_price <= 0:
        raise SystemExit("raw_price 计算结果非法（<=0），请检查 price 与 token decimals")

    sqrt_price = math.sqrt(raw_price)
    return int(sqrt_price * (1 << 96))


def handle_create_pool(env: Env, token: str, weth: str, fee: int, price: float):
    tusd = Web3.to_checksum_address(token)
    weth = Web3.to_checksum_address(weth)
    token0, token1 = sorted([tusd, weth])
    d0 = get_token_decimals(env, token0)
    d1 = get_token_decimals(env, token1)
    # CLI `price` is always WETH/TUSD (human). Convert to token1/token0 for the sorted order.
    # - If token0=TUSD, token1=WETH: token1/token0 == WETH/TUSD == price
    # - If token0=WETH, token1=TUSD: token1/token0 == TUSD/WETH == 1/price
    if price <= 0:
        raise SystemExit("price 必须 > 0")
    price_token1_per_token0 = price if (token0 == tusd and token1 == weth) else (1.0 / price)
    sqrt_price_x96 = compute_sqrt_price_x96(price_token1_per_token0, d0, d1)
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
    print(f"  token0={token0} (decimals={d0})")
    print(f"  token1={token1} (decimals={d1})")
    print(f"  cli price (WETH/TUSD)={price} (human)")
    print(f"  init price token1/token0={price_token1_per_token0} (human) -> sqrtPriceX96={sqrt_price_x96}")


def calc_tick_from_price_token0_per_token1(price_token0_per_token1: float, token0_decimals: int, token1_decimals: int) -> int:
    if price_token0_per_token1 <= 0:
        raise SystemExit("price_token0_per_token1 必须 > 0")
    raw_price = (1.0 / price_token0_per_token1) * (10 ** (token1_decimals - token0_decimals))
    tick = math.log(raw_price) / math.log(1.0001)
    return int(tick)


def align_ticks(tick_a: int, tick_b: int, spacing: int) -> tuple[int, int]:
    lo = min(tick_a, tick_b)
    hi = max(tick_a, tick_b)
    if spacing <= 0:
        spacing = 1
    lo_aligned = (lo // spacing) * spacing
    hi_aligned = ((hi + spacing - 1) // spacing) * spacing
    if lo_aligned == hi_aligned:
        hi_aligned += spacing
    return lo_aligned, hi_aligned


def tick_spacing_for_fee(fee: int) -> int:
    if fee == 100:
        return 1
    if fee == 500:
        return 10
    if fee == 3000:
        return 60
    if fee == 10000:
        return 200
    return 10


def handle_calc_ticks(env: Env, token: str, weth: str, fee: int, stable_per_weth: float, width_pct: float):
    tusd = Web3.to_checksum_address(token)
    weth = Web3.to_checksum_address(weth)
    token0, token1 = sorted([tusd, weth])
    d0 = get_token_decimals(env, token0)
    d1 = get_token_decimals(env, token1)

    if stable_per_weth <= 0:
        raise SystemExit("stable_per_weth 必须 > 0")
    if width_pct <= 0 or width_pct >= 0.5:
        raise SystemExit("width_pct 建议在 (0, 0.5) 范围内，例如 0.05 表示 ±5%")

    # stable per priced-token (TUSD per WETH) => convert to token0/token1 price.
    stable_is_token0 = token0 == tusd
    def to_token0_per_token1(stable_per_weth_price: float) -> float:
        return stable_per_weth_price if stable_is_token0 else (1.0 / stable_per_weth_price)

    p_low = stable_per_weth * (1.0 - width_pct)
    p_high = stable_per_weth * (1.0 + width_pct)
    t_low = calc_tick_from_price_token0_per_token1(to_token0_per_token1(p_low), d0, d1)
    t_high = calc_tick_from_price_token0_per_token1(to_token0_per_token1(p_high), d0, d1)
    spacing = tick_spacing_for_fee(fee)
    lo, hi = align_ticks(t_low, t_high, spacing)

    print("✓ Tick range calculated")
    print(f"  token0={token0} (decimals={d0})")
    print(f"  token1={token1} (decimals={d1})")
    print(f"  fee={fee} tickSpacing={spacing}")
    print(f"  stable_per_weth={stable_per_weth} widthPct=±{width_pct}")
    print(f"  tick_lower={lo}")
    print(f"  tick_upper={hi}")


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
    # CLI amounts are always: amount0=TUSD amount, amount1=WETH amount (human units).
    tusd = Web3.to_checksum_address(token)
    weth = Web3.to_checksum_address(weth)
    token0, token1 = sorted([tusd, weth])
    d0 = get_token_decimals(env, token0)
    d1 = get_token_decimals(env, token1)

    if amount0 <= 0 or amount1 <= 0:
        raise SystemExit("amount0/amount1 必须 > 0（amount0=TUSD, amount1=WETH）")
    tusd_raw = int(amount0 * (10 ** get_token_decimals(env, tusd)))
    weth_raw = int(amount1 * (10 ** get_token_decimals(env, weth)))

    if token0 == tusd:
        amount0_desired = tusd_raw
        amount1_desired = weth_raw
    else:
        amount0_desired = weth_raw
        amount1_desired = tusd_raw
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
    print(f"  token0={token0} (decimals={d0}) amount0Desired={amount0_desired}")
    print(f"  token1={token1} (decimals={d1}) amount1Desired={amount1_desired}")
    try:
        rcpt = env.web3.eth.get_transaction_receipt(tx_hash)
        transfer_sig = Web3.keccak(text="Transfer(address,address,uint256)").hex()
        for lg in rcpt["logs"]:
            if Web3.to_checksum_address(lg["address"]) != Web3.to_checksum_address(POSITION_MANAGER):
                continue
            topics = [t.hex() if hasattr(t, "hex") else Web3.to_hex(t) for t in lg["topics"]]
            if len(topics) < 4 or topics[0].lower() != transfer_sig.lower():
                continue
            frm = "0x" + topics[1][-40:]
            to = "0x" + topics[2][-40:]
            if int(frm, 16) != 0:
                continue
            if Web3.to_checksum_address(to) != Web3.to_checksum_address(env.account.address):
                continue
            token_id = int(topics[3], 16)
            print(f"  position_token_id={token_id}")
            break
    except Exception:
        pass


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
    pool.add_argument(
        "--price",
        type=float,
        default=0.000314,
        help="WETH/TUSD 初始价格（人类单位；脚本会自动处理 token0/token1 排序与 decimals）",
    )

    ticks = sub.add_parser("calc-ticks", help="根据目标价格与宽度计算 tick 区间（自动对齐 tick spacing）")
    ticks.add_argument("--rpc", default=os.getenv("RPC_URL"))
    ticks.add_argument("--key", default=os.getenv("PRIVATE_KEY"))
    ticks.add_argument("--token", required=True, help="TUSD 合约地址")
    ticks.add_argument("--weth", default=DEFAULT_WETH)
    ticks.add_argument("--fee", type=int, default=500)
    ticks.add_argument("--stable-per-weth", type=float, default=3180.0, help="稳定币计价的 ETH 价格（例如 3180 表示 1 WETH=3180 TUSD）")
    ticks.add_argument("--width-pct", type=float, default=0.05, help="区间宽度（例如 0.05 表示 ±5%）")

    liq = sub.add_parser("add-liquidity", help="为池添加初始流动性")
    liq.add_argument("--rpc", default=os.getenv("RPC_URL"))
    liq.add_argument("--key", default=os.getenv("PRIVATE_KEY"))
    liq.add_argument("--token", required=True)
    liq.add_argument("--weth", default=DEFAULT_WETH)
    liq.add_argument("--fee", type=int, default=3000)
    liq.add_argument("--amount0", type=float, required=True, help="TUSD 数量（人类单位）")
    liq.add_argument("--amount1", type=float, required=True, help="WETH 数量（人类单位）")
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
    elif args.command == "calc-ticks":
        handle_calc_ticks(env, args.token, args.weth, args.fee, args.stable_per_weth, args.width_pct)
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
