//go:build integration

package dexstate

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestArbitrumSepoliaPoolState_Slot0AndLiquidity(t *testing.T) {
	rpcURL := os.Getenv("ARBITRUM_SEPOLIA_RPC_URL")
	poolAddr := os.Getenv("ARBITRUM_SEPOLIA_POOL_ADDRESS")
	if rpcURL == "" || poolAddr == "" {
		t.Skip("ARBITRUM_SEPOLIA_RPC_URL or ARBITRUM_SEPOLIA_POOL_ADDRESS not set")
	}
	if !common.IsHexAddress(poolAddr) {
		t.Fatalf("invalid pool address: %s", poolAddr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	code, err := c.CodeAt(ctx, common.HexToAddress(poolAddr), nil)
	if err != nil {
		t.Fatalf("code at: %v", err)
	}
	if len(code) == 0 {
		t.Skipf("no contract code at %s", poolAddr)
	}

	s, err := NewUniV3State(rpcURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	state, err := s.GetPoolState(421614, common.HexToAddress(poolAddr))
	if err != nil {
		t.Fatalf("get pool state: %v", err)
	}
	if state == nil {
		t.Fatal("nil pool state")
	}
	if state.SqrtPriceX96 == nil || state.SqrtPriceX96.Sign() <= 0 {
		t.Fatalf("unexpected sqrtPriceX96: %v", state.SqrtPriceX96)
	}
	if state.Liquidity == nil {
		t.Fatal("nil liquidity")
	}
}
