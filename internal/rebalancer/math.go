package rebalancer

import (
	"math"
	"math/big"
)

// Constants for Uniswap V3
var (
	Q96     = new(big.Int).Exp(big.NewInt(2), big.NewInt(96), nil)
	MinTick = int64(-887272)
	MaxTick = int64(887272)
)

// TickMath roughly adapted for quick estimation
func TickToSqrtPriceX96(tick int64) *big.Int {
	// sqrtPrice = sqrt(1.0001^tick)
	// sqrtPriceX96 = sqrtPrice * 2^96

	// 使用 float64 进行计算，然后转 big.Int，对于策略估算足够
	// 如果需要精确链上交互，建议使用精确的 TickMath 库，但在没有引入 SDK 的情况下，
	// 我们可以用高精度 float 实现。

	base := 1.0001
	p := math.Pow(base, float64(tick))
	sqrtP := math.Sqrt(p)

	// SqrtX96 calculation
	// We need 2^96 roughly 7.92e28
	// float64 max precision is ~15-17 digits, so direct mult might lose dust precision
	// but for "estimation" phase it's okay.

	f96, _ := new(big.Float).SetInt(Q96).Float64()
	res := sqrtP * f96

	bi := new(big.Int)
	new(big.Float).SetFloat64(res).Int(bi)
	return bi
}

// SqrtPriceX96ToTick converts sqrtPriceX96 back to tick
func SqrtPriceX96ToTick(sqrtPriceX96 *big.Int) int64 {
	if sqrtPriceX96 == nil || sqrtPriceX96.Sign() <= 0 {
		return 0
	}
	fPriceX96, _ := new(big.Float).SetInt(sqrtPriceX96).Float64()
	fQ96, _ := new(big.Float).SetInt(Q96).Float64()

	sqrtP := fPriceX96 / fQ96
	p := sqrtP * sqrtP

	// tick = log(p) / log(1.0001)
	return int64(math.Log(p) / math.Log(1.0001))
}

// GetLiquidityForAmounts calculates liquidity given current sqrtPrice and desired amounts
// Reference: Uniswap V3 LiquidityAmounts.sol
func GetLiquidityForAmounts(
	sqrtRatioX96,
	sqrtRatioAX96,
	sqrtRatioBX96 *big.Int,
	amount0,
	amount1 *big.Int,
) *big.Int {
	// Sort A and B
	if sqrtRatioAX96.Cmp(sqrtRatioBX96) > 0 {
		sqrtRatioAX96, sqrtRatioBX96 = sqrtRatioBX96, sqrtRatioAX96
	}

	if sqrtRatioX96.Cmp(sqrtRatioAX96) <= 0 {
		return GetLiquidityForAmount0(sqrtRatioAX96, sqrtRatioBX96, amount0)
	} else if sqrtRatioX96.Cmp(sqrtRatioBX96) < 0 {
		liq0 := GetLiquidityForAmount0(sqrtRatioX96, sqrtRatioBX96, amount0)
		liq1 := GetLiquidityForAmount1(sqrtRatioAX96, sqrtRatioX96, amount1)
		if liq0.Cmp(liq1) < 0 {
			return liq0
		}
		return liq1
	} else {
		return GetLiquidityForAmount1(sqrtRatioAX96, sqrtRatioBX96, amount1)
	}
}

func GetLiquidityForAmount0(sqrtRatioAX96, sqrtRatioBX96, amount0 *big.Int) *big.Int {
	// L = amount0 * (sqrtA * sqrtB) / (sqrtB - sqrtA)
	//   = amount0 * sqrtA * sqrtB / (sqrtB - sqrtA)
	// All in types...

	// numerator = amount0 * sqrtA * sqrtB
	num := new(big.Int).Mul(amount0, sqrtRatioAX96)
	num.Mul(num, sqrtRatioBX96)

	// denominator = (sqrtB - sqrtA) * Q96
	// Note: standard formula is L = amount0 / (1/sqrtA - 1/sqrtB) = amount0 * sqrtA * sqrtB / (sqrtB - sqrtA)
	// Since sqrtX96 has Q96 factor, let's trace units:
	// sqrtA_X96 = sqrtA * Q96
	// num = A0 * sqrtA * Q96 * sqrtB * Q96 = A0 * sqrtA * sqrtB * Q192
	// denom = (sqrtB_X96 - sqrtA_X96)
	//       = (sqrtB - sqrtA) * Q96
	// res = num / denom = A0 * sqrtA * sqrtB * Q96 / (sqrtB - sqrtA) -> This is Liquidity * Q96 ??
	// Wait, L should be pure number.
	// Standard: L = A0 * sqrtA * sqrtB / (sqrtB - sqrtA)
	// Input are X96.
	// We want to return L.
	// Formula with X96:
	// L = A0 * sqrtRatioAX96 * sqrtRatioBX96 / ((sqrtRatioBX96 - sqrtRatioAX96) * Q96) ?
	// Check:
	// Num units: Token * Q96 * Q96
	// Denom units: Q96 * ?
	// If we divide by Q96 at the end.

	// Let's use Full Math style logic to be safe or simple algebra.
	// A0 = L * (sqrtB - sqrtA) / (sqrtA * sqrtB)
	//    = L * (1/sqrtA - 1/sqrtB)
	// A0 = L * ( (sqrtBX96 - sqrtAX96)/Q96 ) / ( (sqrtAX96/Q96)*(sqrtBX96/Q96) )
	//    = L * ( (sqrtB - sqrtA)/Q96 ) / ( (sqrtA*sqrtB)/Q192 )
	//    = L * (sqrtB - sqrtA) * Q96 / (sqrtA*sqrtB)

	// So L = A0 * sqrtAX96 * sqrtBX96 / ( (sqrtBX96 - sqrtAX96) * Q96 )

	den := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)
	den.Mul(den, Q96) // The extra Q96 comes from the algebra above to cancel out

	// Result
	res := new(big.Int).Div(num, den)
	return res
}

func GetLiquidityForAmount1(sqrtRatioAX96, sqrtRatioBX96, amount1 *big.Int) *big.Int {
	// L = amount1 / (sqrtB - sqrtA)
	// A1 = L * (sqrtB - sqrtA)
	// A1 = L * ( (sqrtBX96 - sqrtAX96) / Q96 )
	// L = A1 * Q96 / (sqrtBX96 - sqrtAX96)

	num := new(big.Int).Mul(amount1, Q96)
	den := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)

	return new(big.Int).Div(num, den)
}

// GetAmountsForLiquidity calculates amount0 and amount1 for a given liquidity and range
func GetAmountsForLiquidity(
	sqrtRatioX96,
	sqrtRatioAX96,
	sqrtRatioBX96,
	liquidity *big.Int,
) (*big.Int, *big.Int) {
	if sqrtRatioAX96.Cmp(sqrtRatioBX96) > 0 {
		sqrtRatioAX96, sqrtRatioBX96 = sqrtRatioBX96, sqrtRatioAX96
	}

	var amount0, amount1 *big.Int
	amount0 = big.NewInt(0)
	amount1 = big.NewInt(0)

	if sqrtRatioX96.Cmp(sqrtRatioAX96) <= 0 {
		amount0 = GetAmount0ForLiquidity(sqrtRatioAX96, sqrtRatioBX96, liquidity)
	} else if sqrtRatioX96.Cmp(sqrtRatioBX96) < 0 {
		amount0 = GetAmount0ForLiquidity(sqrtRatioX96, sqrtRatioBX96, liquidity)
		amount1 = GetAmount1ForLiquidity(sqrtRatioAX96, sqrtRatioX96, liquidity)
	} else {
		amount1 = GetAmount1ForLiquidity(sqrtRatioAX96, sqrtRatioBX96, liquidity)
	}
	return amount0, amount1
}

func GetAmount0ForLiquidity(sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int) *big.Int {
	// A0 = L * (sqrtB - sqrtA) / (sqrtA * sqrtB)
	// X96 version: A0 = L * ((sqrtB - sqrtA)/Q96) / (sqrtA*sqrtB/Q192) ??? No
	// Let's go back to: L = A0 * sqrtA * sqrtB / ((sqrtB - sqrtA) * Q96)
	// So A0 = L * (sqrtB - sqrtA) * Q96 / (sqrtA * sqrtB)

	// Numerator: L * (sqrtB - sqrtA) * Q96
	diff := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)
	num := new(big.Int).Mul(liquidity, diff)
	num.Mul(num, Q96)

	// Denominator: sqrtA * sqrtB
	den := new(big.Int).Mul(sqrtRatioAX96, sqrtRatioBX96)

	return new(big.Int).Div(num, den)
}

func GetAmount1ForLiquidity(sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int) *big.Int {
	// A1 = L * (sqrtB - sqrtA)
	// A1 = L * (sqrtBX96 - sqrtAX96) / Q96

	diff := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)
	num := new(big.Int).Mul(liquidity, diff)
	return new(big.Int).Div(num, Q96)
}
