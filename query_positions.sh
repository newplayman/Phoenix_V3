#!/bin/bash
PM="0xC36442b4a4522E871399CD717aBDD847Ab11FE88"
WALLET="0x4edc10cc33a324459470cce3a9cd7b0b879e228f"
RPC="https://arb1.arbitrum.io/rpc"

echo "=== 查询LP头寸 ==="

# balanceOf
BAL_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
COUNT_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$BAL_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
COUNT=$((16#${COUNT_HEX:2}))

echo "持有LP数量: $COUNT"
echo ""

for i in $(seq 0 $((COUNT-1))); do
    # tokenOfOwnerByIndex
    INDEX_HEX=$(printf "%064x" $i)
    TOI_DATA="0x2f745c59000000000000000000000000${WALLET:2}${INDEX_HEX}"
    
    TOKEN_ID_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$TOI_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
    TOKEN_ID=$((16#${TOKEN_ID_HEX:2}))
    
    echo "--- Position #$((i+1)) ---"
    echo "Token ID: $TOKEN_ID"
    
    # positions(tokenId)
    TID_HEX=$(printf "%064x" $TOKEN_ID)
    POS_DATA="0x99fbab88${TID_HEX}"
    
    POS_RESULT=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$PM\",\"data\":\"$POS_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
    
    # 解析结果 (简化版，只提取关键字段)
    # 跳过nonce(96位), operator(160位), token0(160位), token1(160位), fee(24位)
    # tickLower在第6个字段 (offset 5*32=160字节后)
    # tickUpper在第7个字段
    # liquidity在第8个字段
    
    TICK_LOWER_HEX=${POS_RESULT:330:64}
    TICK_UPPER_HEX=${POS_RESULT:394:64}
    LIQUIDITY_HEX=${POS_RESULT:458:64}
    
    # 转换为有符号整数 (简化处理)
    TICK_LOWER=$((16#$TICK_LOWER_HEX))
    TICK_UPPER=$((16#$TICK_UPPER_HEX))
    LIQUIDITY=$((16#$LIQUIDITY_HEX))
    
    # 处理负数tick
    if [ $TICK_LOWER -gt 2147483647 ]; then
        TICK_LOWER=$((TICK_LOWER - 4294967296))
    fi
    if [ $TICK_UPPER -gt 2147483647 ]; then
        TICK_UPPER=$((TICK_UPPER - 4294967296))
    fi
    
    echo "Tick Lower: $TICK_LOWER"
    echo "Tick Upper: $TICK_UPPER"
    echo "Liquidity: $LIQUIDITY"
    echo ""
    
    # 写入数据库
    sqlite3 phoenix.db << SQL
INSERT INTO position_tracking (token_id, liquidity, tick_lower, tick_upper, status)
VALUES ('$TOKEN_ID', '$LIQUIDITY', $TICK_LOWER, $TICK_UPPER, 'ACTIVE');
SQL
done

echo "头寸信息已记录到数据库"
