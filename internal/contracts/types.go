package contracts

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Feed / Market Data ---------------------------------------------------------

type Ticker struct {
	Symbol    string
	Price     float64
	Timestamp time.Time
}

type PriceFeed interface {
	Start(ctx context.Context) error
	SubscribeTicker(symbol string) (<-chan Ticker, error)
}

// DEX State -----------------------------------------------------------------

type PoolState struct {
	ChainID     int64
	PoolAddress common.Address
	CurrentTick int64
	Liquidity   *big.Int
}

type DexState interface {
	GetPoolState(chainID int64, poolAddress common.Address) (*PoolState, error)
}

// Engine / Strategy ---------------------------------------------------------

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

// Intent / Scheduler --------------------------------------------------------

type IntentType string

const (
	IntentRebalance  IntentType = "rebalance"
	IntentWithdraw   IntentType = "withdraw"
	IntentCollectFee IntentType = "collect_fee"
	IntentSwap       IntentType = "swap"
)

type Intent struct {
	ID              string
	Type            IntentType
	PoolID          string
	ChainID         int64
	Urgency         int
	Deadline        time.Time
	ExpectedPnL     float64
	StrategyVersion string
	RiskMode        string
	Metadata        map[string]string
}

type IntentQueue interface {
	Enqueue(intent Intent)
	Dequeue() Intent
	Len() int
}

// Gateway / Chain -----------------------------------------------------------

type TxStatus string

const (
	StatusCreated     TxStatus = "created"
	StatusSigned      TxStatus = "signed"
	StatusBroadcasted TxStatus = "broadcasted"
	StatusPending     TxStatus = "pending"
	StatusMined       TxStatus = "mined"
	StatusReverted    TxStatus = "reverted"
	StatusDropped     TxStatus = "dropped"
)

type TxResult struct {
	Hash   common.Hash
	Status TxStatus
	Error  error
}

type Gateway interface {
	Send(ctx context.Context, intent Intent) (*TxResult, error)
}
