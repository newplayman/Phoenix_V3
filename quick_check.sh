#!/bin/bash
echo "正在查询钱包余额..."
echo ""

# 使用 cast 工具查询（如果有）或者直接用 curl
WALLET="0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
WETH="0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
USDC="0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
RPC="https://arb1.arbitrum.io/rpc"

# balanceOf(address) = 0x70a08231 + address (padded)
WETH_DATA="0x70a08231000000000000000000000000742d35Cc6634C0532925a3b844Bc454e4438f44e"
USDC_DATA="0x70a08231000000000000000000000000742d35Cc6634C0532925a3b844Bc454e4438f44e"

echo "WETH 余额查询..."
curl -s -X POST $RPC \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$WETH\",\"data\":\"$WETH_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result'

echo ""
echo "USDC 余额查询..."
curl -s -X POST $RPC \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$USDC\",\"data\":\"$USDC_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result'
