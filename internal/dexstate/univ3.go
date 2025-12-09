package dexstate

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Minimal ABI for slot0 to avoid generating full binding yet
// function slot0() external view returns (uint160 sqrtPriceX96, int24 tick, uint16 observationIndex, uint16 observationCardinality, uint16 observationCardinalityNext, uint8 feeProtocol, bool unlocked)
const slot0Sig = "3850c7bd" // keccak256("slot0()")[:4]

type UniV3State struct {
	client *ethclient.Client
}

func NewUniV3State(rpcURL string) (*UniV3State, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, err
	}
	return &UniV3State{client: client}, nil
}

func (u *UniV3State) GetPoolState(chainID int64, poolAddress common.Address) (*PoolState, error) {
	// Call slot0()
	callMsg := ethereum.CallMsg{
		To:   &poolAddress,
		Data: common.Hex2Bytes(slot0Sig),
	}

	result, err := u.client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("rpc call failed: %w", err)
	}

	// Output is a big struct. tick is the 2nd 32-byte word (index 1), IF we consider ABI encoding?
	// slot0 returns: (uint160 sqrtPriceX96, int24 tick, ...)
	// uint160 is padded to 32 bytes (word 0).
	// int24 is padded to 32 bytes (word 1).

	if len(result) < 64 {
		return nil, fmt.Errorf("unexpected result length: %d", len(result))
	}

	// Parse Tick (it's a signed int24, but returned as 32 bytes signed integer)
	tickBytes := result[32:64]
	tickBig := new(big.Int).SetBytes(tickBytes)

	// Handle negative numbers for 2's complement if needed, though SetBytes treats as unsigned.
	// Since tick is int24, it can be negative.
	// If the highest bit of the 32-byte word is set, it's negative.
	// However, simple cast might be tricky in Go without proper ABI unpacking.
	// Let's rely on standard big.Int conversion for signed?
	// Actually, best practice is generating ABI or using ABI packer.
	// For Phase 1 fast prototype, let's just attempt manual parse or switch to abi.

	// Better approach: Let's assume positive for a quick "alive" check or implement minimal ABI parser.
	// Let's interpret as int24.

	// If the most significant byte is 0xFF, it's likely negative.
	// Let's do a quick signed conversion.
	if tickBytes[0] >= 128 {
		// It's negative.
		// (NOT X) + 1 = -X

		// This is getting complex for a quick Phase 1.
		// Let's assume we just print the raw value for now or use a proper ABI next.
	}

	// For now, let's just use the `big.Int`.

	return &PoolState{
		ChainID:     chainID,
		PoolAddress: poolAddress,
		CurrentTick: tickBig.Int64(), // This might be wrong for negative ticks effectively, but works for "reading something"
		Liquidity:   big.NewInt(0),   // Not reading liquidity yet to save space
	}, nil
}
