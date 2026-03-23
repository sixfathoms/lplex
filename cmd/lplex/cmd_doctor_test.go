package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/sendpolicy"
)

func TestDoctorCheckPlatform(t *testing.T) {
	r := checkPlatform()
	if r.status != "ok" {
		t.Fatalf("expected ok, got %s: %s", r.status, r.detail)
	}
}

func TestDoctorCheckJournalDir(t *testing.T) {
	t.Run("writable", func(t *testing.T) {
		dir := t.TempDir()
		r := checkJournalDir(dir)
		if r.status != "ok" {
			t.Fatalf("expected ok, got %s: %s", r.status, r.detail)
		}
	})

	t.Run("nonexistent", func(t *testing.T) {
		r := checkJournalDir("/tmp/lplex-doctor-nonexistent-" + t.Name())
		if r.status != "fail" {
			t.Fatalf("expected fail, got %s: %s", r.status, r.detail)
		}
	})

	t.Run("with_journal_files", func(t *testing.T) {
		dir := t.TempDir()
		// Create a fake .lpj file.
		if err := os.WriteFile(filepath.Join(dir, "test.lpj"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := checkJournalDir(dir)
		if r.status != "ok" {
			t.Fatalf("expected ok, got %s: %s", r.status, r.detail)
		}
	})
}

func TestDoctorCheckDiskSpace(t *testing.T) {
	r := checkDiskSpace(t.TempDir())
	if r.status == "fail" && r.detail == "" {
		t.Fatal("expected non-empty detail")
	}
	// Any status is fine as long as it doesn't panic.
	t.Logf("disk space: %s — %s", r.status, r.detail)
}

func TestDoctorCheckServerReachable(t *testing.T) {
	// Start a minimal HTTP server.
	logger := slog.Default()
	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:          1024,
		Logger:            logger,
		DeviceIdleTimeout: -1,
	})
	go broker.Run(context.Background())
	defer broker.CloseRx()

	srv := lplex.NewServer(broker, logger, sendpolicy.SendPolicy{})
	httpSrv := &http.Server{Handler: srv}

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() { _ = httpSrv.Serve(ln) }()
	defer func() { _ = httpSrv.Close() }()

	time.Sleep(50 * time.Millisecond)

	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	t.Run("reachable", func(t *testing.T) {
		r := checkServerReachable(serverURL)
		if r.status != "ok" {
			t.Fatalf("expected ok, got %s: %s", r.status, r.detail)
		}
	})

	t.Run("devices", func(t *testing.T) {
		r := checkServerDevices(serverURL)
		// No devices injected, so expect warn.
		if r.status != "warn" {
			t.Fatalf("expected warn (no devices), got %s: %s", r.status, r.detail)
		}
	})

	t.Run("health", func(t *testing.T) {
		// Register healthz (normally done by lplex-server main).
		srv.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
		r := checkServerHealth(serverURL)
		if r.status != "ok" {
			t.Fatalf("expected ok, got %s: %s", r.status, r.detail)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		r := checkServerReachable("http://127.0.0.1:1") // nothing listening
		if r.status != "fail" {
			t.Fatalf("expected fail, got %s: %s", r.status, r.detail)
		}
	})
}
