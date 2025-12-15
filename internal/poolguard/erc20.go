package poolguard

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// ChainCaller is implemented by gateways that can perform eth_call.
type ChainCaller interface {
	Call(ctx context.Context, to common.Address, data []byte) ([]byte, error)
}

var erc20CheckABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(`[
		{"constant":true,"inputs":[],"name":"totalSupply","outputs":[{"name":"","type":"uint256"}],"type":"function"}
	]`))
	if err != nil {
		panic(fmt.Sprintf("poolguard: parse erc20 abi: %v", err))
	}
	erc20CheckABI = parsed
}

func packTotalSupply() ([]byte, error) {
	return erc20CheckABI.Pack("totalSupply")
}

func unpackTotalSupply(res []byte) (*big.Int, error) {
	outs, err := erc20CheckABI.Unpack("totalSupply", res)
	if err != nil || len(outs) == 0 {
		return nil, fmt.Errorf("unpack totalSupply: %w", err)
	}
	supply, ok := outs[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected totalSupply type %T", outs[0])
	}
	return supply, nil
}
