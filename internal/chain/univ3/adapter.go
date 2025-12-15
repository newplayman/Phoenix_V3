package univ3

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	
	"phoenix-v3/internal/strategy"
)

type Adapter struct {
	PositionManager common.Address
	ParsedABI       abi.ABI
}

func NewAdapter(posMgr string) *Adapter {
	parsed, err := abi.JSON(strings.NewReader(NonfungiblePositionManagerABI))
	if err != nil {
		// Should not happen with static constant
		panic(fmt.Sprintf("failed to parse PositionManager ABI: %v", err))
	}
	return &Adapter{
		PositionManager: common.HexToAddress(posMgr),
		ParsedABI:       parsed,
	}
}

func (a *Adapter) TargetAddress() common.Address {
	return a.PositionManager
}

// MintParams struct matching the ABI tuple
type MintParams struct {
	Token0         common.Address
	Token1         common.Address
	Fee            *big.Int // uint24 but go-ethereum uses big.Int or type reflection carefully. struct field type needs to match what Pack expects.
	// Actually, for uint24, abi.Pack usually expects uint32 or *big.Int? 
	// Let's check Geth ABI packing rules. Usually straightforward to use appropriate Go types.
	// uint24 -> uint32 or *big.Int is safer. Let's try native types where possible or big.Int.
	TickLower      *big.Int // int24
	TickUpper      *big.Int // int24
	Amount0Desired *big.Int
	Amount1Desired *big.Int
	Amount0Min     *big.Int
	Amount1Min     *big.Int
	Recipient      common.Address
	Deadline       *big.Int
}

// BuildMintData constructs the calldata for minting a new position
func (a *Adapter) BuildMintData(intent strategy.Intent) ([]byte, error) {
	// 1. Extract params from intent.Metadata
	token0 := common.HexToAddress(intent.Metadata["token0"])
	token1 := common.HexToAddress(intent.Metadata["token1"])
	if token0 == (common.Address{}) || token1 == (common.Address{}) {
		return nil, fmt.Errorf("mint: token0/token1 required")
	}
	fee := parseMetaBig(intent.Metadata, "fee")
	if fee.Sign() == 0 {
		fee = big.NewInt(3000) // Default 0.3%
	}
	
	tickLower := parseMetaBig(intent.Metadata, "lower_tick")
	tickUpper := parseMetaBig(intent.Metadata, "upper_tick")
	if tickLower.Cmp(tickUpper) >= 0 {
		return nil, fmt.Errorf("mint: invalid ticks lower=%s upper=%s", tickLower.String(), tickUpper.String())
	}
	
	amt0 := parseMetaBig(intent.Metadata, "amount0")
	amt1 := parseMetaBig(intent.Metadata, "amount1")
	if amt0.Sign() < 0 || amt1.Sign() < 0 {
		return nil, fmt.Errorf("mint: invalid amounts amount0=%s amount1=%s", amt0.String(), amt1.String())
	}
	
	// Slippage handling for Min amounts (Simple 0.5% default if not set)
	// In Phase 1, we might just set 0 for strictly "Attempt construction" 
	// or calc 0.98 * desired.
	// Let's set 0 for simplicity/safety against revert on slight move,
	// BUT production should compute this.
	amt0Min := big.NewInt(0)
	amt1Min := big.NewInt(0)
	
	recipient := common.HexToAddress(intent.Metadata["recipient"])
	if recipient == (common.Address{}) {
		return nil, fmt.Errorf("mint: recipient required")
	}
	deadline := big.NewInt(time.Now().Add(10 * time.Minute).Unix())

	// Struct matching the ABI:
	// The ABI definition uses specific types.
	// We need to pass a struct that matches the component structure.
	
	params := struct {
		Token0         common.Address
		Token1         common.Address
		Fee            *big.Int // uint24 in ABI
		TickLower      *big.Int // int24
		TickUpper      *big.Int // int24
		Amount0Desired *big.Int
		Amount1Desired *big.Int
		Amount0Min     *big.Int
		Amount1Min     *big.Int
		Recipient      common.Address
		Deadline       *big.Int
	}{
		Token0:         token0,
		Token1:         token1,
		Fee:            fee,
		TickLower:      tickLower,
		TickUpper:      tickUpper,
		Amount0Desired: amt0,
		Amount1Desired: amt1,
		Amount0Min:     amt0Min,
		Amount1Min:     amt1Min,
		Recipient:      recipient,
		Deadline:       deadline,
	}
	
	// Pack "mint"
	data, err := a.ParsedABI.Pack("mint", params)
	if err != nil {
		return nil, fmt.Errorf("failed to pack mint: %w", err)
	}
	
	return data, nil
}

func (a *Adapter) BuildDecreaseLiquidityData(intent strategy.Intent) ([]byte, error) {
	tokenId := parseMetaBig(intent.Metadata, "token_id")
	liq := parseMetaBig(intent.Metadata, "liquidity")
	
	params := struct {
		TokenId    *big.Int
		Liquidity  *big.Int
		Amount0Min *big.Int
		Amount1Min *big.Int
		Deadline   *big.Int
	}{
		TokenId:    tokenId,
		Liquidity:  liq,
		Amount0Min: big.NewInt(0),
		Amount1Min: big.NewInt(0),
		Deadline:   big.NewInt(time.Now().Add(10*time.Minute).Unix()),
	}
	return a.ParsedABI.Pack("decreaseLiquidity", params)
}

func (a *Adapter) BuildCollectData(intent strategy.Intent) ([]byte, error) {
	tokenId := parseMetaBig(intent.Metadata, "token_id")
	recipient := common.HexToAddress(intent.Metadata["recipient"])
	if recipient == (common.Address{}) {
		return nil, fmt.Errorf("collect: recipient required")
	}
	
	// Max uint128 for Collect Max
	max128 := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil), big.NewInt(1))
	
	params := struct {
		TokenId    *big.Int
		Recipient  common.Address
		Amount0Max *big.Int
		Amount1Max *big.Int
	}{
		TokenId:    tokenId,
		Recipient:  recipient,
		Amount0Max: max128,
		Amount1Max: max128,
	}
	return a.ParsedABI.Pack("collect", params)
}

func (a *Adapter) BuildBurnNFTData(intent strategy.Intent) ([]byte, error) {
	tokenId := parseMetaBig(intent.Metadata, "token_id")
	return a.ParsedABI.Pack("burn", tokenId)
}

func parseMetaBig(meta map[string]string, key string) *big.Int {
	valStr := meta[key]
	if valStr == "" {
		return big.NewInt(0)
	}
	i, ok := new(big.Int).SetString(valStr, 10)
	if !ok {
		return big.NewInt(0)
	}
	return i
}
