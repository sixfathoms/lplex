package lplex

import (
	"encoding/json"
	"testing"
)

func TestInjectDecoded(t *testing.T) {
	// PGN 129025 (Position Rapid Update): lat=51.5°, lon=-0.1°
	// Build a frame JSON with known data
	frame := frameJSON{
		Seq:  1,
		Ts:   "2026-03-22T12:00:00Z",
		Prio: 2,
		PGN:  129025,
		Src:  10,
		Dst:  255,
		Data: "00b5ac1e00f0bdc0", // lat=51.5, lon=-0.1 encoded
	}
	input, _ := json.Marshal(frame)

	result := injectDecoded(input)

	// Should contain "decoded" field
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal(err)
	}

	decoded, ok := out["decoded"]
	if !ok {
		t.Fatal("expected decoded field in output")
	}
	if decoded == nil {
		t.Fatal("decoded should not be nil for PGN 129025")
	}

	// Original fields should still be present
	if out["pgn"].(float64) != 129025 {
		t.Errorf("pgn = %v, want 129025", out["pgn"])
	}
	if out["data"] != "00b5ac1e00f0bdc0" {
		t.Errorf("data should be preserved")
	}
}

func TestInjectDecodedUnknownPGN(t *testing.T) {
	frame := frameJSON{
		Seq:  1,
		Ts:   "2026-03-22T12:00:00Z",
		Prio: 2,
		PGN:  99999, // Unknown PGN
		Src:  10,
		Dst:  255,
		Data: "0102030405060708",
	}
	input, _ := json.Marshal(frame)

	result := injectDecoded(input)

	// Should return original data unchanged (no decoded field)
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal(err)
	}

	if _, ok := out["decoded"]; ok {
		t.Error("should not have decoded field for unknown PGN")
	}
}

func TestInjectDecodedInvalidJSON(t *testing.T) {
	input := []byte("not json")
	result := injectDecoded(input)

	// Should return original data unchanged
	if string(result) != "not json" {
		t.Error("should return original data for invalid JSON")
	}
}
