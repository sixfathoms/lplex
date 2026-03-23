package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/pgn"
)

var update = flag.Bool("update", false, "update golden files")

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name)
}

func assertGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := goldenPath(name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden file: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file not found (run with -update to create): %v", err)
	}

	if got != string(want) {
		t.Errorf("output differs from golden file %s\n--- got ---\n%s\n--- want ---\n%s",
			name, got, string(want))
	}
}

func fixedTime(offsetMs int) time.Time {
	return time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC).
		Add(time.Duration(offsetMs) * time.Millisecond)
}

func goldenFrames() []lplex.RxFrame {
	return []lplex.RxFrame{
		makeTestFrame(fixedTime(0), 129025, 1, []byte{0x10, 0x27, 0x00, 0x00, 0xD0, 0x49, 0x01, 0x00}),
		makeTestFrame(fixedTime(100), 130310, 1, []byte{0x01, 0x20, 0x1C, 0x40, 0x9C, 0xFF, 0xFF, 0xFF}),
		makeTestFrame(fixedTime(200), 127250, 2, []byte{0x00, 0xAE, 0x1E, 0xF6, 0xFF, 0x0A, 0x00, 0x01}),
		makeTestFrame(fixedTime(300), 129026, 1, []byte{0x00, 0xFC, 0xFF, 0x20, 0x4E, 0x64, 0x00, 0xFF}),
		makeTestFrame(fixedTime(400), 128259, 3, []byte{0x00, 0xC8, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}),
	}
}

func readJournalFrames(t *testing.T, path string) []journal.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r, err := journal.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var entries []journal.Entry
	for r.Next() {
		e := r.Frame()
		// Copy data to avoid reuse of internal buffer.
		data := make([]byte, len(e.Data))
		copy(data, e.Data)
		entries = append(entries, journal.Entry{
			Timestamp: e.Timestamp,
			Header:    e.Header,
			Data:      data,
		})
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestGoldenDumpJSON verifies JSON output of journal frame dumping.
func TestGoldenDumpJSON(t *testing.T) {
	journalFile := writeTestJournal(t, goldenFrames())
	entries := readJournalFrames(t, journalFile)

	var buf bytes.Buffer
	for _, e := range entries {
		obj := map[string]any{
			"ts":   e.Timestamp.UTC().Format(time.RFC3339Nano),
			"prio": e.Header.Priority,
			"pgn":  e.Header.PGN,
			"src":  e.Header.Source,
			"dst":  e.Header.Destination,
			"data": fmt.Sprintf("%x", e.Data),
		}
		line, _ := json.Marshal(obj)
		buf.Write(line)
		buf.WriteByte('\n')
	}

	assertGolden(t, "dump_json.golden", buf.String())
}

// TestGoldenDumpDecodeJSON verifies decoded JSON output.
func TestGoldenDumpDecodeJSON(t *testing.T) {
	journalFile := writeTestJournal(t, goldenFrames())
	entries := readJournalFrames(t, journalFile)

	var buf bytes.Buffer
	for _, e := range entries {
		obj := map[string]any{
			"ts":   e.Timestamp.UTC().Format(time.RFC3339Nano),
			"prio": e.Header.Priority,
			"pgn":  e.Header.PGN,
			"src":  e.Header.Source,
			"dst":  e.Header.Destination,
			"data": fmt.Sprintf("%x", e.Data),
		}

		if info, ok := pgn.Registry[e.Header.PGN]; ok && info.Decode != nil {
			if decoded, err := info.Decode(e.Data); err == nil {
				obj["decoded"] = decoded
			}
		}

		line, _ := json.Marshal(obj)
		buf.Write(line)
		buf.WriteByte('\n')
	}

	assertGolden(t, "dump_decode_json.golden", buf.String())
}

// TestGoldenInspect verifies the inspect output format.
func TestGoldenInspect(t *testing.T) {
	journalFile := writeTestJournal(t, goldenFrames())

	old := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := runInspectFile(journalFile)

	_ = pw.Close()
	os.Stdout = old

	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(pr)
	output := buf.String()

	// Normalize temp directory path.
	dir := filepath.Dir(journalFile)
	output = strings.ReplaceAll(output, dir+"/", "/test/")

	assertGolden(t, "inspect.golden", output)
}
