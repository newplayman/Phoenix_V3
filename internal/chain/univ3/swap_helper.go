package univ3

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const swapHelperABIJSON = `[
  {
    "inputs": [
      { "internalType": "address", "name": "pool", "type": "address" },
      { "internalType": "address", "name": "tokenIn", "type": "address" },
      { "internalType": "address", "name": "tokenOut", "type": "address" },
      { "internalType": "uint256", "name": "amountIn", "type": "uint256" },
      { "internalType": "uint256", "name": "amountOutMinimum", "type": "uint256" },
      { "internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160" }
    ],
    "name": "swapExactInputSingleMinOut",
    "outputs": [
      { "internalType": "uint256", "name": "amountOut", "type": "uint256" }
    ],
    "stateMutability": "nonpayable",
    "type": "function"
  }
]`

type SwapHelper struct {
	Address common.Address
	abi     abi.ABI
}

func NewSwapHelper(addr string) (*SwapHelper, error) {
	parsed, err := abi.JSON(strings.NewReader(swapHelperABIJSON))
	if err != nil {
		return nil, fmt.Errorf("parse swaphelper abi: %w", err)
	}
	return &SwapHelper{Address: common.HexToAddress(addr), abi: parsed}, nil
}

func (s *SwapHelper) BuildSwapExactInputSingleMinOutData(pool, tokenIn, tokenOut common.Address, amountIn, amountOutMinimum, sqrtPriceLimitX96 *big.Int) ([]byte, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("amountIn must be > 0")
	}
	if amountOutMinimum == nil || amountOutMinimum.Sign() < 0 {
		return nil, fmt.Errorf("amountOutMinimum must be >= 0")
	}
	if sqrtPriceLimitX96 == nil {
		sqrtPriceLimitX96 = big.NewInt(0)
	}
	return s.abi.Pack(
		"swapExactInputSingleMinOut",
		pool,
		tokenIn,
		tokenOut,
		amountIn,
		amountOutMinimum,
		sqrtPriceLimitX96,
	)
}

