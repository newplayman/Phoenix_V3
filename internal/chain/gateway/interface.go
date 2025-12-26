package gateway

import "phoenix-v3/internal/contracts"

type (
	TxStatus = contracts.TxStatus
	TxResult = contracts.TxResult
	Gateway  = contracts.Gateway
)

const (
	StatusCreated     = contracts.StatusCreated
	StatusSigned      = contracts.StatusSigned
	StatusBroadcasted = contracts.StatusBroadcasted
	StatusPending     = contracts.StatusPending
	StatusMined       = contracts.StatusMined
	StatusReverted    = contracts.StatusReverted
	StatusDropped     = contracts.StatusDropped
)
