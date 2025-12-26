//go:build integration

package univ3

import (
	"context"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type ethClientCaller struct {
	c *ethclient.Client
}

func (e ethClientCaller) Call(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	if e.c == nil {
		return nil, context.Canceled
	}
	msg := ethereum.CallMsg{To: &to, Data: data}
	return e.c.CallContract(ctx, msg, nil)
}

func envAny(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func TestArbitrumSepoliaQuoter_QuoteExactInputSingle(t *testing.T) {
	rpcURL := os.Getenv("ARBITRUM_SEPOLIA_RPC_URL")
	quoterAddr := envAny("ARBITRUM_SEPOLIA_QUOTER_ADDRESS", "QUOTER_ADDRESS")
	tokenIn := envAny("ARBITRUM_SEPOLIA_QUOTE_TOKEN_IN", "TOKEN0_ADDRESS", "TOKEN_IN")
	tokenOut := envAny("ARBITRUM_SEPOLIA_QUOTE_TOKEN_OUT", "TOKEN1_ADDRESS", "TOKEN_OUT")
	feeStr := envAny("ARBITRUM_SEPOLIA_QUOTE_FEE", "POOL_FEE", "FEE")
	amtInStr := envAny("ARBITRUM_SEPOLIA_QUOTE_AMOUNT_IN", "AMOUNT_IN")

	if rpcURL == "" || quoterAddr == "" || tokenIn == "" || tokenOut == "" || feeStr == "" || amtInStr == "" {
		t.Skip("set ARBITRUM_SEPOLIA_RPC_URL, ARBITRUM_SEPOLIA_QUOTER_ADDRESS, tokenIn/out, fee, amountIn to run")
	}
	if !common.IsHexAddress(quoterAddr) || !common.IsHexAddress(tokenIn) || !common.IsHexAddress(tokenOut) {
		t.Fatalf("invalid address: quoter=%s tokenIn=%s tokenOut=%s", quoterAddr, tokenIn, tokenOut)
	}

	fee64, err := strconv.ParseUint(feeStr, 10, 32)
	if err != nil {
		t.Fatalf("parse fee %q: %v", feeStr, err)
	}
	amountIn := new(big.Int)
	if _, ok := amountIn.SetString(amtInStr, 10); !ok || amountIn.Sign() <= 0 {
		t.Fatalf("invalid amountIn %q", amtInStr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	c, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	q, err := NewQuoter(quoterAddr)
	if err != nil {
		t.Fatalf("new quoter: %v", err)
	}

	code, err := c.CodeAt(ctx, q.Address, nil)
	if err != nil {
		t.Fatalf("code at quoter: %v", err)
	}
	if len(code) == 0 {
		t.Fatalf("no contract code at quoter address: %s", q.Address.Hex())
	}

	amountOut, err := q.QuoteExactInputSingle(
		ctx,
		ethClientCaller{c: c},
		common.HexToAddress(tokenIn),
		common.HexToAddress(tokenOut),
		uint32(fee64),
		amountIn,
		big.NewInt(0),
	)
	if err != nil {
		// Many deployments (including Arbitrum Sepolia in this repo) do not implement quoteExactInputSingle.
		// Fall back to the path-based quoteExactInput which is widely supported.
		amountOut, err = q.QuoteExactInput(
			ctx,
			ethClientCaller{c: c},
			common.HexToAddress(tokenIn),
			common.HexToAddress(tokenOut),
			uint32(fee64),
			amountIn,
		)
		if err != nil {
			t.Fatalf("quote failed (single + path fallback): %v", err)
		}
	}
	if amountOut == nil || amountOut.Sign() <= 0 {
		t.Fatalf("unexpected amountOut: %v", amountOut)
	}
}
