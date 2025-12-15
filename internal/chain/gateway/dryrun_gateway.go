package gateway

import (
	"context"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/strategy"
)

// DryRunGateway is a lightweight gateway used for dry-run rehearsal.
// executeIntent short-circuits before calling Send when dry-run is enabled,
// but it still requires a non-nil Gateway.
type DryRunGateway struct {
	chainID int64
}

func NewDryRunGateway(chainID int64) *DryRunGateway {
	return &DryRunGateway{chainID: chainID}
}

func (g *DryRunGateway) Send(ctx context.Context, intent strategy.Intent) (*TxResult, error) {
	_ = ctx
	_ = intent
	return &TxResult{Hash: common.Hash{}, Status: StatusSigned}, nil
}

