package main

import (
	"fmt"
	"os"
	"strings"
)

func loadBotPrivateKey() (string, error) {
	if keyHex := strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY")); keyHex != "" {
		return keyHex, nil
	}
	keyFile := strings.TrimSpace(os.Getenv("BOT_PRIVATE_KEY_FILE"))
	if keyFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("read BOT_PRIVATE_KEY_FILE failed: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
