package pgn

import (
	"encoding/hex"
	"math"
	"testing"
)

func TestDecodeUtilityPhaseAACPower(t *testing.T) {
	// Real frame: 889W real, 902VA apparent.
	raw, _ := hex.DecodeString("7997357786973577")
	m, err := decodeACPower(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.RealPower != 889 {
		t.Errorf("RealPower = %v, want 889", m.RealPower)
	}
	if m.ApparentPower != 902 {
		t.Errorf("ApparentPower = %v, want 902", m.ApparentPower)
	}
}

func TestDecodeUtilityPhaseBACPower(t *testing.T) {
	// Real frame: 0W real, 0VA apparent (unused phase).
	raw, _ := hex.DecodeString("0094357700943577")
	m, err := decodeACPower(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.RealPower != 0 {
		t.Errorf("RealPower = %v, want 0", m.RealPower)
	}
	if m.ApparentPower != 0 {
		t.Errorf("ApparentPower = %v, want 0", m.ApparentPower)
	}
}

func TestDecodeUtilityPhaseABasicACQuantities(t *testing.T) {
	// Real frame: L-L=N/A, L-N=125V, 60.09Hz, 7A.
	raw, _ := hex.DecodeString("ffff7d000b1e0700")
	m, err := decodeACBasicQuantities(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.LineLineVoltage != 65535 { // 0xFFFF = not available
		t.Errorf("LineLineVoltage = %v, want 65535", m.LineLineVoltage)
	}
	if m.LineNeutralVoltage != 125 {
		t.Errorf("LineNeutralVoltage = %v, want 125", m.LineNeutralVoltage)
	}
	wantFreq := float64(0x1e0b) / 128.0
	if math.Abs(m.ACFrequency-wantFreq) > 0.001 {
		t.Errorf("ACFrequency = %v, want ~%v", m.ACFrequency, wantFreq)
	}
	if m.ACRMSCurrent != 7 {
		t.Errorf("ACRMSCurrent = %v, want 7", m.ACRMSCurrent)
	}
}

func TestDecodeUtilityPhaseBBasicACQuantities(t *testing.T) {
	// Real frame: L-L=N/A, L-N=125V, 60.09Hz, current=N/A.
	raw, _ := hex.DecodeString("ffff7d000b1effff")
	m, err := decodeACBasicQuantities(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.LineNeutralVoltage != 125 {
		t.Errorf("LineNeutralVoltage = %v, want 125", m.LineNeutralVoltage)
	}
	if m.ACRMSCurrent != 65535 { // 0xFFFF = not available
		t.Errorf("ACRMSCurrent = %v, want 65535 (not available)", m.ACRMSCurrent)
	}
}

func TestACUtilityPGNsInRegistry(t *testing.T) {
	for _, pgn := range []uint32{65010, 65011, 65013, 65014, 65016} {
		if _, ok := Registry[pgn]; !ok {
			t.Errorf("PGN %d not in registry", pgn)
		}
	}
}
