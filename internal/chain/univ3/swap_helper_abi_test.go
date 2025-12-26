package univ3

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSwapHelper_BuildSwapExactInputSingleMinOutData_Selector(t *testing.T) {
	h, err := NewSwapHelper("0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("new swaphelper: %v", err)
	}

	data, err := h.BuildSwapExactInputSingleMinOutData(
		common.HexToAddress("0x0000000000000000000000000000000000000011"),
		common.HexToAddress("0x0000000000000000000000000000000000000022"),
		common.HexToAddress("0x0000000000000000000000000000000000000033"),
		big.NewInt(123),
		big.NewInt(100),
		big.NewInt(0),
	)
	if err != nil {
		t.Fatalf("build calldata: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("calldata too short: %d", len(data))
	}

	wantSel := crypto.Keccak256([]byte("swapExactInputSingleMinOut(address,address,address,uint256,uint256,uint160)"))[:4]
	if hex.EncodeToString(data[:4]) != hex.EncodeToString(wantSel) {
		t.Fatalf("selector mismatch got=%x want=%x", data[:4], wantSel)
	}
}

