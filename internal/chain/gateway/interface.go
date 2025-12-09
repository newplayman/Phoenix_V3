package gateway

import (
	"context"

	"phoenix-v3/internal/strategy"

	"github.com/ethereum/go-ethereum/common"
)

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
	Send(ctx context.Context, intent strategy.Intent) (*TxResult, error)
}
