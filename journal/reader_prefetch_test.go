package journal

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/sixfathoms/lplex/canbus"
)

// writeTestJournal writes a multi-block v2 journal with 4096-byte blocks.
func writeTestJournal(t testing.TB, dir string, nFrames int, compression CompressionType) string {
	return writeTestJournalWithBlockSize(t, dir, nFrames, compression, 4096)
}

func writeTestJournalWithBlockSize(t testing.TB, dir string, nFrames int, compression CompressionType, blockSize int) string {
	t.Helper()
	path := filepath.Join(dir, "test-20250615T120000.000Z.lpj")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// File header
	var hdr [16]byte
	copy(hdr[0:3], Magic[:])
	hdr[3] = Version2
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(blockSize))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(compression))
	if _, err := f.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}

	baseTimeUs := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC).UnixMicro()

	// Build blocks
	frameIdx := 0
	var blockOffsets []int64

	for frameIdx < nFrames {
		blockBuf := make([]byte, blockSize)

		// BaseTime (8 bytes)
		blockBaseTime := baseTimeUs + int64(frameIdx)*1000 // 1ms per frame
		binary.LittleEndian.PutUint64(blockBuf[0:8], uint64(blockBaseTime))

		// BaseSeq (8 bytes, v2)
		baseSeq := uint64(frameIdx + 1)
		binary.LittleEndian.PutUint64(blockBuf[8:16], baseSeq)

		off := BlockDataOffsetV2
		framesInBlock := 0
		maxDataArea := blockSize - BlockTrailerLen - 2 // leave room for empty device table

		for frameIdx < nFrames && off+1+4+8 < maxDataArea {
			// Delta time (varint, 0 for first frame in block)
			var deltaUs uint64
			if framesInBlock > 0 {
				deltaUs = 1000 // 1ms
			}
			n := binary.PutUvarint(blockBuf[off:], deltaUs)
			off += n

			// CAN ID with standard flag (MSB set = 8-byte fixed length)
			canID := canbus.BuildCANID(canbus.CANHeader{
				Priority:    2,
				PGN:         129025,
				Source:      10,
				Destination: 0xFF,
			})
			binary.LittleEndian.PutUint32(blockBuf[off:], canID|0x80000000)
			off += 4

			// 8 bytes data
			for i := range 8 {
				blockBuf[off+i] = byte((frameIdx + 1) & 0xFF)
			}
			off += 8

			frameIdx++
			framesInBlock++
		}

		// Device table: empty (count=0)
		devTableOff := blockSize - BlockTrailerLen - 2
		binary.LittleEndian.PutUint16(blockBuf[devTableOff:], 0)

		// Trailer: DeviceTableSize(2) + FrameCount(4) + CRC(4)
		trailerOff := blockSize - BlockTrailerLen
		binary.LittleEndian.PutUint16(blockBuf[trailerOff:], 2) // dev table size = 2 (just the count)
		binary.LittleEndian.PutUint32(blockBuf[trailerOff+2:], uint32(framesInBlock))

		// CRC32C over everything except the last 4 bytes
		crc := crc32.Checksum(blockBuf[:blockSize-4], CRC32cTable)
		binary.LittleEndian.PutUint32(blockBuf[blockSize-4:], crc)

		switch compression {
		case CompressionNone:
			offset, _ := f.Seek(0, io.SeekCurrent)
			blockOffsets = append(blockOffsets, offset)
			if _, err := f.Write(blockBuf); err != nil {
				t.Fatal(err)
			}
		case CompressionZstd:
			offset, _ := f.Seek(0, io.SeekCurrent)
			blockOffsets = append(blockOffsets, offset)

			enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
			if err != nil {
				t.Fatal(err)
			}
			compressed := enc.EncodeAll(blockBuf, nil)
			_ = enc.Close()

			// Block header: BaseTime(8) + BaseSeq(8) + CompressedLen(4)
			var bhdr [20]byte
			binary.LittleEndian.PutUint64(bhdr[0:8], uint64(blockBaseTime))
			binary.LittleEndian.PutUint64(bhdr[8:16], baseSeq)
			binary.LittleEndian.PutUint32(bhdr[16:20], uint32(len(compressed)))
			if _, err := f.Write(bhdr[:]); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(compressed); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported compression type %d", compression)
		}
	}

	// For compressed files, write block index footer
	if compression != CompressionNone {
		for _, off := range blockOffsets {
			var offBuf [8]byte
			binary.LittleEndian.PutUint64(offBuf[:], uint64(off))
			if _, err := f.Write(offBuf[:]); err != nil {
				t.Fatal(err)
			}
		}
		var tail [8]byte
		binary.LittleEndian.PutUint32(tail[0:4], uint32(len(blockOffsets)))
		copy(tail[4:8], BlockIndexMagic[:])
		if _, err := f.Write(tail[:]); err != nil {
			t.Fatal(err)
		}
	}

	return path
}

func TestPrefetchUncompressed(t *testing.T) {
	dir := t.TempDir()
	// ~300 frames per 4096-byte block → 1000 frames should give ~3 blocks
	path := writeTestJournal(t, dir, 1000, CompressionNone)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if r.BlockCount() < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", r.BlockCount())
	}

	r.EnablePrefetch()

	// Read all frames and verify correctness
	count := 0
	for r.Next() {
		entry := r.Frame()
		count++
		if entry.Header.PGN != 129025 {
			t.Fatalf("frame %d: PGN = %d, want 129025", count, entry.Header.PGN)
		}
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if count != 1000 {
		t.Fatalf("read %d frames, want 1000", count)
	}
}

func TestPrefetchCompressed(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJournal(t, dir, 1000, CompressionZstd)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if r.BlockCount() < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", r.BlockCount())
	}

	r.EnablePrefetch()

	count := 0
	for r.Next() {
		entry := r.Frame()
		count++
		if entry.Header.PGN != 129025 {
			t.Fatalf("frame %d: PGN = %d, want 129025", count, entry.Header.PGN)
		}
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if count != 1000 {
		t.Fatalf("read %d frames, want 1000", count)
	}
}

func TestPrefetchMatchesNonPrefetch(t *testing.T) {
	dir := t.TempDir()

	for _, compression := range []CompressionType{CompressionNone, CompressionZstd} {
		name := "uncompressed"
		if compression == CompressionZstd {
			name = "zstd"
		}
		t.Run(name, func(t *testing.T) {
			path := writeTestJournal(t, dir, 1000, compression)

			// Read without prefetch
			f1, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			r1, err := NewReader(f1)
			if err != nil {
				_ = f1.Close()
				t.Fatal(err)
			}
			var noPrefetchFrames []Entry
			for r1.Next() {
				e := r1.Frame()
				// Copy data to avoid slice reuse
				data := make([]byte, len(e.Data))
				copy(data, e.Data)
				noPrefetchFrames = append(noPrefetchFrames, Entry{
					Timestamp: e.Timestamp,
					Header:    e.Header,
					Data:      data,
				})
			}
			if r1.Err() != nil {
				t.Fatal(r1.Err())
			}
			r1.Close()
			_ = f1.Close()

			// Read with prefetch
			f2, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			r2, err := NewReader(f2)
			if err != nil {
				_ = f2.Close()
				t.Fatal(err)
			}
			r2.EnablePrefetch()
			var prefetchFrames []Entry
			for r2.Next() {
				e := r2.Frame()
				data := make([]byte, len(e.Data))
				copy(data, e.Data)
				prefetchFrames = append(prefetchFrames, Entry{
					Timestamp: e.Timestamp,
					Header:    e.Header,
					Data:      data,
				})
			}
			if r2.Err() != nil {
				t.Fatal(r2.Err())
			}
			r2.Close()
			_ = f2.Close()

			// Compare
			if len(noPrefetchFrames) != len(prefetchFrames) {
				t.Fatalf("frame count mismatch: %d (no prefetch) vs %d (prefetch)",
					len(noPrefetchFrames), len(prefetchFrames))
			}
			for i := range noPrefetchFrames {
				a, b := noPrefetchFrames[i], prefetchFrames[i]
				if !a.Timestamp.Equal(b.Timestamp) || a.Header != b.Header {
					t.Fatalf("frame %d mismatch: header/timestamp differ", i)
				}
				if len(a.Data) != len(b.Data) {
					t.Fatalf("frame %d data length mismatch: %d vs %d", i, len(a.Data), len(b.Data))
				}
				for j := range a.Data {
					if a.Data[j] != b.Data[j] {
						t.Fatalf("frame %d data[%d] mismatch: %d vs %d", i, j, a.Data[j], b.Data[j])
					}
				}
			}
		})
	}
}

func TestPrefetchSeekInvalidates(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJournal(t, dir, 1000, CompressionNone)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	r.EnablePrefetch()

	// Read a few frames to trigger prefetch
	for i := 0; i < 10; i++ {
		if !r.Next() {
			t.Fatal("expected more frames")
		}
	}

	// Seek should invalidate prefetch and still work correctly
	if err := r.SeekBlock(0); err != nil {
		t.Fatal(err)
	}

	count := 0
	for r.Next() {
		count++
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if count != 1000 {
		t.Fatalf("after seek: read %d frames, want 1000", count)
	}
}

func TestPrefetchSeekToSeq(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJournal(t, dir, 1000, CompressionNone)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	r.EnablePrefetch()

	// Read 10 frames
	for i := 0; i < 10; i++ {
		if !r.Next() {
			t.Fatal("expected more frames")
		}
	}

	// Seek to a specific seq
	if err := r.SeekToSeq(500); err != nil {
		t.Fatal(err)
	}

	// Should be able to iterate from there
	if !r.Next() {
		t.Fatal("expected frame after SeekToSeq")
	}
	seq := r.FrameSeq()
	if seq > 500 {
		t.Fatalf("after SeekToSeq(500): first frame seq = %d, want <= 500", seq)
	}
}

func TestPrefetchSingleBlock(t *testing.T) {
	dir := t.TempDir()
	// 10 frames fits in a single block; no prefetch should be triggered
	path := writeTestJournal(t, dir, 10, CompressionNone)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if r.BlockCount() != 1 {
		t.Fatalf("expected 1 block, got %d", r.BlockCount())
	}

	r.EnablePrefetch()

	count := 0
	for r.Next() {
		count++
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if count != 10 {
		t.Fatalf("read %d frames, want 10", count)
	}
}

func BenchmarkReaderNoPrefetch(b *testing.B) {
	dir := b.TempDir()
	// 256KB blocks, realistic for production journals
	path := writeTestJournalWithBlockSize(b, dir, 100000, CompressionZstd, 256*1024)

	b.ResetTimer()
	for range b.N {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		r, err := NewReader(f)
		if err != nil {
			_ = f.Close()
			b.Fatal(err)
		}
		count := 0
		for r.Next() {
			_ = r.Frame()
			count++
		}
		r.Close()
		_ = f.Close()
		if count != 100000 {
			b.Fatalf("read %d frames, want 100000", count)
		}
	}
}

func BenchmarkReaderPrefetch(b *testing.B) {
	dir := b.TempDir()
	path := writeTestJournalWithBlockSize(b, dir, 100000, CompressionZstd, 256*1024)

	b.ResetTimer()
	for range b.N {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		r, err := NewReader(f)
		if err != nil {
			_ = f.Close()
			b.Fatal(err)
		}
		r.EnablePrefetch()
		count := 0
		for r.Next() {
			_ = r.Frame()
			count++
		}
		r.Close()
		_ = f.Close()
		if count != 100000 {
			b.Fatalf("read %d frames, want 100000", count)
		}
	}
}
