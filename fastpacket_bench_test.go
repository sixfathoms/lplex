package lplex

import (
	"testing"
	"time"
)

func BenchmarkFastPacketProcess(b *testing.B) {
	// Simulate a 20-byte fast-packet transfer (3 frames).
	pgn := uint32(126996)
	src := uint8(35)

	frame0 := []byte{0x20, 20, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	frame1 := []byte{0x21, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D}
	frame2 := []byte{0x22, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	b.Run("complete_transfer", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			a := NewFastPacketAssembler(750 * time.Millisecond)
			a.Process(pgn, src, frame0, now)
			a.Process(pgn, src, frame1, now)
			a.Process(pgn, src, frame2, now)
		}
	})

	b.Run("reuse_assembler", func(b *testing.B) {
		a := NewFastPacketAssembler(750 * time.Millisecond)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			a.Process(pgn, src, frame0, now)
			a.Process(pgn, src, frame1, now)
			a.Process(pgn, src, frame2, now)
		}
	})

	b.Run("concurrent_sources", func(b *testing.B) {
		a := NewFastPacketAssembler(750 * time.Millisecond)
		// 4 concurrent sources
		frames := [4][3][]byte{
			{
				{0x20, 20, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
				{0x21, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D},
				{0x22, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14},
			},
			{
				{0x40, 20, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16},
				{0x41, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D},
				{0x42, 0x1E, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24},
			},
			{
				{0x60, 20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26},
				{0x61, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x2D},
				{0x62, 0x2E, 0x2F, 0x30, 0x31, 0x32, 0x33, 0x34},
			},
			{
				{0x80, 20, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36},
				{0x81, 0x37, 0x38, 0x39, 0x3A, 0x3B, 0x3C, 0x3D},
				{0x82, 0x3E, 0x3F, 0x40, 0x41, 0x42, 0x43, 0x44},
			},
		}
		sources := [4]uint8{35, 42, 50, 60}

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			for i := range 4 {
				a.Process(pgn, sources[i], frames[i][0], now)
			}
			for i := range 4 {
				a.Process(pgn, sources[i], frames[i][1], now)
			}
			for i := range 4 {
				a.Process(pgn, sources[i], frames[i][2], now)
			}
		}
	})
}

func BenchmarkFragmentFastPacket(b *testing.B) {
	b.Run("20_bytes", func(b *testing.B) {
		data := make([]byte, 20)
		for i := range data {
			data[i] = byte(i + 1)
		}
		b.ReportAllocs()
		for b.Loop() {
			FragmentFastPacket(data, 0)
		}
	})

	b.Run("223_bytes", func(b *testing.B) {
		data := make([]byte, 223)
		for i := range data {
			data[i] = byte(i)
		}
		b.ReportAllocs()
		for b.Loop() {
			FragmentFastPacket(data, 0)
		}
	})
}

func BenchmarkIsFastPacket(b *testing.B) {
	b.Run("fast_packet", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			IsFastPacket(126996) // Product Information — fast-packet
		}
	})

	b.Run("single_frame", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			IsFastPacket(127250) // Vessel Heading — single frame
		}
	})

	b.Run("unknown_pgn", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			IsFastPacket(99999) // not in registry
		}
	})
}

func BenchmarkPurgeStale(b *testing.B) {
	a := NewFastPacketAssembler(750 * time.Millisecond)
	staleTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	now := staleTime.Add(2 * time.Second)

	// Populate with stale entries.
	for i := range 100 {
		frame0 := []byte{byte(i%8) << 5, 20, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
		a.Process(uint32(126996+i), uint8(i), frame0, staleTime)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a.PurgeStale(now)
	}
}
