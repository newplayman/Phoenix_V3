package main

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestInt24FromTopic_Positive(t *testing.T) {
	// +10 encoded as a 32-byte topic.
	var b [32]byte
	b[31] = 10
	h := common.BytesToHash(b[:])
	got, err := int24FromTopic(h)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 10 {
		t.Fatalf("got=%d want=10", got)
	}
}

func TestInt24FromTopic_NegativeOne(t *testing.T) {
	// -1 encoded as all 0xff.
	var b [32]byte
	for i := range b {
		b[i] = 0xff
	}
	h := common.BytesToHash(b[:])
	got, err := int24FromTopic(h)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != -1 {
		t.Fatalf("got=%d want=-1", got)
	}
}

func TestAddrFromTopic(t *testing.T) {
	addr := common.HexToAddress("0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217")
	var b [32]byte
	copy(b[12:], addr.Bytes())
	h := common.BytesToHash(b[:])
	got := addrFromTopic(h)
	if got != addr {
		t.Fatalf("got=%s want=%s", got.Hex(), addr.Hex())
	}
}
