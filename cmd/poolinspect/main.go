package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var (
		rpcURL  = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		poolStr = flag.String("pool", "", "pool address (0x...)")
		timeout = flag.Duration("timeout", 20*time.Second, "RPC timeout")
		asJSON  = flag.Bool("json", false, "print JSON")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if !common.IsHexAddress(*poolStr) {
		log.Fatalf("missing/invalid -pool address: %q", *poolStr)
	}
	pool := common.HexToAddress(*poolStr)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	gotChainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("chain id: %v", err)
	}
	if gotChainID.Cmp(big.NewInt(*chainID)) != 0 {
		log.Fatalf("unexpected chain id: got %s want %d", gotChainID.String(), *chainID)
	}

	parsed, err := abi.JSON(strings.NewReader(`[
		{"inputs":[],"name":"slot0","outputs":[
			{"internalType":"uint160","name":"sqrtPriceX96","type":"uint160"},
			{"internalType":"int24","name":"tick","type":"int24"},
			{"internalType":"uint16","name":"observationIndex","type":"uint16"},
			{"internalType":"uint16","name":"observationCardinality","type":"uint16"},
			{"internalType":"uint16","name":"observationCardinalityNext","type":"uint16"},
			{"internalType":"uint8","name":"feeProtocol","type":"uint8"},
			{"internalType":"bool","name":"unlocked","type":"bool"}
		],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"liquidity","outputs":[{"internalType":"uint128","name":"","type":"uint128"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"token0","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"token1","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"fee","outputs":[{"internalType":"uint24","name":"","type":"uint24"}],"stateMutability":"view","type":"function"}
	]`))
	if err != nil {
		log.Fatalf("abi parse: %v", err)
	}

	callView := func(method string) ([]byte, error) {
		data, err := parsed.Pack(method)
		if err != nil {
			return nil, err
		}
		msg := ethereum.CallMsg{To: &pool, Data: data}
		return client.CallContract(ctx, msg, nil)
	}

	type slot0Out struct {
		SqrtPriceX96               *big.Int
		Tick                       *big.Int
		ObservationIndex           uint16
		ObservationCardinality     uint16
		ObservationCardinalityNext uint16
		FeeProtocol                uint8
		Unlocked                   bool
	}
	type result struct {
		ChainID      int64  `json:"chain_id"`
		Pool         string `json:"pool"`
		CodePresent  bool   `json:"code_present"`
		SqrtPriceX96 string `json:"sqrt_price_x96"`
		Tick         int64  `json:"tick"`
		Liquidity    string `json:"liquidity"`
		Token0       string `json:"token0,omitempty"`
		Token1       string `json:"token1,omitempty"`
		Fee          uint32 `json:"fee,omitempty"`
	}

	code, err := client.CodeAt(ctx, pool, nil)
	if err != nil {
		log.Fatalf("codeAt: %v", err)
	}
	out := result{ChainID: *chainID, Pool: pool.Hex(), CodePresent: len(code) > 0}

	// slot0 is the minimal requirement to treat this as "UniV3-like" for read-only rehearsal.
	{
		b, err := callView("slot0")
		if err != nil {
			log.Fatalf("slot0 call failed: %v", err)
		}
		var s slot0Out
		if err := parsed.UnpackIntoInterface(&s, "slot0", b); err != nil {
			log.Fatalf("slot0 decode failed: %v", err)
		}
		if s.SqrtPriceX96 != nil {
			out.SqrtPriceX96 = s.SqrtPriceX96.String()
		}
		if s.Tick != nil {
			out.Tick = s.Tick.Int64()
		}
	}
	{
		b, err := callView("liquidity")
		if err != nil {
			log.Fatalf("liquidity call failed: %v", err)
		}
		var vals []interface{}
		vals, err = parsed.Unpack("liquidity", b)
		if err != nil || len(vals) < 1 {
			log.Fatalf("liquidity decode failed: %v", err)
		}
		if v, ok := vals[0].(*big.Int); ok && v != nil {
			out.Liquidity = v.String()
		}
	}

	// Optional: token0/token1/fee are helpful for real pool wiring, but not required for the live-read rehearsal.
	callAddr := func(method string) (common.Address, bool) {
		b, err := callView(method)
		if err != nil || len(b) == 0 {
			return common.Address{}, false
		}
		vals, err := parsed.Unpack(method, b)
		if err != nil || len(vals) < 1 {
			return common.Address{}, false
		}
		a, ok := vals[0].(common.Address)
		return a, ok
	}
	callUint := func(method string) (*big.Int, bool) {
		b, err := callView(method)
		if err != nil || len(b) == 0 {
			return nil, false
		}
		vals, err := parsed.Unpack(method, b)
		if err != nil || len(vals) < 1 {
			return nil, false
		}
		v, ok := vals[0].(*big.Int)
		return v, ok
	}

	if a, ok := callAddr("token0"); ok && a != (common.Address{}) {
		out.Token0 = a.Hex()
	}
	if a, ok := callAddr("token1"); ok && a != (common.Address{}) {
		out.Token1 = a.Hex()
	}
	if v, ok := callUint("fee"); ok && v != nil && v.Sign() >= 0 {
		out.Fee = uint32(v.Uint64())
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	fmt.Printf("chain_id=%d pool=%s code_present=%t sqrt_price_x96=%s tick=%d liquidity=%s",
		out.ChainID, out.Pool, out.CodePresent, out.SqrtPriceX96, out.Tick, out.Liquidity)
	if out.Token0 != "" {
		fmt.Printf(" token0=%s", out.Token0)
	}
	if out.Token1 != "" {
		fmt.Printf(" token1=%s", out.Token1)
	}
	if out.Fee != 0 {
		fmt.Printf(" fee=%d", out.Fee)
	}
	fmt.Println()
}
