package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var (
		rpcURL   = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID  = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		address  = flag.String("address", "", "wallet address (0x...)")
		timeout  = flag.Duration("timeout", 20*time.Second, "RPC timeout")
		minWei   = flag.String("min-wei", "", "optional min balance in wei (fail if below)")
		withJSON = flag.Bool("json", false, "print a simple JSON object")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if !common.IsHexAddress(strings.TrimSpace(*address)) {
		log.Fatal("missing/invalid -address (expected 0x...)")
	}

	var min *big.Int
	if strings.TrimSpace(*minWei) != "" {
		min = new(big.Int)
		if _, ok := min.SetString(strings.TrimSpace(*minWei), 10); !ok || min.Sign() < 0 {
			log.Fatalf("invalid -min-wei: %s", *minWei)
		}
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

	addr := common.HexToAddress(strings.TrimSpace(*address))
	bal, err := client.BalanceAt(ctx, addr, nil)
	if err != nil {
		log.Fatalf("balance: %v", err)
	}

	eth := new(big.Rat).SetFrac(bal, big.NewInt(1_000_000_000_000_000_000))

	if *withJSON {
		fmt.Printf("{\"chain_id\":%d,\"address\":\"%s\",\"balance_wei\":\"%s\",\"balance_eth\":\"%s\"}\n", *chainID, addr.Hex(), bal.String(), eth.FloatString(18))
	} else {
		fmt.Printf("chain_id=%d address=%s balance_wei=%s balance_eth=%s\n", *chainID, addr.Hex(), bal.String(), eth.FloatString(18))
	}

	if min != nil && bal.Cmp(min) < 0 {
		os.Exit(3)
	}
}
