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

	// 1. Determine the "Fair Price"
	// For simplicity, we might weight CEX price higher as it's the "leader"
	fairPrice := (input.CexPrice*0.8 + input.DexPrice*0.2)

	// 2. Calculate Half-Spread based on Volatility and RiskFactor
	// Formula: spread = volatility * risk_factor * scalar
	// Example: 2% volatility * 1.5 risk * 2 (std devs)
	spreadPct := input.Volatility * input.Params.RiskFactor

	// Safety clamp: minimum 0.5% spread, max 20%
	if spreadPct < 0.005 {
		spreadPct = 0.005
	}
	if spreadPct > 0.20 {
		spreadPct = 0.20
	}

	// 3. Calculate Target Bounds
	lowerPrice := fairPrice * (1 - spreadPct)
	upperPrice := fairPrice * (1 + spreadPct)

	// 4. Convert Prices to Ticks (Uniswap V3 style)
	// Tick = log_1.0001(Price)
	// Note: This assumes Token0/Token1 price. If reversed, logic needs conditional.
	// For this Engine, we assume Price is Token1/Token0 (e.g. 2000 USDC per ETH).
	lowerTick := PriceToTick(lowerPrice)
	upperTick := PriceToTick(upperPrice)

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

// PriceToTick converts price to closest tick.
// T = log(P) / log(1.0001)
func PriceToTick(price float64) int64 {
	if price <= 0 {
		return 0
	}
	return int64(math.Log(price) / math.Log(1.0001))
}
