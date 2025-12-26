package univ3

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"phoenix-v3/internal/contracts"
)

// Minimal definitions for NonfungiblePositionManager
// We manually construct calldata for Mint/Burn to avoid 'abigen' dependency issues in this env.

// Function Signatures
// mint((address,address,uint24,int24,int24,uint256,uint256,uint256,uint256,address,uint256))
// 0x88316456
var mintSig = crypto.Keccak256([]byte("mint((address,address,uint24,int24,int24,uint256,uint256,uint256,uint256,address,uint256))"))[:4]

type Adapter struct {
	PositionManager common.Address
}

func NewAdapter(posMgr string) *Adapter {
	return &Adapter{
		PositionManager: common.HexToAddress(posMgr),
	}
}

func (a *Adapter) TargetAddress() common.Address {
	return a.PositionManager
}

// BuildMintData constructs the calldata for minting a new position
func (a *Adapter) BuildMintData(intent contracts.Intent) ([]byte, error) {
	fmt.Printf("[Adapter] Building Mint Data for Intent %s\n", intent.ID)

	type MintParams struct {
		Token0         common.Address
		Token1         common.Address
		Fee            *big.Int
		TickLower      *big.Int
		TickUpper      *big.Int
		Amount0Desired *big.Int
		Amount1Desired *big.Int
		Amount0Min     *big.Int
		Amount1Min     *big.Int
		Recipient      common.Address
		Deadline       *big.Int
	}

	fee := parseUint(intent.Metadata["fee"], 500)
	tickSpacing := spacingForFee(fee)
	rawLower := parseInt(intent.Metadata["tick_lower"], -100)
	rawUpper := parseInt(intent.Metadata["tick_upper"], 100)
	lower := snapTickDown(rawLower, tickSpacing)
	upper := snapTickUp(rawUpper, tickSpacing)
	if lower >= upper {
		upper = lower + tickSpacing
	}

	params := MintParams{
		Token0:         common.HexToAddress(intent.Metadata["token0"]),
		Token1:         common.HexToAddress(intent.Metadata["token1"]),
		Fee:            uintToBig(fee),
		TickLower:      intToBig(lower),
		TickUpper:      intToBig(upper),
		Amount0Desired: big.NewInt(parseInt(intent.Metadata["amount0"], 0)),
		Amount1Desired: big.NewInt(parseInt(intent.Metadata["amount1"], 0)),
		Amount0Min:     big.NewInt(0),
		Amount1Min:     big.NewInt(0),
		Recipient:      common.HexToAddress(intent.Metadata["recipient"]),
		Deadline:       big.NewInt(parseInt(intent.Metadata["deadline"], 0)),
	}

	mintType, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "token0", Type: "address"},
		{Name: "token1", Type: "address"},
		{Name: "fee", Type: "uint24"},
		{Name: "tickLower", Type: "int24"},
		{Name: "tickUpper", Type: "int24"},
		{Name: "amount0Desired", Type: "uint256"},
		{Name: "amount1Desired", Type: "uint256"},
		{Name: "amount0Min", Type: "uint256"},
		{Name: "amount1Min", Type: "uint256"},
		{Name: "recipient", Type: "address"},
		{Name: "deadline", Type: "uint256"},
	})
	if err != nil {
		return nil, err
	}

	arguments := abi.Arguments{{Type: mintType}}

	payload, err := arguments.Pack(params)
	if err != nil {
		return nil, err
	}

	return append(mintSig, payload...), nil
}

func parseInt(val string, def int64) int64 {
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

func parseUint(val string, def uint64) uint64 {
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

func uintToBig(val uint64) *big.Int {
	return new(big.Int).SetUint64(val)
}

func intToBig(val int64) *big.Int {
	return big.NewInt(val)
}

func spacingForFee(fee uint64) int64 {
	switch fee {
	case 100:
		return 1
	case 500:
		return 10
	case 3000:
		return 60
	case 10000:
		return 200
	default:
		return 1
	}
}

func snapTickDown(value int64, spacing int64) int64 {
	if spacing <= 0 {
		return value
	}
	remainder := value % spacing
	if remainder == 0 {
		return value
	}
	if value >= 0 {
		return value - remainder
	}
	return value - remainder - spacing
}

func snapTickUp(value int64, spacing int64) int64 {
	if spacing <= 0 {
		return value
	}
	remainder := value % spacing
	if remainder == 0 {
		return value
	}
	if value >= 0 {
		return value + (spacing - remainder)
	}
	return value - remainder
}
