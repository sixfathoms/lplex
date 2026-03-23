package lplex

import (
	"testing"
	"time"
)

func FuzzFastPacketProcess(f *testing.F) {
	// Seed with typical fast-packet frame 0 (sequence counter 0, frame 0).
	f.Add(uint32(129029), uint8(1), []byte{0x00, 0x2F, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06})
	// Frame 1 continuation.
	f.Add(uint32(129029), uint8(1), []byte{0x01, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D})
	// Empty data.
	f.Add(uint32(129029), uint8(1), []byte{})
	// Single byte.
	f.Add(uint32(129029), uint8(1), []byte{0x00})
	// All 0xFF.
	f.Add(uint32(129029), uint8(1), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// TotalLen = 0 (invalid).
	f.Add(uint32(129029), uint8(1), []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	f.Fuzz(func(t *testing.T, pgn uint32, source uint8, data []byte) {
		a := NewFastPacketAssembler(750 * time.Millisecond)
		now := time.Now()
		// Process must not panic on any input.
		_ = a.Process(pgn, source, data, now)
		// PurgeStale must not panic.
		a.PurgeStale(now.Add(time.Second))
	})
}
