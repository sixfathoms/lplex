package lplex

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/sendpolicy"
)

func TestHistoryEndpoint(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Write frames to journal
	var frames []RxFrame
	for i := range 20 {
		frames = append(frames, makeFrameWithSeq(
			base.Add(time.Duration(i)*time.Second),
			129025, 10,
			[]byte{byte(i + 1), 0, 0, 0, 0, 0, 0, 0},
			uint64(i+1),
		))
	}
	writeJournalFrames(t, dir, frames)

	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		JournalDir:        dir,
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Query with time range
	from := base.Add(5 * time.Second).Format(time.RFC3339)
	to := base.Add(15 * time.Second).Format(time.RFC3339)
	resp, err := http.Get(ts.URL + "/history?from=" + from + "&to=" + to)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result []historyFrame
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Should have frames from second 5 to second 15 (11 frames)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	for _, f := range result {
		if f.PGN != 129025 {
			t.Errorf("PGN = %d, want 129025", f.PGN)
		}
	}
}

func TestHistoryPGNFilter(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	var frames []RxFrame
	for i := range 10 {
		pgn := uint32(129025)
		if i%2 == 1 {
			pgn = 129026
		}
		frames = append(frames, makeFrameWithSeq(
			base.Add(time.Duration(i)*time.Second),
			pgn, 10,
			[]byte{byte(i + 1), 0, 0, 0, 0, 0, 0, 0},
			uint64(i+1),
		))
	}
	writeJournalFrames(t, dir, frames)

	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		JournalDir:        dir,
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	from := base.Format(time.RFC3339)
	to := base.Add(20 * time.Second).Format(time.RFC3339)
	resp, err := http.Get(ts.URL + "/history?from=" + from + "&to=" + to + "&pgn=129025")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result []historyFrame
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	for _, f := range result {
		if f.PGN != 129025 {
			t.Errorf("expected only PGN 129025, got %d", f.PGN)
		}
	}
}

func TestHistoryMissingFrom(t *testing.T) {
	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		JournalDir:        t.TempDir(),
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/history")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHistoryNoJournal(t *testing.T) {
	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		// No JournalDir
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/history?from=2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHistoryEmptyDir(t *testing.T) {
	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		JournalDir:        t.TempDir(),
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/history?from=2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result []historyFrame
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d frames", len(result))
	}
}

func TestHistoryDownsample(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Write 20 frames at 100ms intervals (2 seconds total)
	var frames []RxFrame
	for i := range 20 {
		frames = append(frames, makeFrameWithSeq(
			base.Add(time.Duration(i)*100*time.Millisecond),
			129025, 10,
			[]byte{byte(i + 1), 0, 0, 0, 0, 0, 0, 0},
			uint64(i+1),
		))
	}
	writeJournalFrames(t, dir, frames)

	b := NewBroker(BrokerConfig{
		RingSize:          16,
		MaxBufferDuration: time.Minute,
		JournalDir:        dir,
	})
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	from := base.Format(time.RFC3339)
	to := base.Add(3 * time.Second).Format(time.RFC3339)

	// Without downsampling: should get all 20 frames
	resp, err := http.Get(ts.URL + "/history?from=" + from + "&to=" + to)
	if err != nil {
		t.Fatal(err)
	}
	var allFrames []historyFrame
	_ = json.NewDecoder(resp.Body).Decode(&allFrames)
	_ = resp.Body.Close()

	// With 1s downsampling: should get at most 2-3 frames (one per second bucket)
	resp, err = http.Get(ts.URL + "/history?from=" + from + "&to=" + to + "&interval=1s")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var downsampled []historyFrame
	if err := json.NewDecoder(resp.Body).Decode(&downsampled); err != nil {
		t.Fatal(err)
	}

	if len(downsampled) >= len(allFrames) {
		t.Errorf("downsampled (%d) should be fewer than all frames (%d)", len(downsampled), len(allFrames))
	}
	if len(downsampled) == 0 {
		t.Error("expected non-empty downsampled result")
	}
	// With 1s interval over 2s of data, expect ~2 frames
	if len(downsampled) > 3 {
		t.Errorf("expected at most 3 downsampled frames, got %d", len(downsampled))
	}
}
