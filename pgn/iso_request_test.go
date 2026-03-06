package pgn

import (
	"encoding/hex"
	"testing"
)

func TestDecodeISORequest(t *testing.T) {
	// Real frame: requesting PGN 60928 (ISO Address Claim).
	raw, _ := hex.DecodeString("00ee00")
	m, err := DecodeISORequest(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.RequestedPGN != 60928 {
		t.Errorf("RequestedPGN = %d, want 60928", m.RequestedPGN)
	}
}

func TestDecodeISORequestTooShort(t *testing.T) {
	_, err := DecodeISORequest([]byte{0x00})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestISORequestRegistry(t *testing.T) {
	info, ok := Registry[59904]
	if !ok {
		t.Fatal("PGN 59904 not in registry")
	}
	if info.Description != "ISO Request" {
		t.Errorf("description = %q", info.Description)
	}
}
