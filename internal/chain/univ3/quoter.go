package univ3

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Quoter provides read-only swap quotes from Uniswap V3 Quoter.
// This is used to set AmountOutMinimum for swap slippage protection.
// ABI: https://docs.uniswap.org/contracts/v3/reference/periphery/lens/Quoter

const quoterABIJSON = `[
	{"inputs":[{"internalType":"address","name":"tokenIn","type":"address"},{"internalType":"address","name":"tokenOut","type":"address"},{"internalType":"uint24","name":"fee","type":"uint24"},{"internalType":"uint256","name":"amountIn","type":"uint256"},{"internalType":"uint160","name":"sqrtPriceLimitX96","type":"uint160"}],"name":"quoteExactInputSingle","outputs":[{"internalType":"uint256","name":"amountOut","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}
]`

type Caller interface {
	Call(ctx context.Context, to common.Address, data []byte) ([]byte, error)
}

type Quoter struct {
	Address common.Address
	abi     abi.ABI
}

func NewQuoter(addr string) (*Quoter, error) {
	parsed, err := abi.JSON(strings.NewReader(quoterABIJSON))
	if err != nil {
		return nil, fmt.Errorf("parse quoter abi: %w", err)
	}
	return &Quoter{Address: common.HexToAddress(addr), abi: parsed}, nil
}

func (q *Quoter) QuoteExactInputSingle(ctx context.Context, caller Caller, tokenIn, tokenOut common.Address, fee uint32, amountIn *big.Int, sqrtPriceLimitX96 *big.Int) (*big.Int, error) {
	if caller == nil {
		return nil, fmt.Errorf("caller is nil")
	}
	if amountIn == nil || amountIn.Sign() <= 0 {
		return big.NewInt(0), nil
	}
	if sqrtPriceLimitX96 == nil {
		sqrtPriceLimitX96 = big.NewInt(0)
	}
	data, err := q.abi.Pack("quoteExactInputSingle", tokenIn, tokenOut, fee, amountIn, sqrtPriceLimitX96)
	if err != nil {
		return nil, fmt.Errorf("pack quote call: %w", err)
	}
	res, err := caller.Call(ctx, q.Address, data)
	if err != nil {
		return nil, fmt.Errorf("quoter call failed: %w", err)
	}
	outs, err := q.abi.Unpack("quoteExactInputSingle", res)
	if err != nil || len(outs) == 0 {
		return nil, fmt.Errorf("unpack quote failed: %w", err)
	}
	amountOut, ok := outs[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected quote type %T", outs[0])
	}
	return amountOut, nil
}
