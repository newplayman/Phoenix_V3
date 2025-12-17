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

func formatUnits(amount *big.Int, decimals int) string {
	if amount == nil {
		return "0"
	}
	if decimals <= 0 {
		return amount.String()
	}
	if amount.Sign() == 0 {
		return "0"
	}
	neg := amount.Sign() < 0
	x := new(big.Int).Abs(amount)

	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	intPart := new(big.Int).Quo(x, base)
	frac := new(big.Int).Mod(x, base)
	fracStr := fmt.Sprintf("%0*s", decimals, frac.String())
	fracStr = strings.TrimRight(fracStr, "0")
	if fracStr == "" {
		if neg {
			return "-" + intPart.String()
		}
		return intPart.String()
	}
	if neg {
		return fmt.Sprintf("-%s.%s", intPart.String(), fracStr)
	}
	return fmt.Sprintf("%s.%s", intPart.String(), fracStr)
}

func main() {
	var (
		rpcURL     = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID    = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		tokenStr   = flag.String("token", "", "ERC20 token address (0x...)")
		ownerStr   = flag.String("owner", "", "owner address (0x...)")
		decimalsIn = flag.Int("decimals", -1, "override decimals (when token.decimals() is missing)")
		timeout    = flag.Duration("timeout", 20*time.Second, "RPC timeout")
		asJSON     = flag.Bool("json", false, "print JSON")
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
	if !common.IsHexAddress(*ownerStr) {
		log.Fatalf("missing/invalid -owner address: %q", *ownerStr)
	}
	token := common.HexToAddress(*tokenStr)
	owner := common.HexToAddress(*ownerStr)

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
		{"inputs":[{"internalType":"address","name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"type":"uint256"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"decimals","outputs":[{"type":"uint8"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"symbol","outputs":[{"type":"string"}],"stateMutability":"view","type":"function"}
	]`))
	if err != nil {
		log.Fatalf("abi parse: %v", err)
	}

	call := func(method string, args ...interface{}) ([]byte, error) {
		data, err := erc20ABI.Pack(method, args...)
		if err != nil {
			return nil, err
		}
		msg := ethereum.CallMsg{To: &token, Data: data}
		return client.CallContract(ctx, msg, nil)
	}

	type out struct {
		ChainID          int64  `json:"chain_id"`
		Token            string `json:"token"`
		Owner            string `json:"owner"`
		CodeBytes        int    `json:"code_bytes"`
		Symbol           string `json:"symbol,omitempty"`
		Decimals         int    `json:"decimals"`
		BalanceRaw       string `json:"balance_raw"`
		BalanceFormatted string `json:"balance_formatted"`
	}

	code, err := client.CodeAt(ctx, token, nil)
	if err != nil {
		log.Fatalf("codeAt: %v", err)
	}
	o := out{
		ChainID:   *chainID,
		Token:     token.Hex(),
		Owner:     owner.Hex(),
		CodeBytes: len(code),
		Decimals:  *decimalsIn,
	}

	if b, err := call("symbol"); err == nil && len(b) > 0 {
		if vals, err := erc20ABI.Unpack("symbol", b); err == nil && len(vals) == 1 {
			if s, ok := vals[0].(string); ok {
				o.Symbol = s
			}
		}
	}

	if o.Decimals < 0 {
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
	}

	balRaw := big.NewInt(0)
	if b, err := call("balanceOf", owner); err == nil && len(b) > 0 {
		if vals, err := erc20ABI.Unpack("balanceOf", b); err == nil && len(vals) == 1 {
			if v, ok := vals[0].(*big.Int); ok && v != nil {
				balRaw = v
			}
		}
	}
	o.BalanceRaw = balRaw.String()
	o.BalanceFormatted = formatUnits(balRaw, o.Decimals)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(o)
		return
	}

	fmt.Printf("chain_id=%d token=%s owner=%s code_bytes=%d symbol=%q decimals=%d balance_raw=%s balance=%s\n",
		o.ChainID, o.Token, o.Owner, o.CodeBytes, o.Symbol, o.Decimals, o.BalanceRaw, o.BalanceFormatted)
}
