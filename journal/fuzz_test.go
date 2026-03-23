package journal

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func FuzzNewReader(f *testing.F) {
	// Seed with a minimal valid v1 journal file (header only, no blocks).
	hdr := make([]byte, 16)
	copy(hdr[0:3], Magic[:])
	hdr[3] = Version
	binary.LittleEndian.PutUint32(hdr[4:8], 4096) // block size
	// bytes 8-15 are zero (no compression, reserved)
	f.Add(hdr)

	// Seed with a minimal valid v2 header.
	hdr2 := make([]byte, 16)
	copy(hdr2[0:3], Magic[:])
	hdr2[3] = Version2
	binary.LittleEndian.PutUint32(hdr2[4:8], 4096)
	f.Add(hdr2)

	// Seed with garbage.
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte("LPJ"))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		jr, err := NewReader(r)
		if err != nil {
			return
		}
		defer jr.Close()

		// Iterate through all frames without panicking.
		for jr.Next() {
			_ = jr.Frame()
		}
		// Err() must not panic.
		_ = jr.Err()
	})
}

func FuzzReadLenPrefixedString(f *testing.F) {
	f.Add([]byte{5, 'h', 'e', 'l', 'l', 'o'}, 0)
	f.Add([]byte{0}, 0)
	f.Add([]byte{}, 0)
	f.Add([]byte{0xFF}, 0)
	f.Add([]byte{3, 'a', 'b'}, 0) // truncated

	f.Fuzz(func(t *testing.T, data []byte, off int) {
		// Must not panic on any input.
		_, _, _ = ReadLenPrefixedString(data, off)
	})
}
