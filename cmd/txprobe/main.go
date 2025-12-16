package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var (
		rpcURL   = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID  = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		toStr    = flag.String("to", "", "recipient address (default: self)")
		valueWei = flag.String("value-wei", "0", "value in wei (default: 0)")
		dataHex  = flag.String("data-hex", "", "optional calldata hex (without 0x)")
		timeout  = flag.Duration("timeout", 20*time.Second, "RPC timeout")
		maxRetry = flag.Int("max-retry", 2, "max send retries on underpriced/replacement errors")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}

	effectiveDryRun := envBoolDefaultTrue("TXPROBE_DRY_RUN") || envBoolDefaultTrue("TXPROBE_KILL_SWITCH") || !envBoolDefaultFalse("TXPROBE_ALLOW_BROADCAST")
	if effectiveDryRun {
		fmt.Println("status=simulated effective_dry_run=true (set TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true to broadcast)")
		return
	}
	if strings.TrimSpace(os.Getenv("TXPROBE_CONFIRM")) != "I_UNDERSTAND_GAS_COSTS" {
		log.Fatal("missing TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS")
	}

	privKeyHex := strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY"))
	if privKeyHex == "" {
		keyFile := strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY_FILE"))
		if keyFile != "" {
			b, err := os.ReadFile(keyFile)
			if err != nil {
				log.Fatalf("read BOT_PRIVATE_KEY_FILE failed: %v", err)
			}
			privKeyHex = strings.TrimSpace(string(b))
		}
	}
	if privKeyHex == "" {
		log.Fatal("missing BOT_PRIVATE_KEY (or BOT_PRIVATE_KEY_FILE)")
	}
	privKeyHex = strings.TrimPrefix(privKeyHex, "0x")
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil || len(privKeyBytes) != 32 {
		log.Fatal("invalid BOT_PRIVATE_KEY: expected 32-byte hex (optionally prefixed with 0x)")
	}
	privKey, err := crypto.ToECDSA(privKeyBytes)
	if err != nil {
		log.Fatalf("invalid BOT_PRIVATE_KEY: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey)

	to := from
	if strings.TrimSpace(*toStr) != "" {
		if !common.IsHexAddress(*toStr) {
			log.Fatalf("invalid -to address: %s", *toStr)
		}
		to = common.HexToAddress(*toStr)
	}

	val := new(big.Int)
	if _, ok := val.SetString(strings.TrimSpace(*valueWei), 10); !ok || val.Sign() < 0 {
		log.Fatalf("invalid -value-wei: %s", *valueWei)
	}

	var data []byte
	if strings.TrimSpace(*dataHex) != "" {
		b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(*dataHex), "0x"))
		if err != nil {
			log.Fatalf("invalid -data-hex: %v", err)
		}
		data = b
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

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("pending nonce: %v", err)
	}

	msg := ethereum.CallMsg{From: from, To: &to, Value: val, Data: data}
	gasLimit, err := client.EstimateGas(ctx, msg)
	if err != nil {
		// fallback for simple transfers
		gasLimit = 21000
	}

	sendTx := func(ctx context.Context, tipCap, feeCap *big.Int) (*types.Transaction, error) {
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   big.NewInt(*chainID),
			Nonce:     nonce,
			To:        &to,
			Value:     val,
			Data:      data,
			Gas:       gasLimit,
			GasTipCap: tipCap,
			GasFeeCap: feeCap,
		})
		signed, err := types.SignTx(tx, types.NewLondonSigner(big.NewInt(*chainID)), privKey)
		if err != nil {
			return nil, err
		}
		if err := client.SendTransaction(ctx, signed); err != nil {
			return nil, err
		}
		return signed, nil
	}

	baseFee, _ := client.SuggestGasPrice(ctx)
	tip, tipErr := client.SuggestGasTipCap(ctx)
	if tipErr != nil || tip == nil || tip.Sign() <= 0 {
		tip = big.NewInt(1_000_000_000) // 1 gwei conservative default
	}
	if baseFee == nil || baseFee.Sign() <= 0 {
		baseFee = big.NewInt(0)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	var lastErr error
	for attempt := 0; attempt <= *maxRetry; attempt++ {
		tx, err := sendTx(ctx, tip, feeCap)
		if err == nil {
			fmt.Printf("status=sent chain_id=%d from=%s to=%s nonce=%d gas=%d hash=%s explorer=https://sepolia.arbiscan.io/tx/%s\n",
				*chainID, from.Hex(), to.Hex(), nonce, gasLimit, tx.Hash().Hex(), tx.Hash().Hex())
			return
		}
		lastErr = err
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "underpriced") || strings.Contains(msg, "replacement transaction underpriced") || strings.Contains(msg, "fee cap too low") {
			// bump caps and retry
			tip = new(big.Int).Add(tip, new(big.Int).Div(tip, big.NewInt(2)))          // +50%
			feeCap = new(big.Int).Add(feeCap, new(big.Int).Div(feeCap, big.NewInt(2))) // +50%
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		break
	}
	log.Fatalf("send failed: %v", lastErr)
}

func envBoolDefaultTrue(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return true
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envBoolDefaultFalse(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return false
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
