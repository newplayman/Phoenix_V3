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
		rpcURL   = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID  = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		tokenStr = flag.String("token", "", "token address (0x...)")
		timeout  = flag.Duration("timeout", 20*time.Second, "RPC timeout")
		asJSON   = flag.Bool("json", false, "print JSON")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if !common.IsHexAddress(*tokenStr) {
		log.Fatalf("missing/invalid -token address: %q", *tokenStr)
	}
	token := common.HexToAddress(*tokenStr)

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

	erc20ABI, err := abi.JSON(strings.NewReader(`[
		{"inputs":[],"name":"name","outputs":[{"type":"string"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"symbol","outputs":[{"type":"string"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"decimals","outputs":[{"type":"uint8"}],"stateMutability":"view","type":"function"}
	]`))
	if err != nil {
		log.Fatalf("abi parse: %v", err)
	}

	call := func(method string) ([]byte, error) {
		data, err := erc20ABI.Pack(method)
		if err != nil {
			return nil, err
		}
		msg := ethereum.CallMsg{To: &token, Data: data}
		return client.CallContract(ctx, msg, nil)
	}

	type out struct {
		ChainID   int64  `json:"chain_id"`
		Token     string `json:"token"`
		CodeBytes int    `json:"code_bytes"`
		Name      string `json:"name,omitempty"`
		Symbol    string `json:"symbol,omitempty"`
		Decimals  int    `json:"decimals,omitempty"`
	}
	code, err := client.CodeAt(ctx, token, nil)
	if err != nil {
		log.Fatalf("codeAt: %v", err)
	}
	o := out{ChainID: *chainID, Token: token.Hex(), CodeBytes: len(code), Decimals: -1}

	if b, err := call("name"); err == nil && len(b) > 0 {
		if vals, err := erc20ABI.Unpack("name", b); err == nil && len(vals) == 1 {
			if s, ok := vals[0].(string); ok {
				o.Name = s
			}
		}
	}
	if b, err := call("symbol"); err == nil && len(b) > 0 {
		if vals, err := erc20ABI.Unpack("symbol", b); err == nil && len(vals) == 1 {
			if s, ok := vals[0].(string); ok {
				o.Symbol = s
			}
		}
	}
	if b, err := call("decimals"); err == nil && len(b) > 0 {
		if vals, err := erc20ABI.Unpack("decimals", b); err == nil && len(vals) == 1 {
			switch v := vals[0].(type) {
			case uint8:
				o.Decimals = int(v)
			case *big.Int:
				o.Decimals = int(v.Int64())
			}
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(o)
		return
	}

	fmt.Printf("chain_id=%d token=%s code_bytes=%d name=%q symbol=%q decimals=%d\n", o.ChainID, o.Token, o.CodeBytes, o.Name, o.Symbol, o.Decimals)
}
