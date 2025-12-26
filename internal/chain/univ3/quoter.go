package univ3

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Quoter provides read-only swap quotes from Uniswap V3 Quoter-style contracts.
// This is used to set AmountOutMinimum for swap slippage protection.
//
// Notes:
// - Some deployments expose `quoteExactInput(bytes,uint256)` but do NOT expose
//   `quoteExactInputSingle(...)`. This implementation supports both.

const quoterABIJSON = `[
  {
    "inputs": [
      { "internalType": "bytes", "name": "path", "type": "bytes" },
      { "internalType": "uint256", "name": "amountIn", "type": "uint256" }
    ],
    "name": "quoteExactInput",
    "outputs": [
      { "internalType": "uint256", "name": "amountOut", "type": "uint256" }
    ],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [
      {
        "components": [
          { "internalType": "address", "name": "tokenIn", "type": "address" },
          { "internalType": "address", "name": "tokenOut", "type": "address" },
          { "internalType": "uint24", "name": "fee", "type": "uint24" },
          { "internalType": "uint256", "name": "amountIn", "type": "uint256" },
          { "internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160" }
        ],
        "internalType": "struct IQuoterV2.QuoteExactInputSingleParams",
        "name": "params",
        "type": "tuple"
      }
    ],
    "name": "quoteExactInputSingle",
    "outputs": [
      { "internalType": "uint256", "name": "amountOut", "type": "uint256" },
      { "internalType": "uint160", "name": "sqrtPriceX96After", "type": "uint160" },
      { "internalType": "uint32", "name": "initializedTicksCrossed", "type": "uint32" },
      { "internalType": "uint256", "name": "gasEstimate", "type": "uint256" }
    ],
    "stateMutability": "nonpayable",
    "type": "function"
  }
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
	if sqrtPriceLimitX96 == nil || sqrtPriceLimitX96.Sign() == 0 {
		// Uniswap V3 pool.swap requires a non-zero sqrtPriceLimitX96 within bounds.
		// Use the same default boundary limits as Router does to express "no limit".
		zeroForOne := true
		bIn := tokenIn.Bytes()
		bOut := tokenOut.Bytes()
		for i := 0; i < 20; i++ {
			if bIn[i] < bOut[i] {
				zeroForOne = true
				break
			} else if bIn[i] > bOut[i] {
				zeroForOne = false
				break
			}
		}
		limit := new(big.Int)
		if zeroForOne {
			limit.SetString("4295128740", 10) // MIN_SQRT_RATIO + 1
		} else {
			limit.SetString("1461446703485210103287273052203988822378723970341", 10) // MAX_SQRT_RATIO - 1
		}
		sqrtPriceLimitX96 = limit
	}

	params := struct {
		TokenIn           common.Address
		TokenOut          common.Address
		Fee               *big.Int
		AmountIn          *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           tokenIn,
		TokenOut:          tokenOut,
		Fee:               big.NewInt(int64(fee)), // uint24
		AmountIn:          amountIn,
		SqrtPriceLimitX96: sqrtPriceLimitX96, // uint160
	}

	data, err := q.abi.Pack("quoteExactInputSingle", params)
	if err != nil {
		return nil, fmt.Errorf("pack quote call: %w", err)
	}
	res, err := caller.Call(ctx, q.Address, data)
	if err != nil {
		return nil, fmt.Errorf("quoter call failed: %w", err)
	}
	outs, err := q.abi.Unpack("quoteExactInputSingle", res)
	if err != nil || len(outs) < 1 {
		return nil, fmt.Errorf("unpack quote failed: %w", err)
	}
	amountOut, ok := outs[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected quote type %T", outs[0])
	}
	return amountOut, nil
}

func encodeV3Path(tokenIn common.Address, fee uint32, tokenOut common.Address) []byte {
	// Uniswap V3 path encoding: tokenIn (20 bytes) + fee (3 bytes) + tokenOut (20 bytes)
	b := make([]byte, 0, 20+3+20)
	b = append(b, tokenIn.Bytes()...)
	b = append(b, byte(fee>>16), byte(fee>>8), byte(fee))
	b = append(b, tokenOut.Bytes()...)
	return b
}

func (q *Quoter) QuoteExactInput(ctx context.Context, caller Caller, tokenIn, tokenOut common.Address, fee uint32, amountIn *big.Int) (*big.Int, error) {
	if caller == nil {
		return nil, fmt.Errorf("caller is nil")
	}
	if amountIn == nil || amountIn.Sign() <= 0 {
		return big.NewInt(0), nil
	}
	path := encodeV3Path(tokenIn, fee, tokenOut)
	data, err := q.abi.Pack("quoteExactInput", path, amountIn)
	if err != nil {
		return nil, fmt.Errorf("pack quoteExactInput call: %w", err)
	}
	res, err := caller.Call(ctx, q.Address, data)
	if err != nil {
		return nil, fmt.Errorf("quoter call failed: %w", err)
	}
	outs, err := q.abi.Unpack("quoteExactInput", res)
	if err != nil || len(outs) < 1 {
		return nil, fmt.Errorf("unpack quoteExactInput failed: %w", err)
	}
	amountOut, ok := outs[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected quote type %T", outs[0])
	}
	return amountOut, nil
}

// QuoteExactInputWithFallback tries quoteExactInputSingle first, then falls back to quoteExactInput(path).
// It returns amountOut, and the method name that succeeded ("quoteExactInputSingle" or "quoteExactInput").
func (q *Quoter) QuoteExactInputWithFallback(ctx context.Context, caller Caller, tokenIn, tokenOut common.Address, fee uint32, amountIn *big.Int) (*big.Int, string, error) {
	out, err := q.QuoteExactInputSingle(ctx, caller, tokenIn, tokenOut, fee, amountIn, big.NewInt(0))
	if err == nil && out != nil && out.Sign() > 0 {
		return out, "quoteExactInputSingle", nil
	}
	out, err = q.QuoteExactInput(ctx, caller, tokenIn, tokenOut, fee, amountIn)
	if err != nil {
		return nil, "", err
	}
	return out, "quoteExactInput", nil
}

// ComputeMinOutBps computes minOut = quoteOut * (1 - slippageBps/10000).
func ComputeMinOutBps(quoteOut *big.Int, slippageBps uint32) (*big.Int, error) {
	if quoteOut == nil || quoteOut.Sign() < 0 {
		return nil, fmt.Errorf("invalid quoteOut")
	}
	if slippageBps >= 10_000 {
		return nil, fmt.Errorf("slippageBps must be < 10000")
	}
	n := new(big.Int).Set(quoteOut)
	n.Mul(n, big.NewInt(int64(10_000-slippageBps)))
	n.Div(n, big.NewInt(10_000))
	return n, nil
}

// DefaultQuoterTimeout is used for on-demand quoting calls.
const DefaultQuoterTimeout = 12 * time.Second
