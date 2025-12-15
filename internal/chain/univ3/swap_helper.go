package univ3

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/rebalancer"
)

// SwapHelper wraps the on-chain contracts/SwapHelper.sol helper that directly calls UniswapV3Pool.swap.
// It is intended for testnets where the official SwapRouter is missing/unreliable.
type SwapHelper struct {
	Address common.Address
	abi     abi.ABI
}

const swapHelperABI = `[
  {
    "inputs":[
      {"internalType":"address","name":"pool","type":"address"},
      {"internalType":"address","name":"tokenIn","type":"address"},
      {"internalType":"address","name":"tokenOut","type":"address"},
      {"internalType":"uint256","name":"amountIn","type":"uint256"},
      {"internalType":"uint160","name":"sqrtPriceLimitX96","type":"uint160"}
    ],
    "name":"swapExactInputSingle",
    "outputs":[{"internalType":"uint256","name":"amountOut","type":"uint256"}],
    "stateMutability":"nonpayable",
    "type":"function"
  }
]`

func NewSwapHelper(addr string) (*SwapHelper, error) {
	parsed, err := abi.JSON(strings.NewReader(swapHelperABI))
	if err != nil {
		return nil, fmt.Errorf("parse swaphelper abi: %w", err)
	}
	a := common.HexToAddress(addr)
	if a == (common.Address{}) {
		return nil, fmt.Errorf("swaphelper: invalid address %q", addr)
	}
	return &SwapHelper{Address: a, abi: parsed}, nil
}

// BuildSwapData constructs calldata for SwapHelper.swapExactInputSingle.
// Note: SwapHelper does not support multi-hop routes.
func (h *SwapHelper) BuildSwapData(pool common.Address, action rebalancer.SwapAction) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("swaphelper not initialized")
	}
	if pool == (common.Address{}) {
		return nil, fmt.Errorf("swaphelper: pool required")
	}
	if action.AmountIn == nil || action.AmountIn.Sign() <= 0 {
		return nil, fmt.Errorf("swaphelper: amountIn required")
	}
	if len(action.Path) > 2 {
		return nil, fmt.Errorf("swaphelper: multi-hop not supported (path_len=%d)", len(action.Path))
	}
	// sqrtPriceLimitX96=0 => contract uses full range.
	return h.abi.Pack(
		"swapExactInputSingle",
		pool,
		action.FromToken,
		action.ToToken,
		action.AmountIn,
		new(big.Int), // uint160(0)
	)
}

