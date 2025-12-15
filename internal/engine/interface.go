package engine

type CurrentPosition struct {
	LowerTick int64
	UpperTick int64
	Liquidity float64
}

type StrategyParams struct {
	RiskFactor   float64
	MinSpreadPct float64
	MaxSpreadPct float64
}

type EngineInput struct {
	CexPrice       float64
	DexPrice       float64
	Volatility     float64
	Position       CurrentPosition
	Token0Decimals int
	Token1Decimals int
	// StableIsToken0 indicates whether the stable side of the pool is token0 (Uniswap ordering).
	// Phoenix uses stable-per-priced-token (e.g. USD/ETH) prices for CEX/DexPrice, and this flag
	// tells the engine whether it must invert when converting that human price into token0/token1
	// ratio for tick math.
	StableIsToken0 bool
	Params         StrategyParams
}

type EngineOutput struct {
	TargetLowerTick int64
	TargetUpperTick int64
	TargetDelta     float64
}

type Engine interface {
	Calculate(input EngineInput) (*EngineOutput, error)
}
