package lplex

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestConvertPositionToSignalK(t *testing.T) {
	// PGN 129025 Position Rapid Update: lat=51.5, lon=-0.1
	// lat = 51.5 / 1e-7 = 515000000 = 0x1EB3B540 (little-endian)
	// lon = -0.1 / 1e-7 = -1000000 = 0xFFF0BDC0 (little-endian)
	lat := int32(515000000)
	lon := int32(-1000000)
	data := make([]byte, 8)
	data[0] = byte(lat)
	data[1] = byte(lat >> 8)
	data[2] = byte(lat >> 16)
	data[3] = byte(lat >> 24)
	data[4] = byte(lon)
	data[5] = byte(lon >> 8)
	data[6] = byte(lon >> 16)
	data[7] = byte(lon >> 24)

	ts := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	delta := ConvertToSignalK(129025, 10, ts, data)
	if delta == nil {
		t.Fatal("expected non-nil delta")
	}

	if len(delta.Updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(delta.Updates))
	}

	u := delta.Updates[0]
	if u.Source.PGN != 129025 {
		t.Errorf("source PGN = %d, want 129025", u.Source.PGN)
	}
	if u.Source.Src != "10" {
		t.Errorf("source src = %q, want %q", u.Source.Src, "10")
	}
	if u.Source.Type != "NMEA2000" {
		t.Errorf("source type = %q, want %q", u.Source.Type, "NMEA2000")
	}

	if len(u.Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(u.Values))
	}
	if u.Values[0].Path != "navigation.position" {
		t.Errorf("path = %q, want %q", u.Values[0].Path, "navigation.position")
	}

	pos, ok := u.Values[0].Value.(map[string]float64)
	if !ok {
		t.Fatalf("value type = %T, want map[string]float64", u.Values[0].Value)
	}
	if math.Abs(pos["latitude"]-51.5) > 0.001 {
		t.Errorf("latitude = %f, want ~51.5", pos["latitude"])
	}
	if math.Abs(pos["longitude"]+0.1) > 0.001 {
		t.Errorf("longitude = %f, want ~-0.1", pos["longitude"])
	}
}

func TestConvertToSignalKJSON(t *testing.T) {
	lat := int32(515000000)
	lon := int32(-1000000)
	data := make([]byte, 8)
	data[0] = byte(lat)
	data[1] = byte(lat >> 8)
	data[2] = byte(lat >> 16)
	data[3] = byte(lat >> 24)
	data[4] = byte(lon)
	data[5] = byte(lon >> 8)
	data[6] = byte(lon >> 16)
	data[7] = byte(lon >> 24)

	b, err := ConvertToSignalKJSON(129025, 10, time.Now(), data)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("expected non-nil JSON")
	}

	var delta SignalKDelta
	if err := json.Unmarshal(b, &delta); err != nil {
		t.Fatal(err)
	}
	if len(delta.Updates) == 0 {
		t.Error("expected updates in JSON")
	}
}

func TestConvertUnmappedPGNReturnsNil(t *testing.T) {
	delta := ConvertToSignalK(99999, 1, time.Now(), []byte{0, 0, 0, 0, 0, 0, 0, 0})
	if delta != nil {
		t.Error("expected nil for unmapped PGN")
	}
}

func TestHasSignalKMapping(t *testing.T) {
	if !HasSignalKMapping(129025) {
		t.Error("expected mapping for PGN 129025")
	}
	if HasSignalKMapping(99999) {
		t.Error("expected no mapping for PGN 99999")
	}
}
