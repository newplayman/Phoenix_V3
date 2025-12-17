package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	var (
		keyHex   = flag.String("key-hex", strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY")), "private key hex (BOT_PRIVATE_KEY, optional 0x prefix)")
		keyFile  = flag.String("key-file", strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY_FILE")), "private key file path (BOT_PRIVATE_KEY_FILE)")
		with0x   = flag.Bool("with-0x", true, "prefix output with 0x")
		quietOut = flag.Bool("quiet", false, "print only the address")
	)
	flag.Parse()

	privKeyHex := strings.TrimSpace(*keyHex)
	if privKeyHex == "" && strings.TrimSpace(*keyFile) != "" {
		b, err := os.ReadFile(strings.TrimSpace(*keyFile))
		if err != nil {
			log.Fatalf("read key file failed: %v", err)
		}
		privKeyHex = strings.TrimSpace(string(b))
	}
	if privKeyHex == "" {
		log.Fatal("missing key (set -key-hex / BOT_PRIVATE_KEY or -key-file / BOT_PRIVATE_KEY_FILE)")
	}
	privKeyHex = strings.TrimPrefix(privKeyHex, "0x")
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil || len(privKeyBytes) != 32 {
		log.Fatal("invalid key: expected 32-byte hex (optionally prefixed with 0x)")
	}
	privKey, err := crypto.ToECDSA(privKeyBytes)
	if err != nil {
		log.Fatalf("invalid key: %v", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	if !*with0x {
		addr = strings.TrimPrefix(addr, "0x")
	}
	if *quietOut {
		fmt.Println(addr)
		return
	}
	fmt.Printf("address=%s\n", addr)
}
