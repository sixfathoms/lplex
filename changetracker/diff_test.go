package changetracker

import (
	"bytes"
	"math"
	"testing"

	"github.com/sixfathoms/lplex/pgn"
)

func ptr[T any](v T) *T { return &v }

func TestByteMaskDiff_Identical(t *testing.T) {
	d := ByteMaskDiff{}
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	sig, diff := d.Diff(data, data)
	if sig || diff != nil {
		t.Fatalf("identical packets should not be significant, got sig=%v diff=%v", sig, diff)
	}
}

func TestByteMaskDiff_SingleByteChange(t *testing.T) {
	d := ByteMaskDiff{}
	prev := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	curr := []byte{0x01, 0xFF, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("single byte change should be significant")
	}

	// Short format: mask=0x02 (bit 1), changed=[0xFF]
	if diff[0] != 0x02 {
		t.Fatalf("expected mask 0x02, got 0x%02x", diff[0])
	}
	if len(diff) != 2 || diff[1] != 0xFF {
		t.Fatalf("expected [0x02, 0xFF], got %x", diff)
	}
}

func TestByteMaskDiff_MultipleChanges(t *testing.T) {
	d := ByteMaskDiff{}
	prev := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	curr := []byte{0xAA, 0x02, 0xBB, 0x04, 0x05, 0x06, 0x07, 0xCC}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}

	// mask: bits 0, 2, 7 => 0b10000101 = 0x85
	if diff[0] != 0x85 {
		t.Fatalf("expected mask 0x85, got 0x%02x", diff[0])
	}
	if !bytes.Equal(diff[1:], []byte{0xAA, 0xBB, 0xCC}) {
		t.Fatalf("expected changed [AA BB CC], got %x", diff[1:])
	}
}

func TestByteMaskDiff_AllChanged(t *testing.T) {
	d := ByteMaskDiff{}
	prev := make([]byte, 8)
	curr := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}
	if diff[0] != 0xFF {
		t.Fatalf("expected mask 0xFF, got 0x%02x", diff[0])
	}
	if !bytes.Equal(diff[1:], curr) {
		t.Fatalf("expected all bytes in diff")
	}
}

func TestByteMaskDiff_RoundTrip(t *testing.T) {
	d := ByteMaskDiff{}
	prev := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	curr := []byte{0x01, 0xFF, 0x03, 0xEE, 0x05, 0x06, 0xDD, 0x08}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}

	applied := d.Apply(prev, diff)
	if !bytes.Equal(applied, curr) {
		t.Fatalf("round-trip failed: got %x, want %x", applied, curr)
	}
}

func TestByteMaskDiff_ShortPacket(t *testing.T) {
	d := ByteMaskDiff{}
	prev := []byte{0x01, 0x02, 0x03}
	curr := []byte{0x01, 0xFF, 0x03}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}

	// Short format (3 bytes <= 8), mask=0x02
	if diff[0] != 0x02 {
		t.Fatalf("expected mask 0x02, got 0x%02x", diff[0])
	}

	applied := d.Apply(prev, diff)
	if !bytes.Equal(applied, curr) {
		t.Fatalf("round-trip failed: got %x, want %x", applied, curr)
	}
}

func TestByteMaskDiff_Extended(t *testing.T) {
	d := ByteMaskDiff{}
	// 20-byte packet (fast packet territory)
	prev := make([]byte, 20)
	curr := make([]byte, 20)
	copy(curr, prev)
	curr[0] = 0xAA   // byte 0
	curr[9] = 0xBB   // byte 9 (past first mask byte)
	curr[19] = 0xCC  // byte 19

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}

	// Extended format: maskLen=3 (ceil(20/8))
	maskLen := int(diff[0]) | int(diff[1])<<8
	if maskLen != 3 {
		t.Fatalf("expected maskLen=3, got %d", maskLen)
	}
	if len(diff) != 2+3+3 {
		t.Fatalf("expected diff len 8, got %d", len(diff))
	}
}

func TestByteMaskDiff_ExtendedRoundTrip(t *testing.T) {
	d := ByteMaskDiff{}
	prev := make([]byte, 50)
	curr := make([]byte, 50)
	for i := range prev {
		prev[i] = byte(i)
		curr[i] = byte(i)
	}
	// Change a few scattered bytes.
	curr[0] = 0xFF
	curr[15] = 0xFF
	curr[31] = 0xFF
	curr[49] = 0xFF

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("expected significant")
	}

	applied := d.Apply(prev, diff)
	if !bytes.Equal(applied, curr) {
		t.Fatalf("extended round-trip failed:\ngot  %x\nwant %x", applied, curr)
	}
}

func TestByteMaskDiff_LengthMismatch(t *testing.T) {
	d := ByteMaskDiff{}
	sig, diff := d.Diff([]byte{1, 2, 3}, []byte{1, 2, 3, 4})
	if !sig {
		t.Fatal("length change should be significant")
	}
	if diff != nil {
		t.Fatal("length change should return nil diff (caller emits Snapshot)")
	}
}

func TestFieldToleranceDiff_WithinTolerance(t *testing.T) {
	// Build two wind data packets with a tiny heading change within tolerance.
	w1 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0000), WindReference: ptr(pgn.WindReferenceApparent)}
	w2 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0001), WindReference: ptr(pgn.WindReferenceApparent)}

	d := &FieldToleranceDiff{
		PGN: 130306,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeWindData(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "wind_angle", Tolerance: 0.001},
			{Field: "wind_speed", Tolerance: 0.1},
		},
	}

	sig, _ := d.Diff(w1.Encode(), w2.Encode())
	if sig {
		t.Fatal("change within tolerance should be suppressed")
	}
}

func TestFieldToleranceDiff_ExceedsTolerance(t *testing.T) {
	w1 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0), WindReference: ptr(pgn.WindReferenceApparent)}
	w2 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.5), WindReference: ptr(pgn.WindReferenceApparent)}

	d := &FieldToleranceDiff{
		PGN: 130306,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeWindData(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "wind_angle", Tolerance: 0.001},
		},
	}

	sig, diff := d.Diff(w1.Encode(), w2.Encode())
	if !sig {
		t.Fatal("change exceeding tolerance should be significant")
	}

	// Should produce a valid ByteMaskDiff.
	applied := d.Apply(w1.Encode(), diff)
	if !bytes.Equal(applied, w2.Encode()) {
		t.Fatalf("round-trip failed: got %x, want %x", applied, w2.Encode())
	}
}

func TestFieldToleranceDiff_MixedFields(t *testing.T) {
	// WindSpeed changes significantly, WindAngle within tolerance.
	w1 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0), WindReference: ptr(pgn.WindReferenceApparent)}
	w2 := &pgn.WindData{WindSpeed: ptr(10.0), WindAngle: ptr(1.0001), WindReference: ptr(pgn.WindReferenceApparent)}

	d := &FieldToleranceDiff{
		PGN: 130306,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeWindData(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "wind_angle", Tolerance: 0.001},
			{Field: "wind_speed", Tolerance: 0.1},
		},
	}

	sig, _ := d.Diff(w1.Encode(), w2.Encode())
	if !sig {
		t.Fatal("should be significant when any field exceeds tolerance")
	}
}

func TestFieldToleranceDiff_NoDecodeFunc(t *testing.T) {
	// Without a decode function, falls back to ByteMaskDiff.
	d := &FieldToleranceDiff{
		PGN:        99999,
		Decode:     nil,
		Tolerances: []FieldTolerance{{Field: "x", Tolerance: 100}},
	}

	prev := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	curr := []byte{0x01, 0xFF, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	sig, diff := d.Diff(prev, curr)
	if !sig {
		t.Fatal("should fall back to ByteMaskDiff and be significant")
	}

	applied := d.Apply(prev, diff)
	if !bytes.Equal(applied, curr) {
		t.Fatalf("round-trip failed")
	}
}

func TestFieldToleranceDiff_NonNumericFieldChange(t *testing.T) {
	// WindReference is an enum. With tolerance=0, any change is significant.
	w1 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0), WindReference: ptr(pgn.WindReferenceApparent)}
	w2 := &pgn.WindData{WindSpeed: ptr(5.0), WindAngle: ptr(1.0), WindReference: ptr(pgn.WindReferenceTrueNorth)}

	d := &FieldToleranceDiff{
		PGN: 130306,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeWindData(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "wind_speed", Tolerance: 1.0},
			{Field: "wind_reference", Tolerance: 0},
		},
	}

	sig, _ := d.Diff(w1.Encode(), w2.Encode())
	if !sig {
		t.Fatal("non-numeric field change should be significant")
	}

	// Without wind_reference in the tolerance list, the change is ignored.
	d2 := &FieldToleranceDiff{
		PGN: 130306,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeWindData(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "wind_speed", Tolerance: 1.0},
		},
	}

	sig2, _ := d2.Diff(w1.Encode(), w2.Encode())
	if sig2 {
		t.Fatal("field without tolerance entry should be ignored")
	}
}

func TestFieldToleranceDiff_DataLengthChange(t *testing.T) {
	// Length changes are handled by ChangeTracker.Process (emits Snapshot),
	// not by the diff method. When the inner ByteMaskDiff gets different-length
	// slices it returns significant=true, nil diff.
	d := ByteMaskDiff{}

	sig, diff := d.Diff([]byte{1, 2, 3}, []byte{1, 2, 3, 4})
	if !sig {
		t.Fatal("length change should be significant")
	}
	if diff != nil {
		t.Fatal("length change should return nil diff")
	}
}

func TestFieldToleranceDiff_VesselHeading(t *testing.T) {
	// Use VesselHeading to verify tolerance works with float64 heading in radians.
	h1 := &pgn.VesselHeading{Heading: ptr(math.Pi), Deviation: ptr(0.0), Variation: ptr(0.0)}
	h2 := &pgn.VesselHeading{Heading: ptr(math.Pi + 0.0001), Deviation: ptr(0.0), Variation: ptr(0.0)}

	d := &FieldToleranceDiff{
		PGN: 127250,
		Decode: func(data []byte) (any, error) {
			return pgn.DecodeVesselHeading(data)
		},
		Tolerances: []FieldTolerance{
			{Field: "heading", Tolerance: 0.01},
		},
	}

	sig, _ := d.Diff(h1.Encode(), h2.Encode())
	if sig {
		t.Fatal("0.0001 rad change should be within 0.01 tolerance")
	}

	// Now exceed the tolerance.
	h3 := &pgn.VesselHeading{Heading: ptr(math.Pi + 0.05), Deviation: ptr(0.0), Variation: ptr(0.0)}
	sig, _ = d.Diff(h1.Encode(), h3.Encode())
	if !sig {
		t.Fatal("0.05 rad change should exceed 0.01 tolerance")
	}
}
