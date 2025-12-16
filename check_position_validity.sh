#!/bin/bash
if [[ "${PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE:-}" != "1" ]]; then
  echo "blocked: this script targets Arbitrum One; set PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1 to run" >&2
  exit 2
fi
echo "=== 检查LP头寸有效性 ==="
echo ""

# 1. 获取当前价格对应的Tick
PRICE=3200
# Tick = log(price) / log(1.0001)
# 对于WETH/USDC，需要考虑decimals: price_raw = 3200 * 10^(6-18) = 3.2e-9
# Tick(3.2e-9) ≈ -195600

echo "当前ETH价格: \$3200"
echo "对应Raw Price (考虑decimals): 3.2e-9"
echo "对应Tick: 约 -195600"
echo ""

# 2. 查询实际创建的头寸
PM="0xC36442b4a4522E871399CD717aBDD847Ab11FE88"
WALLET="0x4edc10cc33a324459470cce3a9cd7b0b879e228f"
RPC="https://arb1.arbitrum.io/rpc"

BAL_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
COUNT_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$BAL_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
COUNT=$((16#${COUNT_HEX:2}))

echo "持有LP数量: $COUNT"
echo ""

if [ $COUNT -eq 0 ]; then
    echo "没有LP头寸"
    exit 0
fi

# 查询第一个头寸
INDEX_HEX="0000000000000000000000000000000000000000000000000000000000000000"
TOI_DATA="0x2f745c59000000000000000000000000${WALLET:2}${INDEX_HEX}"

TOKEN_ID_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$TOI_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
TOKEN_ID=$((16#${TOKEN_ID_HEX:2}))

echo "Token ID: $TOKEN_ID"

# 查询positions详情
TID_HEX=$(printf "%064x" $TOKEN_ID)
POS_DATA="0x99fbab88${TID_HEX}"

POS_RESULT=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$POS_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')

# 解析 (positions返回12个字段)
# 跳过前5个字段(nonce, operator, token0, token1, fee)，每个32字节
# tickLower在offset 5*64+2 = 322
# tickUpper在offset 6*64+2 = 386  
# liquidity在offset 7*64+2 = 450

TICK_LOWER_HEX=${POS_RESULT:322:64}
TICK_UPPER_HEX=${POS_RESULT:386:64}
LIQUIDITY_HEX=${POS_RESULT:450:64}

# 转换为十进制（处理有符号整数）
TICK_LOWER_RAW=$((16#$TICK_LOWER_HEX))
TICK_UPPER_RAW=$((16#$TICK_UPPER_HEX))

# 处理负数（如果最高位是1）
if [ $TICK_LOWER_RAW -gt $((2**255)) ]; then
    TICK_LOWER=$((TICK_LOWER_RAW - 2**256))
else
    TICK_LOWER=$TICK_LOWER_RAW
fi

if [ $TICK_UPPER_RAW -gt $((2**255)) ]; then
    TICK_UPPER=$((TICK_UPPER_RAW - 2**256))
else
    TICK_UPPER=$TICK_UPPER_RAW
fi

LIQUIDITY=$((16#$LIQUIDITY_HEX))

echo "Tick Lower: $TICK_LOWER"
echo "Tick Upper: $TICK_UPPER"
echo "Liquidity: $LIQUIDITY"
echo ""

# 判断是否在范围内
CURRENT_TICK=-195600

echo "=== 有效性分析 ==="
if [ $CURRENT_TICK -ge $TICK_LOWER ] && [ $CURRENT_TICK -le $TICK_UPPER ]; then
    echo "✅ 头寸在有效范围内！"
    echo "   当前Tick ($CURRENT_TICK) 在 [$TICK_LOWER, $TICK_UPPER] 范围内"
else
    echo "❌ 头寸不在有效范围内！"
    echo "   当前Tick: $CURRENT_TICK"
    echo "   头寸范围: [$TICK_LOWER, $TICK_UPPER]"
    
    if [ $CURRENT_TICK -lt $TICK_LOWER ]; then
        echo "   问题: 当前价格低于范围下限"
        echo "   这意味着LP全部是Token1 (USDC)，无法赚取手续费"
    else
        echo "   问题: 当前价格高于范围上限"
        echo "   这意味着LP全部是Token0 (WETH)，无法赚取手续费"
    fi
fi
