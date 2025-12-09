package dexstate

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

type PoolState struct {
	ChainID     int64
	PoolAddress common.Address
	CurrentTick int64
	Liquidity   *big.Int
}

type DexState interface {
	GetPoolState(chainID int64, poolAddress common.Address) (*PoolState, error)
}
