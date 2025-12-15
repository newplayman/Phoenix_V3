package poolguard

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

type mockCaller struct {
	ret *big.Int
}

func (m *mockCaller) Call(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	// Simulate packing return of totalSupply.
	outs, _ := erc20CheckABI.Pack("totalSupply")
	_ = outs
	// totalSupply ABI returns uint256, so we encode using abi.Arguments.
	args := abi.Arguments{{Type: erc20CheckABI.Methods["totalSupply"].Outputs[0].Type}}
	encoded, _ := args.Pack(m.ret)
	return encoded, nil
}

func TestCheckPool_TotalSupplyZero(t *testing.T) {
	g := NewGuard()
	g.SetChainCaller(1, &mockCaller{ret: big.NewInt(0)})
	res := g.CheckPool(context.Background(), "pool-x", 1, "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222")
	if res.Risk != RiskDanger {
		t.Fatalf("expected danger, got %s", res.Risk)
	}
}
