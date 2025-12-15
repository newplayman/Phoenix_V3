#!/bin/bash

echo "========================================="
echo "Phoenix V3 完整测试报告"
echo "测试时间: $(date)"
echo "========================================="
echo ""

# 1. 读取初始余额
echo "### 1. 初始状态（测试开始前）"
sqlite3 phoenix.db "SELECT * FROM balance_snapshots WHERE phase='INITIAL';" | while IFS='|' read id ts phase weth usdc eth notes; do
    echo "WETH: $weth wei ($(echo "scale=6; $weth / 1000000000000000000" | bc) WETH)"
    echo "USDC: $usdc ($(echo "scale=2; $usdc / 1000000" | bc) USDC)"
    echo "ETH:  $eth wei ($(echo "scale=6; $eth / 1000000000000000000" | bc) ETH)"
done
echo ""

# 2. 当前余额
echo "### 2. 当前状态（LP创建后）"
./check_real_wallet.sh | tail -4
echo ""

# 3. 执行Cleanup
echo "### 3. 执行LP撤出..."
if [[ -z "${BOT_PRIVATE_KEY:-}" ]]; then
  echo "Missing BOT_PRIVATE_KEY. Export a testnet key before running:" >&2
  echo "  export BOT_PRIVATE_KEY=0x<your_testnet_private_key>" >&2
  exit 2
fi
./bot -config configs/config_arbitrum.yaml -cleanup > logs/final_cleanup.log 2>&1 &
CLEANUP_PID=$!

echo "等待cleanup完成（最多2分钟）..."
for i in {1..24}; do
    sleep 5
    if ! ps -p $CLEANUP_PID > /dev/null; then
        echo "Cleanup已完成"
        break
    fi
    echo -n "."
done
echo ""

# 4. 最终余额
echo ""
echo "### 4. 最终状态（LP撤出后）"
sleep 5
./check_real_wallet.sh | tail -4

# 5. 记录最终余额到数据库
WALLET="0x4edc10cc33a324459470cce3a9cd7b0b879e228f"
WETH="0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
USDC="0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
RPC="https://arb1.arbitrum.io/rpc"

WETH_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
WETH_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$WETH\",\"data\":\"$WETH_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
USDC_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
USDC_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$USDC\",\"data\":\"$USDC_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
ETH_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBalance\",\"params\":[\"$WALLET\",\"latest\"],\"id\":1}" | jq -r '.result')

WETH_FINAL=$((16#${WETH_HEX:2}))
USDC_FINAL=$((16#${USDC_HEX:2}))
ETH_FINAL=$((16#${ETH_HEX:2}))

sqlite3 phoenix.db << SQL
INSERT INTO balance_snapshots (phase, weth_balance, usdc_balance, eth_balance, notes)
VALUES ('FINAL', '$WETH_FINAL', '$USDC_FINAL', '$ETH_FINAL', 'After cleanup - test complete');
SQL

# 6. 计算盈亏
echo ""
echo "========================================="
echo "### 5. 盈亏分析"
echo "========================================="

sqlite3 phoenix.db << 'SQL'
SELECT 
    'WETH变化: ' || 
    CAST((f.weth_balance - i.weth_balance) AS TEXT) || ' wei (' ||
    printf('%.6f', CAST(f.weth_balance - i.weth_balance AS REAL) / 1000000000000000000) || ' WETH)'
FROM 
    (SELECT weth_balance FROM balance_snapshots WHERE phase='INITIAL') i,
    (SELECT weth_balance FROM balance_snapshots WHERE phase='FINAL') f;

SELECT 
    'USDC变化: ' || 
    CAST((f.usdc_balance - i.usdc_balance) AS TEXT) || ' (' ||
    printf('%.2f', CAST(f.usdc_balance - i.usdc_balance AS REAL) / 1000000) || ' USDC)'
FROM 
    (SELECT usdc_balance FROM balance_snapshots WHERE phase='INITIAL') i,
    (SELECT usdc_balance FROM balance_snapshots WHERE phase='FINAL') f;

SELECT 
    'ETH变化 (Gas): ' || 
    CAST((f.eth_balance - i.eth_balance) AS TEXT) || ' wei (' ||
    printf('%.6f', CAST(f.eth_balance - i.eth_balance AS REAL) / 1000000000000000000) || ' ETH)'
FROM 
    (SELECT eth_balance FROM balance_snapshots WHERE phase='INITIAL') i,
    (SELECT eth_balance FROM balance_snapshots WHERE phase='FINAL') f;
SQL

echo ""
echo "### 6. 交易统计"
grep -c "Sent Tx Hash" logs/final_cleanup.log && echo "笔交易已执行" || echo "0笔交易"

echo ""
echo "========================================="
echo "测试完成"
echo "========================================="
