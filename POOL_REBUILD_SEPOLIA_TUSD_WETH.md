# Sepolia 重建 TUSD/WETH 测试池（推荐流程）

## 为什么“重建”而不是“修复旧池”

Uniswap V3 同一组 `(token0, token1, fee)` 只能有一个池子；如果旧池在 `createAndInitializePoolIfNecessary` 时用错了 `sqrtPriceX96`（未按 decimals 把人类价格换算为 rawPrice），后续无法通过同接口“重新初始化”，因此要想得到正确 tick/价格，只能：

- **部署一个新的 TestUSD（新 token 地址）**，再用相同 fee 新建一口“干净”的池。

## 0. 准备

- 只用全新的 testnet 钱包私钥（不要复用、不要发到聊天里）。
- 安装依赖：
  - `pip install -r scripts/requirements.txt`
- 准备环境变量（示例，推荐写入本机文件，避免出现在命令历史/聊天里）：
  - 一次性写入 `/root/.phoenix_secrets`（不会把私钥打印到屏幕）：
    - `umask 077 && printf 'export RPC_URL=%q\nexport PRIVATE_KEY=%q\n' "$RPC_URL" "$PRIVATE_KEY" > /root/.phoenix_secrets`
    - `PRIVATE_KEY` 需要是 `0x` + 64 位 hex（或不带 `0x` 的 64 位 hex）
  - 之后用 `SECRETS_FILE=/root/.phoenix_secrets` 让脚本自动 `source`：
    - `SECRETS_FILE=/root/.phoenix_secrets ./scripts/rebuild_sepolia_tusd_weth_pool.sh`
  - （可选）如果 `solc-bin.ethereum.org` 被 403，可强制 solcx 使用新域名：
    - `export SOLCX_LIST_URL="https://binaries.soliditylang.org/linux-amd64/list.json"`
    - `export SOLCX_DOWNLOAD_BASE="https://binaries.soliditylang.org/linux-amd64"`

一键脚本（可选）：

```bash
SECRETS_FILE=/root/.phoenix_secrets ./scripts/rebuild_sepolia_tusd_weth_pool.sh
```

脚本支持“断点复用”（避免重复部署/重复建池）：

```bash
SECRETS_FILE=/root/.phoenix_secrets \
  TUSD2=0x... POOL2=0x... \
  ./scripts/rebuild_sepolia_tusd_weth_pool.sh
```

## 1. 部署新的 TestUSD（得到新 token 地址）

```bash
python scripts/tusd_setup.py deploy-token --rpc "$RPC_URL" --key "$PRIVATE_KEY"
```

记下输出的 `TUSD` 合约地址（下面记为 `TUSD2`）。

## 2. 给钱包增发 TUSD2

```bash
python scripts/tusd_setup.py mint --rpc "$RPC_URL" --key "$PRIVATE_KEY" \
  --token "$TUSD2" --to "<YOUR_WALLET>" --amount 1000
```

## 3. 准备 WETH

确保钱包里有一定量的 WETH（例如 0.3）。如果只有 ETH，请用钱包/脚本调用 WETH 的 `deposit()` 先 wrap。

Sepolia WETH：`0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9`

## 4. 创建并初始化池（关键：--price 语义）

`--price` 固定表示 **WETH/TUSD** 的人类单位价格（脚本会自动处理 Uniswap 的 token0/token1 地址排序与 decimals）。所以：

- 如果你希望初始价格为 `ETH ≈ 3180 TUSD`  
- 则 `WETH/TUSD ≈ 1/3180 ≈ 0.000314`

```bash
python scripts/tusd_setup.py create-pool --rpc "$RPC_URL" --key "$PRIVATE_KEY" \
  --token "$TUSD2" --weth 0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9 \
  --fee 500 --price 0.000314
```

脚本会打印 `pool` 地址（记为 `POOL2`）。
如果你执行了 `add-liquidity`，脚本也会打印 `position_token_id=<id>`（UniV3 NFT tokenId），建议把它写进 `configs/config.yaml` 的 `position_token_id` 用于防止重复建仓/便于 cleanup。

## 5. 添加初始流动性（tick 区间）

fee=500 的 tick spacing=10，tick 需要对齐到 10 的倍数。  
不要手填 tick；先用脚本算：

```bash
python scripts/tusd_setup.py calc-ticks --rpc "$RPC_URL" --key "$PRIVATE_KEY" \
  --token "$TUSD2" --weth 0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9 \
  --fee 500 --stable-per-weth 3180 --width-pct 0.05

python scripts/tusd_setup.py add-liquidity --rpc "$RPC_URL" --key "$PRIVATE_KEY" \
  --token "$TUSD2" --weth 0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9 \
  --fee 500 --amount0 1000 --amount1 0.3 \
  --tick-lower <LOWER_TICK> --tick-upper <UPPER_TICK>
```

## 6. 更新 Phoenix 配置

把 `configs/config.yaml` 里的池替换为：

- `token0/token1` 必须按 Uniswap 规则排序：`token0 < token1`（按地址字节序），否则 mint 会 revert（Phoenix 的 `configcheck` 也会拒绝启动）。
- `address=$POOL2`
- `position_token_id=<上一步输出的 tokenId>`（强烈建议）
- `stable_tokens` 必须包含稳定侧（TUSD2 或 USDC 等），且不得包含 `cex_price_token`
- `cex_price_token` 指向 WETH（ETHUSDT feed 对应 token）

然后跑一次：

```bash
go run ./cmd/configcheck -config configs/config.yaml
```

## 7. 打开“波动率自适应宽度”（测试链）

在 `configs/config.yaml` 增加（或调整）：

- `strategy.range.vol_window`：波动率统计窗口（例如 `"6h"`）
- `strategy.range.vol_k`：宽度系数（宽度约等于 `vol_k * sigma_daily`）
- `strategy.range.min_width_pct/max_width_pct`：宽度上下界（测试链建议 `min_width_pct` 不要太小，避免窄区间反复出场导致 churn）

并确保池里不要再写死 `min_width_pct/max_width_pct`（否则会覆盖全局 range 设置）。
