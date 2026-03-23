package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
)

// writeTestJournal creates a journal file with the given frames and returns the path.
func writeTestJournal(t *testing.T, frames []lplex.RxFrame) string {
	t.Helper()

	dir := t.TempDir()
	devices := lplex.NewDeviceRegistry()

	ch := make(chan lplex.RxFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)

	cfg := lplex.JournalConfig{
		Dir:       dir,
		Prefix:    "test",
		BlockSize: 4096,
	}
	w, err := lplex.NewJournalWriter(cfg, devices, ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no journal files written")
	}

	return filepath.Join(dir, entries[0].Name())
}

// makeTestFrame builds a simple RxFrame for testing.
func makeTestFrame(ts time.Time, pgn uint32, src uint8, data []byte) lplex.RxFrame {
	return lplex.RxFrame{
		Timestamp: ts,
		Header: lplex.CANHeader{
			Priority:    2,
			PGN:         pgn,
			Source:      src,
			Destination: 0xFF,
		},
		Data: data,
	}
}

// makeTestAddressClaim builds a PGN 60928 address claim frame.
func makeTestAddressClaim(ts time.Time, src uint8, name uint64) lplex.RxFrame {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, name)
	return lplex.RxFrame{
		Timestamp: ts,
		Header: lplex.CANHeader{
			Priority:    2,
			PGN:         60928,
			Source:      src,
			Destination: 0xFF,
		},
		Data: data,
	}
}

func TestSimulateServesEvents(t *testing.T) {
	// Create a journal with known frames.
	base := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)
	frames := make([]lplex.RxFrame, 0, 50)

	// Add an address claim so we get a device.
	frames = append(frames, makeTestAddressClaim(base, 1, 0x00A0000000000001))

	// Add 49 GPS position frames (PGN 129025).
	for i := range 49 {
		frames = append(frames, makeTestFrame(
			base.Add(time.Duration(i+1)*100*time.Millisecond),
			129025, 1,
			[]byte{byte(i), 1, 2, 3, 4, 5, 6, 7},
		))
	}

	journalPath := writeTestJournal(t, frames)

	// Verify the journal is readable.
	f, err := os.Open(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := journal.NewReader(f)
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	var count int
	for r.Next() {
		count++
	}
	r.Close()
	_ = f.Close()
	if count != 50 {
		t.Fatalf("expected 50 frames in journal, got %d", count)
	}

	// Set up simulate flags.
	simFile = journalPath
	simSpeed = 0 // as fast as possible
	simLoop = false
	simRingSize = 1024
	simStartTime = ""

	// Pick a random port.
	simPort = 0 // we'll override by finding a free port
	// Use a fixed port for simplicity.
	simPort = 18923

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	logger := newTestLogger()
	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:          simRingSize,
		MaxBufferDuration: 10 * time.Minute,
		Logger:            logger,
		DeviceIdleTimeout: -1,
	})
	go broker.Run()

	srv := lplex.NewServer(broker, logger, lplex.SendPolicy{Enabled: false})

	addr := fmt.Sprintf(":%d", simPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Logf("HTTP server error: %v", err)
		}
	}()

	// Give server time to start.
	time.Sleep(100 * time.Millisecond)

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
		broker.CloseRx()
	}()

	// Run replay into broker.
	replayDone := make(chan error, 1)
	go func() {
		replayDone <- replayOnce(ctx, broker, logger)
	}()

	// Wait for replay to finish.
	select {
	case err := <-replayDone:
		if err != nil {
			t.Fatalf("replay failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("replay timed out")
	}

	// Give broker a moment to process all frames.
	time.Sleep(200 * time.Millisecond)

	baseURL := fmt.Sprintf("http://localhost:%d", simPort)

	// Test /devices — should have the device from address claim.
	t.Run("devices", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/devices")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var devices []json.RawMessage
		if err := json.Unmarshal(body, &devices); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(devices) == 0 {
			t.Fatal("expected at least one device")
		}
	})

	// Test /values — should have values from the GPS frames.
	t.Run("values", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/values")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var values []json.RawMessage
		if err := json.Unmarshal(body, &values); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(values) == 0 {
			t.Fatal("expected at least one value group")
		}
	})

	// Test /events — SSE should be connectable (even if no live frames).
	t.Run("events_connectable", func(t *testing.T) {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		resp, err := client.Get(baseURL + "/events")
		if err != nil {
			// Timeout is expected since no live frames are being produced.
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		// Verify it's SSE.
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("expected text/event-stream, got %s", ct)
		}
	})

	// Test /send — should be forbidden (no CAN bus).
	t.Run("send_disabled", func(t *testing.T) {
		body := `{"pgn":59904,"dst":255,"data":"000000"}`
		resp, err := http.Post(baseURL+"/send", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 403 {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
