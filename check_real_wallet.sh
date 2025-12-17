#!/bin/bash
if [[ "${PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE:-}" != "1" ]]; then
  echo "blocked: this script targets Arbitrum One; set PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1 to run" >&2
  exit 2
fi
WALLET="0x4edc10cc33a324459470cce3a9cd7b0b879e228f"
WETH="0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
USDC="0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
RPC="https://arb1.arbitrum.io/rpc"

WETH_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"
USDC_DATA="0x70a082310000000000000000000000004edc10cc33a324459470cce3a9cd7b0b879e228f"

echo "查询钱包余额: $WALLET"
echo ""

WETH_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$WETH\",\"data\":\"$WETH_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')
USDC_HEX=$(curl -s -X POST $RPC -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$USDC\",\"data\":\"$USDC_DATA\"},\"latest\"],\"id\":1}" | jq -r '.result')

WETH_DEC=$((16#${WETH_HEX:2}))
USDC_DEC=$((16#${USDC_HEX:2}))

echo "WETH: $WETH_DEC wei ($(echo "scale=6; $WETH_DEC / 1000000000000000000" | bc) WETH)"
echo "USDC: $USDC_DEC ($(echo "scale=2; $USDC_DEC / 1000000" | bc) USDC)"
