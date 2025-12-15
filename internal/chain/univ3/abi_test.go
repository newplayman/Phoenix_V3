package univ3

import (
	"math/big"
	"testing"
	
	"github.com/ethereum/go-ethereum/common"
	
	"phoenix-v3/internal/rebalancer"
	"phoenix-v3/internal/strategy"
)

func TestAdapter_BuildMintData(t *testing.T) {
	// 0xC36442b4a4522E871399CD717aBDD847Ab11FE88 (Mainnet NFP Manager)
	adapter := NewAdapter("0xC36442b4a4522E871399CD717aBDD847Ab11FE88")
	
	intent := strategy.Intent{
		Metadata: map[string]string{
			"token0": "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", // WETH
			"token1": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", // USDC
			"lower_tick": "-201000",
			"upper_tick": "-199000",
			"amount0": "1000000000000000000",
			"amount1": "2000000000",
			"fee": "3000",
			"recipient": "0x1234567890123456789012345678901234567890",
		},
	}
	
	data, err := adapter.BuildMintData(intent)
	if err != nil {
		t.Fatalf("BuildMintData failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Empty data")
	}
	// Verify method ID 88316456 (mint)
	// First 4 bytes
	if len(data) < 4 {
		t.Fatal("Data too short")
	}
	// 0x88316456 = [136 49 100 86]
	if data[0] != 0x88 || data[1] != 0x31 || data[2] != 0x64 || data[3] != 0x56 {
		t.Errorf("Wrong Method ID: %x", data[:4])
	}
}

func TestRouter_BuildSwapData(t *testing.T) {
	router := NewRouter("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	
	valOne := big.NewInt(1000000)
	
	action := rebalancer.SwapAction{
		FromToken: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
		ToToken: common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"),
		AmountIn: valOne,
		MinAmountOut: big.NewInt(500),
		SlippagePct: 0.01,
	}
	
	data, err := router.BuildSwapData(action, "0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatalf("BuildSwapData failed: %v", err)
	}
	// Method ID for exactInputSingle = 0x414bf389 (actually depends on struct components check in abi)
	// Let's verify it packs at least.
	if len(data) < 4 {
		t.Fatal("Data too short")
	}
}
