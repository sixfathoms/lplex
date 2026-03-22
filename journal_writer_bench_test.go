package lplex

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/journal"
)

func BenchmarkJournalAppendFrame(b *testing.B) {
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	for _, compression := range []journal.CompressionType{
		journal.CompressionNone,
		journal.CompressionZstd,
	} {
		name := "none"
		if compression == journal.CompressionZstd {
			name = "zstd"
		}

		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			ch := make(chan RxFrame, b.N+1)
			devices := NewDeviceRegistry()

			for i := range b.N {
				ch <- RxFrame{
					Timestamp: base.Add(time.Duration(i) * time.Millisecond),
					Header: CANHeader{
						Priority:    2,
						PGN:         127250,
						Source:      35,
						Destination: 0xFF,
					},
					Data: []byte{0xFF, 0x10, 0x7B, 0x00, 0x00, 0x00, 0x00, 0x00},
					Seq:  uint64(i + 1),
				}
			}
			close(ch)

			w, err := NewJournalWriter(JournalConfig{
				Dir:         dir,
				BlockSize:   262144,
				Compression: compression,
			}, devices, ch)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			w.Run(context.Background()) //nolint:errcheck
		})
	}
}

func BenchmarkJournalFrameEncoding(b *testing.B) {
	// Benchmark just the varint + CAN ID encoding portion, without I/O.
	block := make([]byte, 262144)
	data := []byte{0xFF, 0x10, 0x7B, 0x00, 0x00, 0x00, 0x00, 0x00}
	canID := uint32(0x09F11223) // typical CAN ID
	deltaUs := uint64(100)      // 100µs delta

	b.Run("standard_8byte", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			off := 16
			off += binary.PutUvarint(block[off:], deltaUs)
			storedID := canID | 0x80000000
			binary.LittleEndian.PutUint32(block[off:], storedID)
			off += 4
			copy(block[off:], data)
		}
	})

	b.Run("variable_length", func(b *testing.B) {
		varData := []byte{0x01, 0x02, 0x03, 0x04, 0x05} // 5 bytes
		b.ReportAllocs()
		for b.Loop() {
			off := 16
			off += binary.PutUvarint(block[off:], deltaUs)
			binary.LittleEndian.PutUint32(block[off:], canID)
			off += 4
			off += binary.PutUvarint(block[off:], uint64(len(varData)))
			copy(block[off:], varData)
		}
	})
}

func BenchmarkBuildCANID(b *testing.B) {
	header := CANHeader{
		Priority:    2,
		PGN:         127250,
		Source:      35,
		Destination: 0xFF,
	}

	b.ReportAllocs()
	for b.Loop() {
		BuildCANID(header)
	}
}
