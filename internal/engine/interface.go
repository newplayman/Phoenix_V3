package engine

type CurrentPosition struct {
	LowerTick int64
	UpperTick int64
	Liquidity float64
}

type StrategyParams struct {
	RiskFactor float64
}

type EngineInput struct {
	CexPrice   float64
	DexPrice   float64
	Volatility float64
	Position   CurrentPosition
	Params     StrategyParams
}

type EngineOutput struct {
	TargetLowerTick int64
	TargetUpperTick int64
	TargetDelta     float64
}

type Engine interface {
	Calculate(input EngineInput) (*EngineOutput, error)
}
