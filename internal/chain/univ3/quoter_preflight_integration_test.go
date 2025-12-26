//go:build integration

package univ3

import (
	"bytes"
	"context"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const peripheryImmutableStateABI = `[
  {"inputs":[],"name":"factory","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"WETH9","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"}
]`

const v3FactoryABI = `[
  {"inputs":[{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint24","name":"fee","type":"uint24"}],"name":"getPool","outputs":[{"internalType":"address","name":"pool","type":"address"}],"stateMutability":"view","type":"function"}
]`

func TestArbitrumSepoliaQuoterV2_Preflight_FactoryAndPool(t *testing.T) {
	rpcURL := os.Getenv("ARBITRUM_SEPOLIA_RPC_URL")
	quoterAddr := envAny("ARBITRUM_SEPOLIA_QUOTER_ADDRESS", "QUOTER_ADDRESS")
	token0 := envAny("TOKEN0_ADDRESS", "ARBITRUM_SEPOLIA_QUOTE_TOKEN_IN", "TOKEN_IN")
	token1 := envAny("TOKEN1_ADDRESS", "ARBITRUM_SEPOLIA_QUOTE_TOKEN_OUT", "TOKEN_OUT")
	feeStr := envAny("POOL_FEE", "ARBITRUM_SEPOLIA_QUOTE_FEE", "FEE")
	expectedPool := envAny("POOL_ADDRESS", "ARBITRUM_SEPOLIA_POOL_ADDRESS")

	if rpcURL == "" || quoterAddr == "" || token0 == "" || token1 == "" || feeStr == "" {
		t.Skip("set ARBITRUM_SEPOLIA_RPC_URL, ARBITRUM_SEPOLIA_QUOTER_ADDRESS, TOKEN0_ADDRESS, TOKEN1_ADDRESS, POOL_FEE to run")
	}
	if !common.IsHexAddress(quoterAddr) || !common.IsHexAddress(token0) || !common.IsHexAddress(token1) {
		t.Fatalf("invalid address: quoter=%s token0=%s token1=%s", quoterAddr, token0, token1)
	}
	if expectedPool != "" && !common.IsHexAddress(expectedPool) {
		t.Fatalf("invalid POOL_ADDRESS: %s", expectedPool)
	}

	fee64, err := strconv.ParseUint(feeStr, 10, 32)
	if err != nil {
		t.Fatalf("parse fee %q: %v", feeStr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	c, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	quoter := common.HexToAddress(quoterAddr)
	code, err := c.CodeAt(ctx, quoter, nil)
	if err != nil {
		t.Fatalf("code at quoter: %v", err)
	}
	if len(code) == 0 {
		t.Fatalf("no contract code at quoter address: %s", quoter.Hex())
	}
	// Quoter addresses are frequently confused with other periphery contracts (router/position manager),
	// which also expose factory()/WETH9() but do not implement quoteExactInputSingle.
	selV2 := crypto.Keccak256([]byte("quoteExactInputSingle((address,address,uint24,uint256,uint160))"))[:4]
	selV1 := crypto.Keccak256([]byte("quoteExactInputSingle(address,address,uint24,uint256,uint160)"))[:4]
	if !bytes.Contains(code, selV2) && !bytes.Contains(code, selV1) {
		t.Logf("warning: address %s does not appear to implement quoteExactInputSingle (may not be a Uniswap V3 Quoter); quoting may fail even though factory()/pool checks pass", quoter.Hex())
	}

	peripheryABI, err := abi.JSON(strings.NewReader(peripheryImmutableStateABI))
	if err != nil {
		t.Fatalf("parse periphery abi: %v", err)
	}
	call0 := func(method string) common.Address {
		data, err := peripheryABI.Pack(method)
		if err != nil {
			t.Fatalf("pack %s: %v", method, err)
		}
		msg := ethereum.CallMsg{To: &quoter, Data: data}
		res, err := c.CallContract(ctx, msg, nil)
		if err != nil {
			t.Fatalf("call %s: %v", method, err)
		}
		outs, err := peripheryABI.Unpack(method, res)
		if err != nil || len(outs) == 0 {
			t.Fatalf("unpack %s: %v", method, err)
		}
		return outs[0].(common.Address)
	}

	factory := call0("factory")
	if factory == (common.Address{}) {
		t.Fatal("quoter factory() returned zero address")
	}
	factoryCode, err := c.CodeAt(ctx, factory, nil)
	if err != nil {
		t.Fatalf("code at factory: %v", err)
	}
	if len(factoryCode) == 0 {
		t.Fatalf("no contract code at factory address: %s", factory.Hex())
	}

	factoryABI, err := abi.JSON(strings.NewReader(v3FactoryABI))
	if err != nil {
		t.Fatalf("parse factory abi: %v", err)
	}
	paramsFee := big.NewInt(int64(fee64)) // uint24
	data, err := factoryABI.Pack("getPool", common.HexToAddress(token0), common.HexToAddress(token1), paramsFee)
	if err != nil {
		t.Fatalf("pack getPool: %v", err)
	}
	msg := ethereum.CallMsg{To: &factory, Data: data}
	res, err := c.CallContract(ctx, msg, nil)
	if err != nil {
		t.Fatalf("call getPool: %v", err)
	}
	outs, err := factoryABI.Unpack("getPool", res)
	if err != nil || len(outs) == 0 {
		t.Fatalf("unpack getPool: %v", err)
	}
	pool := outs[0].(common.Address)
	if pool == (common.Address{}) {
		t.Fatal("factory.getPool returned zero address (pool not deployed)")
	}
	poolCode, err := c.CodeAt(ctx, pool, nil)
	if err != nil {
		t.Fatalf("code at pool: %v", err)
	}
	if len(poolCode) == 0 {
		t.Fatalf("no contract code at pool address: %s", pool.Hex())
	}
	if expectedPool != "" && !strings.EqualFold(pool.Hex(), expectedPool) {
		t.Fatalf("factory.getPool mismatch: got %s expected %s", pool.Hex(), expectedPool)
	}
}
