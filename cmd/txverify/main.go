package main

import (
	"context"
	"encoding/hex"
	"errors"
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
)

func main() {
	var (
		rpcURL  = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		hashStr = flag.String("hash", "", "tx hash (0x...)")
		timeout = flag.Duration("timeout", 20*time.Second, "RPC timeout")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if !isHexHash(strings.TrimSpace(*hashStr)) {
		log.Fatal("missing/invalid -hash (expected 0x...)")
	}
	hash := common.HexToHash(strings.TrimSpace(*hashStr))

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

	receipt, err := client.TransactionReceipt(ctx, hash)
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			fmt.Printf("status=pending chain_id=%d hash=%s explorer=https://sepolia.arbiscan.io/tx/%s\n", *chainID, hash.Hex(), hash.Hex())
			return
		}
		log.Fatalf("receipt: %v", err)
	}

	txStatus := "failed"
	if receipt.Status == 1 {
		txStatus = "success"
	}
	fmt.Printf("status=mined chain_id=%d hash=%s block=%d tx_status=%s gas_used=%d explorer=https://sepolia.arbiscan.io/tx/%s\n",
		*chainID, hash.Hex(), receipt.BlockNumber.Uint64(), txStatus, receipt.GasUsed, hash.Hex())

	if receipt.Status != 1 {
		os.Exit(3)
	}
}

func isHexHash(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}
