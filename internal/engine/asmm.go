package engine

import (
	"errors"
	"math"
)

// StandardASMMEngine implements a basic volatility-based market making strategy
type StandardASMMEngine struct{}

func NewStandardASMMEngine() *StandardASMMEngine {
	return &StandardASMMEngine{}
}

func (e *StandardASMMEngine) Calculate(input EngineInput) (*EngineOutput, error) {
	if input.CexPrice <= 0 || input.DexPrice <= 0 {
		return nil, errors.New("invalid prices")
	}

	// 1. Determine the center price for LP range.
	//
	// IMPORTANT: The LP position MUST straddle the current on-chain price, otherwise
	// it becomes immediately out-of-range ("dead position") and causes repeated
	// rebalance churn (close+mint) and gas burn.
	//
	// We therefore center the range on DEX price. CEX price is still used elsewhere
	// (e.g. swap/arb decisions and USD notional estimation), but should not pull
	// the LP range away from current DEX.
	centerPrice := input.DexPrice

	// 2. Calculate Half-Spread based on Volatility and RiskFactor
	// Formula: spread = volatility * risk_factor * scalar
	// Example: 2% volatility * 1.5 risk * 2 (std devs)
	spreadPct := input.Volatility * input.Params.RiskFactor

	// Clamp spread to configured bounds (defaults keep the previous safety behavior).
	minSpread := input.Params.MinSpreadPct
	maxSpread := input.Params.MaxSpreadPct
	if minSpread <= 0 {
		minSpread = 0.005
	}
	if maxSpread <= 0 {
		maxSpread = 0.20
	}
	if maxSpread < minSpread {
		maxSpread = minSpread
	}
	if spreadPct < minSpread {
		spreadPct = minSpread
	}
	if spreadPct > maxSpread {
		spreadPct = maxSpread
	}

	// 3. Calculate Target Bounds
	lowerPrice := centerPrice * (1 - spreadPct)
	upperPrice := centerPrice * (1 + spreadPct)

	// 4. Convert Prices to Ticks (Uniswap V3 style)
	// Tick = log_1.0001(rawPrice), where rawPrice = token1Raw/token0Raw.
	//
	// Phoenix carries stable-per-priced-token prices through the system (e.g. USD per ETH).
	// Uniswap tick math, however, needs token0/token1 ratio. If the stable side is token1
	// (because token0/token1 ordering is address-sorted), we must invert.
	priceForTicksLower := lowerPrice
	priceForTicksUpper := upperPrice
	if !input.StableIsToken0 {
		priceForTicksLower = 1.0 / lowerPrice
		priceForTicksUpper = 1.0 / upperPrice
	}
	lowerTick := PriceToTickWithDecimals(priceForTicksLower, input.Token0Decimals, input.Token1Decimals)
	upperTick := PriceToTickWithDecimals(priceForTicksUpper, input.Token0Decimals, input.Token1Decimals)
	// Price-to-tick is not monotonic increasing in "stable-per-priced" space for all token orderings.
	// Always sort to ensure lowerTick <= upperTick and the range is well-formed.
	if lowerTick > upperTick {
		lowerTick, upperTick = upperTick, lowerTick
	}

	// 5. Calculate Target Delta
	// If CEX price > DEX price, we expect ARB to buy DEX, so price goes UP.
	// We want to be inventory neutral or slightly ask-heavy if we think price drops?
	// Simple logic: if FairPrice > DexPrice, we want more Token0 (ETH) to sell into the rise?
	// Actually: If P_cex > P_dex, arbitrageurs will BUY from DEX.
	// So DEX pool loses Token0 and gains Token1.
	// We want to provide liquidity in that direction?
	// Let's output a simple "rebalance required" ratio.

	targetDelta := 0.0 // Neutral

	return &EngineOutput{
		TargetLowerTick: lowerTick,
		TargetUpperTick: upperTick,
		TargetDelta:     targetDelta,
	}, nil
}

// PriceToTick converts human-readable price to tick.
// It assumes a WETH(18)/USDC(6) style pair; prefer PriceToTickWithDecimals.
func PriceToTick(price float64) int64 {
	if price <= 0 {
		return 0
	}
	return PriceToTickWithDecimals(price, 18, 6)
}

// PriceToTickWithDecimals converts human-readable price to tick using token decimals.
// price is Token0 per Token1 in human units (e.g. 2000 USDC per 1 ETH when token0=USDC, token1=WETH).
// Uniswap tick uses rawPrice = token1Raw/token0Raw = (1/price) * 10^(dec1-dec0).
func PriceToTickWithDecimals(price float64, token0Decimals, token1Decimals int) int64 {
	if price <= 0 {
		return 0
	}
	// Default to common ETH/stable decimals if not provided.
	if token0Decimals <= 0 {
		token0Decimals = 18
	}
	if token1Decimals <= 0 {
		token1Decimals = 6
	}
	rawPrice := (1.0 / price) * math.Pow(10, float64(token1Decimals-token0Decimals))
	return int64(math.Log(rawPrice) / math.Log(1.0001))
}

// PriceToTickRaw converts already-adjusted raw price to tick.
// Use this when price is already in raw format (e.g. 3.2e-9 for WETH/USDC).
func PriceToTickRaw(rawPrice float64) int64 {
	if rawPrice <= 0 {
		return 0
	}
	return int64(math.Log(rawPrice) / math.Log(1.0001))
}
