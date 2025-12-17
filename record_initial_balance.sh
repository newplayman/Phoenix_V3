#!/bin/bash
if [[ "${PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE:-}" != "1" ]]; then
  echo "blocked: this script targets Arbitrum One; set PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1 to run" >&2
  exit 2
fi
WALLET="0x4edc10cc33a324459470cce3a9cd7b0b879e228f"
WETH="0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
USDC="0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
RPC="https://arb1.arbitrum.io/rpc"

# 查询WETH
WETH_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
WETH_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$WETH\",\"data\":\"$WETH_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')

# 查询USDC
USDC_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
USDC_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$USDC\",\"data\":\"$USDC_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')

# 查询ETH
ETH_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBalance\",\"params\":[\"$WALLET\",\"latest\"],\"id\":1}" | jq -r '.result')

WETH_DEC=$((16#${WETH_HEX:2}))
USDC_DEC=$((16#${USDC_HEX:2}))
ETH_DEC=$((16#${ETH_HEX:2}))

echo "=== 初始余额记录 ==="
echo "时间: $(date)"
echo "WETH: $WETH_DEC wei"
echo "USDC: $USDC_DEC (最小单位)"
echo "ETH:  $ETH_DEC wei"
echo ""

# 写入数据库
sqlite3 phoenix.db << SQL
INSERT INTO balance_snapshots (phase, weth_balance, usdc_balance, eth_balance, notes)
VALUES ('INITIAL', '$WETH_DEC', '$USDC_DEC', '$ETH_DEC', 'Test start - before any operations');
SQL

echo "初始余额已记录到数据库"

# 显示人类可读格式
echo ""
echo "=== 人类可读格式 ==="
echo "WETH: $(echo "scale=6; $WETH_DEC / 1000000000000000000" | bc) WETH"
echo "USDC: $(echo "scale=2; $USDC_DEC / 1000000" | bc) USDC"
echo "ETH:  $(echo "scale=6; $ETH_DEC / 1000000000000000000" | bc) ETH"

# 计算总价值（假设ETH价格$3200）
WETH_USD=$(echo "scale=2; $WETH_DEC * 3200 / 1000000000000000000" | bc)
USDC_USD=$(echo "scale=2; $USDC_DEC / 1000000" | bc)
ETH_USD=$(echo "scale=2; $ETH_DEC * 3200 / 1000000000000000000" | bc)
TOTAL=$(echo "scale=2; $WETH_USD + $USDC_USD + $ETH_USD" | bc)

echo ""
echo "=== 估值（@ETH=$3200） ==="
echo "WETH价值: \$$WETH_USD"
echo "USDC价值: \$$USDC_USD"
echo "ETH价值:  \$$ETH_USD"
echo "总价值:   \$$TOTAL"
