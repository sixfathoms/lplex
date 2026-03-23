package pgn

import "testing"

func FuzzPGNDecode(f *testing.F) {
	// Seed with typical frame sizes and edge cases.
	f.Add(uint32(129025), []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Add(uint32(127488), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(uint32(130310), []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
	f.Add(uint32(59904), []byte{0x00, 0xF0, 0x01})
	f.Add(uint32(127508), []byte{0x01, 0xE8, 0x03, 0x64, 0x00, 0xFF, 0xFF, 0xFF})
	f.Add(uint32(129025), []byte{})               // empty
	f.Add(uint32(129025), []byte{0x01})            // short
	f.Add(uint32(0), []byte{0x00, 0x00, 0x00})     // unknown PGN
	f.Add(uint32(65535), []byte{0xFF, 0xFF, 0xFF}) // unknown PGN

	f.Fuzz(func(t *testing.T, pgnNum uint32, data []byte) {
		info, ok := Registry[pgnNum]
		if !ok || info.Decode == nil {
			return
		}
		// Decode must not panic on any input.
		_, _ = info.Decode(data)
	})
}
