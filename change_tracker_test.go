package lplex

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

func TestChangeTracker_FirstFrame_Snapshot(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ev := ct.Process(ts, 10, 127250, data, 1)
	if ev == nil {
		t.Fatal("first frame should produce an event")
	}
	if ev.Type != Snapshot {
		t.Fatalf("expected Snapshot, got %s", ev.Type)
	}
	if !bytes.Equal(ev.Data, data) {
		t.Fatalf("snapshot data mismatch")
	}
	if ev.Source != 10 || ev.PGN != 127250 || ev.Seq != 1 {
		t.Fatalf("event metadata wrong: src=%d pgn=%d seq=%d", ev.Source, ev.PGN, ev.Seq)
	}
}

func TestChangeTracker_UnchangedFrame_Nil(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 127250, data, 1)
	ev := ct.Process(ts.Add(time.Millisecond), 10, 127250, data, 2)
	if ev != nil {
		t.Fatalf("unchanged frame should return nil, got %s", ev.Type)
	}
}

func TestChangeTracker_ChangedFrame_Delta(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	data2 := []byte{0x01, 0xFF, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 127250, data1, 1)
	ev := ct.Process(ts.Add(time.Millisecond), 10, 127250, data2, 2)
	if ev == nil {
		t.Fatal("changed frame should produce an event")
	}
	if ev.Type != Delta {
		t.Fatalf("expected Delta, got %s", ev.Type)
	}
	if ev.Seq != 2 {
		t.Fatalf("expected seq 2, got %d", ev.Seq)
	}
}

func TestChangeTracker_MultipleSources_Independent(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	// Two different sources with the same PGN should track independently.
	ev1 := ct.Process(ts, 10, 127250, data, 1)
	ev2 := ct.Process(ts, 20, 127250, data, 2)

	if ev1 == nil || ev1.Type != Snapshot {
		t.Fatal("source 10 first frame should be Snapshot")
	}
	if ev2 == nil || ev2.Type != Snapshot {
		t.Fatal("source 20 first frame should be Snapshot")
	}
	if ct.TrackedPairs() != 2 {
		t.Fatalf("expected 2 tracked pairs, got %d", ct.TrackedPairs())
	}
}

func TestChangeTracker_SubKey_Independent(t *testing.T) {
	// Simulate Victron register multiplexing on PGN 61184.
	// Sub-key extracted from bytes 2-3 (register_id as uint16 LE).
	ct := NewChangeTracker(ChangeTrackerConfig{
		SubKeys: map[uint32]SubKeyFunc{
			61184: func(data []byte) uint64 {
				if len(data) < 4 {
					return 0
				}
				return uint64(binary.LittleEndian.Uint16(data[2:4]))
			},
		},
	})

	ts := time.Now()

	// Register 0x0100.
	reg1Data := []byte{0x00, 0x00, 0x00, 0x01, 0xAA, 0xBB, 0xCC, 0xDD}
	// Register 0x0200.
	reg2Data := []byte{0x00, 0x00, 0x00, 0x02, 0x11, 0x22, 0x33, 0x44}

	ev1 := ct.Process(ts, 5, 61184, reg1Data, 1)
	ev2 := ct.Process(ts, 5, 61184, reg2Data, 2)

	if ev1 == nil || ev1.Type != Snapshot {
		t.Fatal("register 1 first frame should be Snapshot")
	}
	if ev1.SubKey != 0x0100 {
		t.Fatalf("expected subkey 0x0100, got 0x%04x", ev1.SubKey)
	}
	if ev2 == nil || ev2.Type != Snapshot {
		t.Fatal("register 2 first frame should be Snapshot")
	}
	if ev2.SubKey != 0x0200 {
		t.Fatalf("expected subkey 0x0200, got 0x%04x", ev2.SubKey)
	}

	// Same data for register 1 again: no event.
	ev3 := ct.Process(ts.Add(time.Millisecond), 5, 61184, reg1Data, 3)
	if ev3 != nil {
		t.Fatalf("unchanged register should return nil, got %s", ev3.Type)
	}

	if ct.TrackedPairs() != 2 {
		t.Fatalf("expected 2 tracked pairs, got %d", ct.TrackedPairs())
	}
}

func TestChangeTracker_DataLengthChange_Snapshot(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()

	ct.Process(ts, 10, 127250, []byte{0x01, 0x02, 0x03}, 1)
	ev := ct.Process(ts.Add(time.Millisecond), 10, 127250, []byte{0x01, 0x02, 0x03, 0x04}, 2)
	if ev == nil || ev.Type != Snapshot {
		t.Fatal("data length change should produce new Snapshot")
	}
}

func TestChangeTracker_IdleDetection(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{
		DefaultIdleTimeout: 100 * time.Millisecond,
	})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 99999, data, 1)

	// Before timeout: no idle events.
	events := ct.Tick(ts.Add(50 * time.Millisecond))
	if len(events) != 0 {
		t.Fatalf("expected no idle events before timeout, got %d", len(events))
	}

	// After timeout: one idle event.
	events = ct.Tick(ts.Add(150 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 idle event, got %d", len(events))
	}
	if events[0].Type != Idle {
		t.Fatalf("expected Idle event, got %s", events[0].Type)
	}
	if events[0].Source != 10 || events[0].PGN != 99999 {
		t.Fatalf("idle event has wrong key")
	}
}

func TestChangeTracker_IdleEmittedOnce(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{
		DefaultIdleTimeout: 100 * time.Millisecond,
	})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 99999, data, 1)

	// First tick past timeout.
	events := ct.Tick(ts.Add(150 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 idle event, got %d", len(events))
	}

	// Second tick: should not emit again.
	events = ct.Tick(ts.Add(200 * time.Millisecond))
	if len(events) != 0 {
		t.Fatalf("idle should emit only once per silence, got %d events", len(events))
	}
}

func TestChangeTracker_ActivityResetsIdle(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{
		DefaultIdleTimeout: 100 * time.Millisecond,
	})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 99999, data, 1)

	// Idle fires.
	events := ct.Tick(ts.Add(150 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected 1 idle, got %d", len(events))
	}

	// New frame resets idle.
	ct.Process(ts.Add(200*time.Millisecond), 10, 99999, data, 2)

	// Idle should not fire yet.
	events = ct.Tick(ts.Add(250 * time.Millisecond))
	if len(events) != 0 {
		t.Fatalf("activity should reset idle, got %d events", len(events))
	}

	// But eventually it fires again.
	events = ct.Tick(ts.Add(350 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected idle to fire again after silence, got %d", len(events))
	}
}

func TestChangeTracker_IdleTimeoutFromRegistry(t *testing.T) {
	// PGN 127250 (Vessel Heading) has Interval=100ms in the registry.
	// With multiplier=3, timeout should be 300ms.
	ct := NewChangeTracker(ChangeTrackerConfig{
		IdleMultiplier:     3,
		DefaultIdleTimeout: 10 * time.Second,
	})
	ts := time.Now()
	data := (&pgn.VesselHeading{Heading: 1.0}).Encode()

	ct.Process(ts, 10, 127250, data, 1)

	// At 200ms: should not idle (300ms timeout).
	events := ct.Tick(ts.Add(200 * time.Millisecond))
	if len(events) != 0 {
		t.Fatalf("expected no idle at 200ms (timeout=300ms), got %d", len(events))
	}

	// At 350ms: should idle.
	events = ct.Tick(ts.Add(350 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected idle at 350ms, got %d events", len(events))
	}
}

func TestChangeTracker_IdleTimeoutOverride(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{
		DefaultIdleTimeout: 10 * time.Second,
		IdleTimeouts: map[uint32]time.Duration{
			127250: 50 * time.Millisecond,
		},
	})
	ts := time.Now()
	data := (&pgn.VesselHeading{Heading: 1.0}).Encode()

	ct.Process(ts, 10, 127250, data, 1)

	// Override is 50ms, which takes precedence over registry * multiplier.
	events := ct.Tick(ts.Add(60 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected idle at 60ms (override=50ms), got %d events", len(events))
	}
}

func TestChangeTracker_IdleTimeoutFallback(t *testing.T) {
	// PGN 99999 is not in the registry, so it falls back to default.
	ct := NewChangeTracker(ChangeTrackerConfig{
		DefaultIdleTimeout: 200 * time.Millisecond,
	})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 99999, data, 1)

	events := ct.Tick(ts.Add(150 * time.Millisecond))
	if len(events) != 0 {
		t.Fatalf("expected no idle at 150ms (default=200ms)")
	}

	events = ct.Tick(ts.Add(250 * time.Millisecond))
	if len(events) != 1 {
		t.Fatalf("expected idle at 250ms")
	}
}

func TestChangeTracker_Reset(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 127250, data, 1)
	if ct.TrackedPairs() != 1 {
		t.Fatal("expected 1 tracked pair")
	}

	ct.Reset()
	if ct.TrackedPairs() != 0 {
		t.Fatal("expected 0 tracked pairs after reset")
	}

	// After reset, next frame is a Snapshot again.
	ev := ct.Process(ts.Add(time.Second), 10, 127250, data, 2)
	if ev == nil || ev.Type != Snapshot {
		t.Fatal("first frame after reset should be Snapshot")
	}
}

func TestChangeTracker_CustomDiffMethod(t *testing.T) {
	// Use FieldToleranceDiff for PGN 127250 to suppress small heading changes.
	ct := NewChangeTracker(ChangeTrackerConfig{
		Methods: map[uint32]DiffMethod{
			127250: &FieldToleranceDiff{
				PGN: 127250,
				Decode: func(data []byte) (any, error) {
					return pgn.DecodeVesselHeading(data)
				},
				Tolerances: []FieldTolerance{
					{Field: "heading", Tolerance: 0.01},
				},
			},
		},
	})

	ts := time.Now()
	h1 := (&pgn.VesselHeading{Heading: 1.0}).Encode()
	h2 := (&pgn.VesselHeading{Heading: 1.001}).Encode() // Within tolerance.
	h3 := (&pgn.VesselHeading{Heading: 1.5}).Encode()   // Exceeds tolerance.

	ct.Process(ts, 10, 127250, h1, 1)

	ev := ct.Process(ts.Add(time.Millisecond), 10, 127250, h2, 2)
	if ev != nil {
		t.Fatal("change within tolerance should be suppressed")
	}

	ev = ct.Process(ts.Add(2*time.Millisecond), 10, 127250, h3, 3)
	if ev == nil || ev.Type != Delta {
		t.Fatal("change exceeding tolerance should produce Delta")
	}
}

func TestChangeReplayer_SnapshotStoresState(t *testing.T) {
	r := NewChangeReplayer(nil, nil)
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	result, err := r.Apply(ChangeEvent{Type: Snapshot, Source: 10, PGN: 127250, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, data) {
		t.Fatalf("snapshot should return full data")
	}

	state := r.State(10, 127250, 0)
	if !bytes.Equal(state, data) {
		t.Fatalf("State should return stored data")
	}
}

func TestChangeReplayer_DeltaApplies(t *testing.T) {
	r := NewChangeReplayer(nil, nil)
	data1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	data2 := []byte{0x01, 0xFF, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	d := ByteMaskDiff{}
	_, diff := d.Diff(data1, data2)

	if _, err := r.Apply(ChangeEvent{Type: Snapshot, Source: 10, PGN: 127250, Data: data1}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Apply(ChangeEvent{Type: Delta, Source: 10, PGN: 127250, Data: diff})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, data2) {
		t.Fatalf("delta should reconstruct: got %x, want %x", result, data2)
	}
}

func TestChangeReplayer_DeltaWithoutSnapshot_Error(t *testing.T) {
	r := NewChangeReplayer(nil, nil)
	_, err := r.Apply(ChangeEvent{Type: Delta, Source: 10, PGN: 127250, Data: []byte{0x02, 0xFF}})
	if err == nil {
		t.Fatal("delta without snapshot should error")
	}
}

func TestChangeReplayer_IdlePreservesState(t *testing.T) {
	r := NewChangeReplayer(nil, nil)
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	if _, err := r.Apply(ChangeEvent{Type: Snapshot, Source: 10, PGN: 127250, Data: data}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Apply(ChangeEvent{Type: Idle, Source: 10, PGN: 127250})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("idle should return nil data")
	}

	state := r.State(10, 127250, 0)
	if !bytes.Equal(state, data) {
		t.Fatal("idle should preserve last known state")
	}
}

func TestChangeTracker_EndToEnd_RoundTrip(t *testing.T) {
	// Full end-to-end: tracker produces events, replayer reconstructs data.
	ct := NewChangeTracker(ChangeTrackerConfig{})
	r := NewChangeReplayer(nil, nil)

	frames := []struct {
		data []byte
		seq  uint64
	}{
		{(&pgn.VesselHeading{Heading: 1.0}).Encode(), 1},
		{(&pgn.VesselHeading{Heading: 1.0}).Encode(), 2},   // No change.
		{(&pgn.VesselHeading{Heading: 1.5}).Encode(), 3},   // Delta.
		{(&pgn.VesselHeading{Heading: 2.0}).Encode(), 4},   // Delta.
		{(&pgn.VesselHeading{Heading: 2.0}).Encode(), 5},   // No change.
		{(&pgn.VesselHeading{Heading: 3.14}).Encode(), 6},  // Delta.
	}

	ts := time.Now()
	for i, f := range frames {
		ev := ct.Process(ts.Add(time.Duration(i)*100*time.Millisecond), 10, 127250, f.data, f.seq)
		if ev == nil {
			continue
		}

		result, err := r.Apply(*ev)
		if err != nil {
			t.Fatalf("frame %d: replayer error: %v", i, err)
		}
		if !bytes.Equal(result, f.data) {
			t.Fatalf("frame %d: reconstructed data mismatch:\ngot  %x\nwant %x", i, result, f.data)
		}
	}
}

func TestChangeTracker_EndToEnd_WithSubKeys(t *testing.T) {
	// Two "register" channels on the same (source, PGN), using sub-keys.
	subKeyFn := func(data []byte) uint64 {
		if len(data) < 4 {
			return 0
		}
		return uint64(binary.LittleEndian.Uint16(data[2:4]))
	}

	ct := NewChangeTracker(ChangeTrackerConfig{
		SubKeys: map[uint32]SubKeyFunc{61184: subKeyFn},
	})
	r := NewChangeReplayer(nil, map[uint32]SubKeyFunc{61184: subKeyFn})

	ts := time.Now()

	// Register 0x0100, first packet.
	r1v1 := []byte{0x00, 0x00, 0x00, 0x01, 0xAA, 0xBB, 0xCC, 0xDD}
	ev := ct.Process(ts, 5, 61184, r1v1, 1)
	result, _ := r.Apply(*ev)
	if !bytes.Equal(result, r1v1) {
		t.Fatal("register 1 snapshot mismatch")
	}

	// Register 0x0200, first packet.
	r2v1 := []byte{0x00, 0x00, 0x00, 0x02, 0x11, 0x22, 0x33, 0x44}
	ev = ct.Process(ts, 5, 61184, r2v1, 2)
	result, _ = r.Apply(*ev)
	if !bytes.Equal(result, r2v1) {
		t.Fatal("register 2 snapshot mismatch")
	}

	// Register 0x0100, value change.
	r1v2 := []byte{0x00, 0x00, 0x00, 0x01, 0xFF, 0xBB, 0xCC, 0xDD}
	ev = ct.Process(ts.Add(time.Millisecond), 5, 61184, r1v2, 3)
	if ev == nil || ev.Type != Delta {
		t.Fatal("register 1 changed value should produce Delta")
	}
	result, _ = r.Apply(*ev)
	if !bytes.Equal(result, r1v2) {
		t.Fatalf("register 1 delta mismatch: got %x, want %x", result, r1v2)
	}

	// Register 0x0200 unchanged.
	state := r.State(5, 61184, 0x0200)
	if !bytes.Equal(state, r2v1) {
		t.Fatal("register 2 should be unchanged")
	}
}

func TestChangeTracker_Remove(t *testing.T) {
	ct := NewChangeTracker(ChangeTrackerConfig{})
	ts := time.Now()
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	ct.Process(ts, 10, 127250, data, 1)
	if ct.TrackedPairs() != 1 {
		t.Fatal("expected 1 tracked pair")
	}

	ct.Remove(10, 127250, 0)
	if ct.TrackedPairs() != 0 {
		t.Fatal("expected 0 tracked pairs after remove")
	}

	// Next frame is a fresh Snapshot.
	ev := ct.Process(ts.Add(time.Second), 10, 127250, data, 2)
	if ev == nil || ev.Type != Snapshot {
		t.Fatal("first frame after remove should be Snapshot")
	}
}
