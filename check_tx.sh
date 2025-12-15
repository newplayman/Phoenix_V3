#!/bin/bash
TX="0x9d9c6707e629e935536f800206152425e022e5a0792d4fcb2c92e8c9c68e117b"
RPC="https://arb1.arbitrum.io/rpc"

echo "查询Collect交易详情..."
curl -s -X POST $RPC \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getTransactionReceipt\",\"params\":[\"$TX\"],\"id\":1}" | jq '.result.logs[] | select(.topics[0] == "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef") | {address, topics, data}'
