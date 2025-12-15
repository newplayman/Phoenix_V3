package dexstate

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var poolABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(`[
		{
			"inputs": [],
			"name": "slot0",
			"outputs": [
				{"internalType":"uint160","name":"sqrtPriceX96","type":"uint160"},
				{"internalType":"int24","name":"tick","type":"int24"},
				{"internalType":"uint16","name":"observationIndex","type":"uint16"},
				{"internalType":"uint16","name":"observationCardinality","type":"uint16"},
				{"internalType":"uint16","name":"observationCardinalityNext","type":"uint16"},
				{"internalType":"uint8","name":"feeProtocol","type":"uint8"},
				{"internalType":"bool","name":"unlocked","type":"bool"}
			],
			"stateMutability": "view",
			"type": "function"
		},
		{
			"inputs": [],
			"name": "liquidity",
			"outputs": [{"internalType":"uint128","name":"","type":"uint128"}],
			"stateMutability": "view",
			"type": "function"
		}
	]`))
	if err != nil {
		panic(fmt.Sprintf("failed to parse uniswap v3 pool abi: %v", err))
	}
	poolABI = parsed
}

type UniV3State struct {
	client *ethclient.Client
}

func NewUniV3State(rpcURL string) (*UniV3State, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, err
	}
	return &UniV3State{client: client}, nil
}

func (u *UniV3State) GetPoolState(chainID int64, poolAddress common.Address) (*PoolState, error) {
	slot0Bytes, err := callView(context.Background(), u.client, poolAddress, "slot0")
	if err != nil {
		return nil, err
	}
	var slot0 struct {
		SqrtPriceX96               *big.Int
		Tick                       *big.Int
		ObservationIndex           uint16
		ObservationCardinality     uint16
		ObservationCardinalityNext uint16
		FeeProtocol                uint8
		Unlocked                   bool
	}
	if err := poolABI.UnpackIntoInterface(&slot0, "slot0", slot0Bytes); err != nil {
		return nil, fmt.Errorf("decode slot0 failed: %w", err)
	}

	liquidityBytes, err := callView(context.Background(), u.client, poolAddress, "liquidity")
	if err != nil {
		return nil, err
	}
	var liq struct {
		Value *big.Int
	}
	if err := poolABI.UnpackIntoInterface(&liq, "liquidity", liquidityBytes); err != nil {
		return nil, fmt.Errorf("decode liquidity failed: %w", err)
	}

	return &PoolState{
		ChainID:      chainID,
		PoolAddress:  poolAddress,
		CurrentTick:  slot0.Tick.Int64(),
		Liquidity:    liq.Value,
		SqrtPriceX96: slot0.SqrtPriceX96,
	}, nil
}

func callView(ctx context.Context, client *ethclient.Client, addr common.Address, method string) ([]byte, error) {
	data, err := poolABI.Pack(method)
	if err != nil {
		return nil, fmt.Errorf("pack %s failed: %w", method, err)
	}
	callMsg := ethereum.CallMsg{
		To:   &addr,
		Data: data,
	}
	result, err := client.CallContract(ctx, callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("rpc call %s failed: %w", method, err)
	}
	return result, nil
}
