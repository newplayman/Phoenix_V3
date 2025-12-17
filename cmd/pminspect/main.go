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
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"phoenix-v3/internal/chain/univ3"
)

func main() {
	var (
		rpcURL  = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		pmStr   = flag.String("pm", "", "position manager address (0x...)")
		tokenID = flag.String("token-id", "1", "token id to query via positions(tokenId)")
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
	if !common.IsHexAddress(*pmStr) {
		log.Fatalf("missing/invalid -pm address: %q", *pmStr)
	}
	pm := common.HexToAddress(*pmStr)

	tid := new(big.Int)
	if _, ok := tid.SetString(strings.TrimSpace(*tokenID), 10); !ok || tid.Sign() < 0 {
		log.Fatalf("invalid -token-id: %q", *tokenID)
	}

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

	code, err := client.CodeAt(ctx, pm, nil)
	if err != nil {
		log.Fatalf("codeAt: %v", err)
	}

	adapter := univ3.NewAdapter(pm.Hex())
	data, err := adapter.ParsedABI.Pack("positions", tid)
	if err != nil {
		log.Fatalf("pack positions: %v", err)
	}
	msg := ethereum.CallMsg{To: &pm, Data: data}
	res, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		log.Fatalf("positions call failed: %v", err)
	}
	decoded, err := adapter.ParsedABI.Unpack("positions", res)
	if err != nil {
		log.Fatalf("positions decode failed: %v", err)
	}
	if len(decoded) < 8 {
		log.Fatalf("positions unexpected result length=%d", len(decoded))
	}

	type out struct {
		ChainID   int64  `json:"chain_id"`
		PM        string `json:"pm"`
		CodeBytes int    `json:"code_bytes"`
		TokenID   string `json:"token_id"`
		Operator  string `json:"operator"`
		Token0    string `json:"token0"`
		Token1    string `json:"token1"`
		Fee       uint32 `json:"fee"`
		TickLower int64  `json:"tick_lower"`
		TickUpper int64  `json:"tick_upper"`
		Liquidity string `json:"liquidity"`
	}

	o := out{
		ChainID:   *chainID,
		PM:        pm.Hex(),
		CodeBytes: len(code),
		TokenID:   tid.String(),
	}
	if v, ok := decoded[1].(common.Address); ok {
		o.Operator = v.Hex()
	}
	if v, ok := decoded[2].(common.Address); ok {
		o.Token0 = v.Hex()
	}
	if v, ok := decoded[3].(common.Address); ok {
		o.Token1 = v.Hex()
	}
	// fee (uint24) arrives as *big.Int or uint32 depending on ABI
	switch v := decoded[4].(type) {
	case *big.Int:
		o.Fee = uint32(v.Uint64())
	case uint32:
		o.Fee = v
	case uint64:
		o.Fee = uint32(v)
	}
	switch v := decoded[5].(type) {
	case *big.Int:
		o.TickLower = v.Int64()
	case int64:
		o.TickLower = v
	case int32:
		o.TickLower = int64(v)
	}
	switch v := decoded[6].(type) {
	case *big.Int:
		o.TickUpper = v.Int64()
	case int64:
		o.TickUpper = v
	case int32:
		o.TickUpper = int64(v)
	}
	switch v := decoded[7].(type) {
	case *big.Int:
		o.Liquidity = v.String()
	case uint64:
		o.Liquidity = new(big.Int).SetUint64(v).String()
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(o)
		return
	}

	fmt.Printf("chain_id=%d pm=%s code_bytes=%d token_id=%s token0=%s token1=%s fee=%d ticks=[%d,%d] liquidity=%s operator=%s\n",
		o.ChainID, o.PM, o.CodeBytes, o.TokenID, o.Token0, o.Token1, o.Fee, o.TickLower, o.TickUpper, o.Liquidity, o.Operator)
}
