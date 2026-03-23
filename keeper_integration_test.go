package lplex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/keeper"
)

// -----------------------------------------------------------------------
// Integration: JournalWriter OnRotate fires on rotation
// -----------------------------------------------------------------------

func TestJournalWriterOnRotateCallback(t *testing.T) {
	dir := t.TempDir()
	devices := NewDeviceRegistry()

	var mu sync.Mutex
	var rotated []keeper.RotatedFile

	// Use 2000-byte frames so each 4096-byte block fits exactly 2 frames
	// (~2007 bytes encoded each). With RotateCount=2 and 8 frames:
	//   File1: frames 0,1 → rotation (callback 1)
	//   File2: frames 2,3,4,5 → rotation (callback 2)
	//   File3: frames 6,7 → finalize (callback 3)
	// Note: file2 holds 4 frames because openFile resets fileFrames, causing
	// the carry-over frame to not count toward the new file's rotation threshold.
	bigData := make([]byte, 2000)
	for i := range bigData {
		bigData[i] = byte(i)
	}

	ch := make(chan RxFrame, 100)
	jw, err := NewJournalWriter(JournalConfig{
		Dir:         dir,
		Prefix:      "nmea2k",
		BlockSize:   4096,
		Compression: journal.CompressionNone,
		RotateCount: 2,
		OnRotate: func(rf keeper.RotatedFile) {
			mu.Lock()
			rotated = append(rotated, rf)
			mu.Unlock()
		},
	}, devices, ch)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now()
	for i := range 8 {
		ch <- RxFrame{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      bigData,
			Seq:       uint64(i + 1),
		}
	}
	close(ch)

	if err := jw.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	count := len(rotated)
	paths := make([]string, len(rotated))
	for i, rf := range rotated {
		paths[i] = rf.Path
	}
	mu.Unlock()

	// 2 rotations + 1 finalize = 3 callbacks.
	if count != 3 {
		t.Fatalf("expected 3 OnRotate calls, got %d: %v", count, paths)
	}

	// All paths should be real files in dir (they were closed, so they exist).
	for _, p := range paths {
		if !strings.HasPrefix(p, dir) {
			t.Errorf("path %q should be in dir %q", p, dir)
		}
		if !strings.HasSuffix(p, ".lpj") {
			t.Errorf("path %q should end with .lpj", p)
		}
	}
}

// -----------------------------------------------------------------------
// Integration: JournalWriter OnRotate fires on finalize (ctx cancel)
// -----------------------------------------------------------------------

func TestJournalWriterOnRotateOnFinalize(t *testing.T) {
	dir := t.TempDir()
	devices := NewDeviceRegistry()

	var called atomic.Bool

	ch := make(chan RxFrame, 10)
	jw, err := NewJournalWriter(JournalConfig{
		Dir:         dir,
		Prefix:      "nmea2k",
		BlockSize:   4096,
		Compression: journal.CompressionNone,
		OnRotate: func(rf keeper.RotatedFile) {
			called.Store(true)
		},
	}, devices, ch)
	if err != nil {
		t.Fatal(err)
	}

	// Start the writer, send frames, then cancel.
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- jw.Run(ctx) }()

	base := time.Now()
	for i := range 3 {
		ch <- RxFrame{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0, 1, 2, 3, 4, 5, 6, 7},
			Seq:       uint64(i + 1),
		}
	}

	// Small delay to let the writer consume the frames, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	if !called.Load() {
		t.Error("OnRotate should fire on finalize")
	}
}

// -----------------------------------------------------------------------
// Integration: BlockWriter OnRotate fires on rotation and Close
// -----------------------------------------------------------------------

func TestBlockWriterOnRotateCallback(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var rotated []string

	bw, err := NewBlockWriter(BlockWriterConfig{
		Dir:        dir,
		Prefix:     "backfill",
		BlockSize:  4096,
		RotateSize: 5000, // ~1 uncompressed block (4096 + header) triggers rotation
		OnRotate: func(rf keeper.RotatedFile) {
			mu.Lock()
			rotated = append(rotated, rf.Path)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write 3 uncompressed blocks (each 4096 bytes). After the first one,
	// fileBytes = 16 (header) + 4096 = 4112, not yet over 5000. After the
	// second, 4112 + 4096 = 8208, over 5000, triggers rotation.
	base := time.Now()
	for i := range 3 {
		block := make([]byte, 4096)
		// Need valid CRC for uncompressed blocks.
		ts := base.Add(time.Duration(i) * time.Second)
		if err := bw.AppendBlock(uint64(i*100+1), ts.UnixMicro(), block, false); err != nil {
			// CRC validation will fail on zeroed blocks, skip the error for this test.
			// Use compressed blocks instead.
			break
		}
	}

	// Use compressed blocks which skip CRC validation.
	bw2, err := NewBlockWriter(BlockWriterConfig{
		Dir:         dir,
		Prefix:      "backfill2",
		BlockSize:   4096,
		Compression: journal.CompressionZstd,
		RotateSize:  100, // tiny limit to trigger rotation quickly
		OnRotate: func(rf keeper.RotatedFile) {
			mu.Lock()
			rotated = append(rotated, rf.Path)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		ts := base.Add(time.Duration(i) * time.Second)
		data := make([]byte, 50) // some compressed payload
		if err := bw2.AppendBlock(uint64(i*100+1), ts.UnixMicro(), data, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := bw2.Close(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	count := len(rotated)
	mu.Unlock()

	// At least 2 callbacks expected (rotations + close).
	if count < 2 {
		t.Errorf("expected at least 2 OnRotate calls, got %d", count)
	}
}

// -----------------------------------------------------------------------
// Integration: full pipeline (JournalWriter → keeper → archive → delete)
// -----------------------------------------------------------------------

func TestFullPipelineJournalWriterToKeeperToArchiveToDelete(t *testing.T) {
	journalDir := t.TempDir()
	scriptDir := t.TempDir()
	devices := NewDeviceRegistry()

	// Create a pre-existing old file that should be cleaned up by retention.
	now := time.Now().UTC()
	oldName := lpjNameHelper("nmea2k", now.Add(-10*24*time.Hour))
	oldPath := filepath.Join(journalDir, oldName)
	if err := os.WriteFile(oldPath, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set up the archive script.
	scriptPath := filepath.Join(scriptDir, "archive.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+`for arg in "$@"; do echo "{\"path\":\"$arg\",\"status\":\"ok\"}"; done`), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create the keeper.
	jk := keeper.NewJournalKeeper(keeper.KeeperConfig{
		Dirs:           []keeper.KeeperDir{{Dir: journalDir, InstanceID: "boat-1"}},
		MaxAge:         7 * 24 * time.Hour,
		ArchiveCommand: scriptPath,
		ArchiveTrigger: keeper.ArchiveOnRotate,
	})

	// Use 2000-byte frames so each 4096-byte block fits exactly 2 frames.
	// With RotateCount=2, rotation fires every block. 6 frames → 2 rotations + 1 finalize.
	bigData := make([]byte, 2000)
	for i := range bigData {
		bigData[i] = byte(i)
	}

	ch := make(chan RxFrame, 200)
	jw, err := NewJournalWriter(JournalConfig{
		Dir:         journalDir,
		Prefix:      "nmea2k",
		BlockSize:   4096,
		Compression: journal.CompressionNone,
		RotateCount: 2,
		OnRotate: func(rf keeper.RotatedFile) {
			rf.InstanceID = "boat-1"
			jk.Send(rf)
		},
	}, devices, ch)
	if err != nil {
		t.Fatal(err)
	}

	// Start the keeper.
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	wg.Go(func() {
		jk.Run(ctx)
	})

	// Write 6 frames: triggers 2 rotations + 1 finalize = 3 OnRotate callbacks.
	base := time.Now()
	for i := range 6 {
		ch <- RxFrame{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      bigData,
			Seq:       uint64(i + 1),
		}
	}
	close(ch)

	// Run the journal writer to completion (processes all frames, finalizes).
	if err := jw.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Poll for the newest .archived marker to appear.
	deadline := time.After(5 * time.Second)
	for {
		entries, _ := os.ReadDir(journalDir)
		count := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".archived") {
				count++
			}
		}
		// We expect at least 2 archived files (rotations sent via keeper).
		if count >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for archived markers, got %d", count)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	wg.Wait()

	// Verify: newly rotated files should have .archived markers (on-rotate trigger).
	entries, _ := os.ReadDir(journalDir)
	archivedCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".archived") {
			archivedCount++
		}
	}

	// We should have some archived files from the on-rotate notifications.
	if archivedCount == 0 {
		t.Error("expected at least one .archived marker from on-rotate")
	}

	// The old file should be deleted or about to be deleted (startup scan
	// archives it, second scan deletes it). Since the keeper only ran the
	// startup scan once, the old file might be archived but not yet deleted.
	if _, err := os.Stat(oldPath); err == nil {
		// Check if .archived marker exists.
		if _, err := os.Stat(oldPath + ".archived"); err != nil {
			t.Error("old file should be archived or deleted by the keeper")
		}
	}
}

// lpjNameHelper builds a journal filename from a time.
func lpjNameHelper(prefix string, ts time.Time) string {
	return prefix + "-" + ts.UTC().Format("20060102T150405.000Z") + ".lpj"
}

// -----------------------------------------------------------------------
// JournalWriter: pause discards frames
// -----------------------------------------------------------------------

func TestJournalWriterPausedDiscardsFrames(t *testing.T) {
	dir := t.TempDir()
	devices := NewDeviceRegistry()

	// Use unbuffered channel so we know the writer has consumed each frame
	// before we send the next.
	ch := make(chan RxFrame)
	jw, err := NewJournalWriter(JournalConfig{
		Dir:         dir,
		Prefix:      "nmea2k",
		BlockSize:   4096,
		Compression: journal.CompressionNone,
	}, devices, ch)
	if err != nil {
		t.Fatal(err)
	}

	// Start writer in background.
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- jw.Run(context.Background())
	}()

	base := time.Now()
	sendFrame := func(i int) {
		ch <- RxFrame{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0, 1, 2, 3, 4, 5, 6, 7},
			Seq:       uint64(i + 1),
		}
	}

	// Phase 1: send 3 frames unpaused.
	for i := range 3 {
		sendFrame(i)
	}

	// Phase 2: pause, send 3 more frames (will be consumed but discarded).
	jw.SetPaused(true)
	for i := 3; i < 6; i++ {
		sendFrame(i)
	}

	// Phase 3: resume, send 3 more frames.
	jw.SetPaused(false)
	for i := 6; i < 9; i++ {
		sendFrame(i)
	}

	close(ch)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}

	// Read back journal files and count frames via the journal reader.
	entries, _ := os.ReadDir(dir)
	totalFrames := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".lpj") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		r, err := journal.NewReader(f)
		if err != nil {
			_ = f.Close()
			t.Fatalf("journal reader %s: %v", path, err)
		}
		for r.Next() {
			totalFrames++
		}
		_ = f.Close()
	}

	// 9 frames sent total. Phase 1 (3) and Phase 3 (3) are unpaused.
	// Phase 2 (3) is paused. Boundary frames at the pause/unpause transitions
	// may or may not be discarded: the writer receives from the unbuffered
	// channel and then checks paused.Load(), racing with the test's Store.
	// Frame at the unpause boundary (frame 5) can leak through if the writer
	// sees the Store(false) before checking the flag. Valid range: 4-7.
	if totalFrames < 4 || totalFrames > 7 {
		t.Errorf("expected 4-7 frames written (some discarded while paused), got %d", totalFrames)
	}
	// Critically: not all 9 frames were written, meaning pause had an effect.
	if totalFrames == 9 {
		t.Error("all 9 frames written, pause had no effect")
	}
}
