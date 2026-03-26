package pgn

import (
	"encoding/json"
	"testing"
)

func TestBinarySwitchBankRoundTrip(t *testing.T) {
	orig := BinarySwitchBankStatus{
		Instance: ptr[uint8](1),
		Indicators: []uint8{
			1, 2, 3, 0, // indicators 1-4 (packed into byte 1)
			1, 1, 2, 2, // indicators 5-8 (packed into byte 2)
			0, 0, 0, 0, // indicators 9-12
			3, 3, 3, 3, // indicators 13-16
			0, 1, 2, 3, // indicators 17-20
			0, 0, 0, 0, // indicators 21-24
			1, 1, 1, 1, // indicators 25-28
		},
	}
	data := orig.Encode()
	if len(data) != 8 {
		t.Fatalf("encoded length = %d, want 8", len(data))
	}

	decoded, err := DecodeBinarySwitchBankStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Instance == nil || *decoded.Instance != 1 {
		t.Errorf("instance = %v, want 1", decoded.Instance)
	}
	if len(decoded.Indicators) != 28 {
		t.Fatalf("len(indicators) = %d, want 28", len(decoded.Indicators))
	}
	for i, want := range orig.Indicators {
		if decoded.Indicators[i] != want {
			t.Errorf("indicator[%d] = %d, want %d", i, decoded.Indicators[i], want)
		}
	}
}

func TestBinarySwitchBankDecodeKnownBytes(t *testing.T) {
	// Hand-crafted: instance=0, all indicators set to specific bit patterns.
	// Byte 0: instance = 0x00
	// Byte 1: indicators 1-4, each 2 bits: 01 10 11 00 = 0b00_11_10_01 = 0xE1
	//   indicator 1 = 01 (1), indicator 2 = 10 (2), indicator 3 = 11 (3), indicator 4 = 00 (0)
	//   Wait, little-endian bit packing: indicator_1 is bits [1:0], indicator_2 is bits [3:2], etc.
	//   So byte = (ind4 << 6) | (ind3 << 4) | (ind2 << 2) | ind1
	//   ind1=1, ind2=2, ind3=3, ind4=0 -> (0<<6)|(3<<4)|(2<<2)|1 = 0x00|0x30|0x08|0x01 = 0x39
	// Bytes 2-7: all 0xFF (all indicators = 3, the "not available" sentinel)
	data := []byte{0x00, 0x39, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	decoded, err := DecodeBinarySwitchBankStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Instance == nil || *decoded.Instance != 0 {
		t.Errorf("instance = %v, want 0", decoded.Instance)
	}

	// First 4 indicators from byte 1 = 0x39
	want := []uint8{1, 2, 3, 0}
	for i, w := range want {
		if decoded.Indicators[i] != w {
			t.Errorf("indicator[%d] = %d, want %d", i, decoded.Indicators[i], w)
		}
	}
	// Remaining 24 indicators from bytes 2-7 (all 0xFF) should all be 3
	for i := 4; i < 28; i++ {
		if decoded.Indicators[i] != 3 {
			t.Errorf("indicator[%d] = %d, want 3 (from 0xFF bytes)", i, decoded.Indicators[i])
		}
	}
}

func TestBinarySwitchBankPartialEncode(t *testing.T) {
	// Encode with fewer than 28 indicators. Unset indicators should stay 0xFF (3).
	orig := BinarySwitchBankStatus{
		Instance:   ptr[uint8](5),
		Indicators: []uint8{1, 0}, // only first two
	}
	data := orig.Encode()

	decoded, err := DecodeBinarySwitchBankStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Instance == nil || *decoded.Instance != 5 {
		t.Errorf("instance = %v, want 5", decoded.Instance)
	}
	if decoded.Indicators[0] != 1 {
		t.Errorf("indicator[0] = %d, want 1", decoded.Indicators[0])
	}
	if decoded.Indicators[1] != 0 {
		t.Errorf("indicator[1] = %d, want 0", decoded.Indicators[1])
	}
	// Indicators 3-28 were not set, buffer was pre-filled with 0xFF, so they should be 3
	for i := 2; i < 28; i++ {
		if decoded.Indicators[i] != 3 {
			t.Errorf("indicator[%d] = %d, want 3 (unset)", i, decoded.Indicators[i])
		}
	}
}

func TestBinarySwitchBankShortData(t *testing.T) {
	// Short data should be padded with 0xFF, not error.
	decoded, err := DecodeBinarySwitchBankStatus([]byte{0x02})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Instance == nil || *decoded.Instance != 2 {
		t.Errorf("instance = %v, want 2", decoded.Instance)
	}
	// All indicators from padded 0xFF bytes should be 3.
	for i := 0; i < 28; i++ {
		if decoded.Indicators[i] != 3 {
			t.Errorf("indicator[%d] = %d, want 3 (padded)", i, decoded.Indicators[i])
		}
	}
}

func TestBinarySwitchBankRegistry(t *testing.T) {
	info, ok := Registry[127501]
	if !ok {
		t.Fatal("PGN 127501 not in registry")
	}
	if info.Description != "Binary Switch Bank Status" {
		t.Errorf("description = %q", info.Description)
	}

	// Decode through registry
	data := []byte{0x03, 0x39, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	v, err := info.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	sw, ok := v.(BinarySwitchBankStatus)
	if !ok {
		t.Fatalf("expected BinarySwitchBankStatus, got %T", v)
	}
	if sw.Instance == nil || *sw.Instance != 3 {
		t.Errorf("instance = %v, want 3", sw.Instance)
	}
	if sw.Indicators[0] != 1 {
		t.Errorf("indicator[0] = %d, want 1", sw.Indicators[0])
	}
}

func TestBinarySwitchBankPGN(t *testing.T) {
	var sw BinarySwitchBankStatus
	if sw.PGN() != 127501 {
		t.Errorf("PGN() = %d, want 127501", sw.PGN())
	}
}

func TestBinarySwitchBankControlRoundTrip(t *testing.T) {
	// Build a control frame: turn switch 1 ON, switch 3 OFF, rest no-change.
	ctrl := BinarySwitchBankControl{
		Instance: ptr[uint8](0),
	}
	ctrl.Indicators = make(Uint8s, 28)
	for i := range ctrl.Indicators {
		ctrl.Indicators[i] = 3 // no change
	}
	ctrl.Indicators[0] = 1 // switch 1 = ON
	ctrl.Indicators[2] = 0 // switch 3 = OFF

	data := ctrl.Encode()
	if len(data) != 8 {
		t.Fatalf("encoded length = %d, want 8", len(data))
	}

	// Instance byte.
	if data[0] != 0 {
		t.Errorf("instance byte = 0x%02X, want 0x00", data[0])
	}

	// Byte 1: ind1=1(ON), ind2=3(no-change), ind3=0(OFF), ind4=3(no-change)
	// = (3<<6)|(0<<4)|(3<<2)|1 = 0xC0|0x00|0x0C|0x01 = 0xCD
	if data[1] != 0xCD {
		t.Errorf("byte 1 = 0x%02X, want 0xCD", data[1])
	}

	// Bytes 2-7: all no-change = 0xFF.
	for i := 2; i < 8; i++ {
		if data[i] != 0xFF {
			t.Errorf("byte %d = 0x%02X, want 0xFF", i, data[i])
		}
	}

	// Decode back through the registry.
	decoded, err := DecodeBinarySwitchBankControl(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Instance == nil || *decoded.Instance != 0 {
		t.Errorf("instance = %v, want 0", decoded.Instance)
	}
	if decoded.Indicators[0] != 1 {
		t.Errorf("indicator[0] = %d, want 1 (ON)", decoded.Indicators[0])
	}
	if decoded.Indicators[1] != 3 {
		t.Errorf("indicator[1] = %d, want 3 (no change)", decoded.Indicators[1])
	}
	if decoded.Indicators[2] != 0 {
		t.Errorf("indicator[2] = %d, want 0 (OFF)", decoded.Indicators[2])
	}
}

func TestBinarySwitchBankControlRegistry(t *testing.T) {
	info, ok := Registry[127502]
	if !ok {
		t.Fatal("PGN 127502 not in registry")
	}
	if info.Description != "Binary Switch Bank Control" {
		t.Errorf("description = %q", info.Description)
	}
}

func TestBinarySwitchBankJSON(t *testing.T) {
	sw := BinarySwitchBankStatus{
		Instance:   ptr[uint8](1),
		Indicators: Uint8s{1, 2, 3, 0},
	}
	data, err := json.Marshal(sw)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	// Must be a JSON integer array, not base64.
	want := `{"instance":1,"indicators":[1,2,3,0]}`
	if got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}

	// Round-trip through unmarshal.
	var decoded BinarySwitchBankStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Indicators) != 4 {
		t.Fatalf("unmarshaled len = %d, want 4", len(decoded.Indicators))
	}
	for i, want := range sw.Indicators {
		if decoded.Indicators[i] != want {
			t.Errorf("indicator[%d] = %d, want %d", i, decoded.Indicators[i], want)
		}
	}
}
