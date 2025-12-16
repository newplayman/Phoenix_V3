//go:build integration

package chain

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

func TestArbitrumSepoliaRPCChainID(t *testing.T) {
	rpcURL := os.Getenv("ARBITRUM_SEPOLIA_RPC_URL")
	if rpcURL == "" {
		t.Skip("ARBITRUM_SEPOLIA_RPC_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	chainID, err := c.ChainID(ctx)
	if err != nil {
		t.Fatalf("chain id: %v", err)
	}
	if chainID.Cmp(big.NewInt(421614)) != 0 {
		t.Fatalf("unexpected chain id: %s", chainID.String())
	}
}
