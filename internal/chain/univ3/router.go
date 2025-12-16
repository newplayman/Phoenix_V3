package univ3

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/rebalancer"
)

type Router struct {
	RouterAddress common.Address
	ParsedABI     abi.ABI
}

func NewRouter(routerAddr string) *Router {
	parsed, err := abi.JSON(strings.NewReader(SwapRouterABI))
	if err != nil {
		panic(fmt.Sprintf("failed to parse SwapRouter ABI: %v", err))
	}
	return &Router{
		RouterAddress: common.HexToAddress(routerAddr),
		ParsedABI:     parsed,
	}
}

// BuildSwapData constructs calldata for exactInputSingle (single hop)
// or exactInput (multi-hop) depending on SwapAction.Path.
func (r *Router) BuildSwapData(action rebalancer.SwapAction, recipient string) ([]byte, error) {
	amountOutMin := action.MinAmountOut
	if amountOutMin == nil {
		amountOutMin = big.NewInt(0)
	}
	deadline := big.NewInt(time.Now().Add(5 * time.Minute).Unix())

	// Multi-hop path support.
	if len(action.Path) > 2 {
		fees := action.Fees
		if len(fees) == 0 {
			fees = make([]uint32, len(action.Path)-1)
			for i := range fees {
				fees[i] = action.Fee
			}
		}
		pathBytes, err := encodeV3Path(action.Path, fees)
		if err != nil {
			return nil, err
		}
		params := struct {
			Path             []byte
			Recipient        common.Address
			Deadline         *big.Int
			AmountIn         *big.Int
			AmountOutMinimum *big.Int
		}{
			Path:             pathBytes,
			Recipient:        common.HexToAddress(recipient),
			Deadline:         deadline,
			AmountIn:         action.AmountIn,
			AmountOutMinimum: amountOutMin,
		}
		data, err := r.ParsedABI.Pack("exactInput", params)
		if err != nil {
			return nil, fmt.Errorf("failed to pack multi-hop swap: %w", err)
		}
		return data, nil
	}

	// Single hop exactInputSingle.
	fee := big.NewInt(int64(action.Fee))

	zeroForOne := true
	bIn := action.FromToken.Bytes()
	bOut := action.ToToken.Bytes()
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
		limit.SetString("4295128740", 10)
	} else {
		limit.SetString("1461446703485210103287273052203988822378723970341", 10)
	}

	params := struct {
		TokenIn           common.Address
		TokenOut          common.Address
		Fee               *big.Int
		Recipient         common.Address
		Deadline          *big.Int
		AmountIn          *big.Int
		AmountOutMinimum  *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           action.FromToken,
		TokenOut:          action.ToToken,
		Fee:               fee,
		Recipient:         common.HexToAddress(recipient),
		Deadline:          deadline,
		AmountIn:          action.AmountIn,
		AmountOutMinimum:  amountOutMin,
		SqrtPriceLimitX96: limit,
	}

	data, err := r.ParsedABI.Pack("exactInputSingle", params)
	if err != nil {
		return nil, fmt.Errorf("failed to pack swap: %w", err)
	}
	return data, nil
}

// encodeV3Path builds Uniswap V3 path bytes: tokenIn(20) + fee(3) + tokenMid(20) + ... + tokenOut(20).
func encodeV3Path(tokens []common.Address, fees []uint32) ([]byte, error) {
	if len(tokens) < 2 {
		return nil, fmt.Errorf("path tokens must have at least 2 entries")
	}
	if len(fees) != len(tokens)-1 {
		return nil, fmt.Errorf("fees length must equal tokens-1")
	}
	buf := bytes.NewBuffer(nil)
	for i, t := range tokens {
		buf.Write(t.Bytes())
		if i < len(fees) {
			fee := fees[i]
			if fee > 0xffffff {
				return nil, fmt.Errorf("fee out of range: %d", fee)
			}
			buf.Write([]byte{byte(fee >> 16), byte(fee >> 8), byte(fee)})
		}
	}
	return buf.Bytes(), nil
}

// EstimateMinAmountOut is a local-only fallback for slippage protection when Quoter is unavailable.
// IMPORTANT: priceFrom/priceTo are assumed to be human prices (per 1 token), so this function must
// apply decimals conversion between raw token amounts and human units.
func (r *Router) EstimateMinAmountOut(amountIn *big.Int, fromDecimals, toDecimals int, priceFrom, priceTo float64, slippage float64) *big.Int {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return big.NewInt(0)
	}
	if priceFrom <= 0 || priceTo <= 0 {
		return big.NewInt(0)
	}
	if fromDecimals <= 0 {
		fromDecimals = 18
	}
	if toDecimals <= 0 {
		toDecimals = 18
	}
	if slippage < 0 {
		slippage = 0
	}
	if slippage > 0.5 {
		slippage = 0.5
	}

	// amountInHuman = amountInRaw / 10^fromDecimals
	amountInHuman, _ := new(big.Float).Quo(
		new(big.Float).SetInt(amountIn),
		new(big.Float).SetFloat64(math.Pow10(fromDecimals)),
	).Float64()
	valueUSD := amountInHuman * priceFrom
	amountOutHuman := valueUSD / priceTo
	minOutHuman := amountOutHuman * (1 - slippage)

	// minOutRaw = minOutHuman * 10^toDecimals
	minOutRaw := minOutHuman * math.Pow10(toDecimals)
	if minOutRaw <= 0 || math.IsNaN(minOutRaw) || math.IsInf(minOutRaw, 0) {
		return big.NewInt(0)
	}
	res := new(big.Int)
	new(big.Float).SetFloat64(minOutRaw).Int(res)
	if res.Sign() < 0 {
		return big.NewInt(0)
	}
	return res
}
