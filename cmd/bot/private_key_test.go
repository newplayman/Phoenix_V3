package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBotPrivateKey_FileFallback(t *testing.T) {
	t.Setenv("BOT_PRIVATE_KEY", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(p, []byte("0xabc\n"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv("BOT_PRIVATE_KEY_FILE", p)

	got, err := loadBotPrivateKey()
	if err != nil {
		t.Fatalf("loadBotPrivateKey: %v", err)
	}
	if got != "0xabc" {
		t.Fatalf("unexpected key: %q", got)
	}
}
