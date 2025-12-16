package gateway

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// StaticBalanceReader is a test-only BalanceReader that returns fixed ERC20 balances.
// It is intended for offline rehearsals where preview/plan generation must work without RPC.
type StaticBalanceReader struct {
	wallet   common.Address
	balances map[common.Address]*big.Int
}

func NewStaticBalanceReader(wallet common.Address, balances map[common.Address]*big.Int) *StaticBalanceReader {
	cp := map[common.Address]*big.Int{}
	for k, v := range balances {
		if v == nil {
			continue
		}
		cp[k] = new(big.Int).Set(v)
	}
	return &StaticBalanceReader{wallet: wallet, balances: cp}
}

func (s *StaticBalanceReader) WalletAddress() common.Address { return s.wallet }

func (s *StaticBalanceReader) BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error) {
	_ = ctx
	if s == nil {
		return big.NewInt(0), nil
	}
	if b, ok := s.balances[token]; ok && b != nil {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}
