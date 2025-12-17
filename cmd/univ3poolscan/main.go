package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var (
		rpcURL      = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID     = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		lookback    = flag.Uint64("lookback", 5000, "how many latest blocks to scan for UniV3 Initialize events")
		timeout     = flag.Duration("timeout", 25*time.Second, "RPC timeout")
		maxResults  = flag.Int("max-results", 2000, "max log results before aborting (safety)")
		verifySlot0 = flag.Bool("verify-slot0", true, "verify each candidate by eth_call slot0() (filters false positives)")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if *lookback == 0 {
		log.Fatal("lookback must be > 0")
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

	latest, err := client.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("blockNumber: %v", err)
	}
	var from uint64
	if latest > *lookback {
		from = latest - *lookback
	}

	// UniswapV3Pool event: Initialize(uint160,int24)
	initSig := crypto.Keccak256Hash([]byte("Initialize(uint160,int24)"))
	q := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(latest),
		Topics:    [][]common.Hash{{initSig}},
	}
	logs, err := client.FilterLogs(ctx, q)
	if err != nil {
		log.Fatalf("filterLogs failed (from=%d to=%d): %v", from, latest, err)
	}
	if *maxResults > 0 && len(logs) > *maxResults {
		log.Fatalf("too many logs (%d) for safety; reduce -lookback or increase -max-results", len(logs))
	}

	addrs := map[string]common.Address{}
	for _, lg := range logs {
		addrs[strings.ToLower(lg.Address.Hex())] = lg.Address
	}

	slot0Selector := common.Hex2Bytes("3850c7bd") // keccak256("slot0()")[0:4]
	isLikelyPool := func(a common.Address) bool {
		if !*verifySlot0 {
			return true
		}
		callCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		msg := ethereum.CallMsg{To: &a, Data: slot0Selector}
		b, err := client.CallContract(callCtx, msg, nil)
		// slot0() returns 7 values => 7*32 bytes (224).
		return err == nil && len(b) >= 224
	}

	out := make([]string, 0, len(addrs))
	for k, a := range addrs {
		_ = k
		if !isLikelyPool(a) {
			continue
		}
		out = append(out, strings.ToLower(a.Hex()))
	}
	sort.Strings(out)

	fmt.Printf("chain_id=%d from_block=%d to_block=%d initialize_logs=%d unique_candidates=%d verified_pools=%d\n",
		*chainID, from, latest, len(logs), len(addrs), len(out))
	for _, a := range out {
		fmt.Println(a)
	}
}
