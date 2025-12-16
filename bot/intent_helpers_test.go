package bot

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type fakeBalReader struct {
	bals map[common.Address]*big.Int
}

func (f fakeBalReader) BalanceOfERC20(_ context.Context, token common.Address) (*big.Int, error) {
	if f.bals == nil {
		return big.NewInt(0), nil
	}
	if b := f.bals[token]; b != nil {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}

func TestHasSufficientBalances(t *testing.T) {
	t0 := common.HexToAddress("0x0000000000000000000000000000000000000001")
	t1 := common.HexToAddress("0x0000000000000000000000000000000000000002")
	r := fakeBalReader{bals: map[common.Address]*big.Int{t0: big.NewInt(10), t1: big.NewInt(20)}}

	meta := map[string]string{
		"token0":  t0.Hex(),
		"amount0": "10",
		"token1":  t1.Hex(),
		"amount1": "21",
	}
	if HasSufficientBalances(context.Background(), r, meta) {
		t.Fatal("expected insufficient balances")
	}
	meta["amount1"] = "20"
	if !HasSufficientBalances(context.Background(), r, meta) {
		t.Fatal("expected sufficient balances")
	}
}

func TestParseMintedPositionTokenID(t *testing.T) {
	pm := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	to := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	transferSig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	var fromZero common.Address
	tokenID := big.NewInt(12345)

	lg := &types.Log{
		Address: pm,
		Topics: []common.Hash{
			transferSig,
			common.BytesToHash(common.LeftPadBytes(fromZero.Bytes(), 32)),
			common.BytesToHash(common.LeftPadBytes(to.Bytes(), 32)),
			common.BytesToHash(common.LeftPadBytes(tokenID.Bytes(), 32)),
		},
	}
	rcpt := &types.Receipt{Logs: []*types.Log{lg}}
	got := ParseMintedPositionTokenID(rcpt, pm, to)
	if got == nil || got.Cmp(tokenID) != 0 {
		t.Fatalf("expected %s, got %v", tokenID.String(), got)
	}
}
