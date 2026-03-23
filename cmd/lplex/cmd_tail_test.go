package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/sendpolicy"
	"github.com/sixfathoms/lplex/lplexc"
)

// startTestServer creates a broker + HTTP server on a random port.
func startTestServer(t *testing.T) (string, *lplex.Broker, func()) {
	t.Helper()

	logger := slog.Default()
	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: 10 * time.Minute,
		Logger:            logger,
		DeviceIdleTimeout: -1,
	})
	go broker.Run(context.Background())

	srv := lplex.NewServer(broker, logger, sendpolicy.SendPolicy{})
	srv.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	httpSrv := &http.Server{Handler: srv}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() { _ = httpSrv.Serve(ln) }()

	time.Sleep(50 * time.Millisecond)

	return fmt.Sprintf("http://127.0.0.1:%d", port), broker, func() {
		_ = httpSrv.Close()
		broker.CloseRx()
	}
}

func injectTestFrames(broker *lplex.Broker, count int) {
	base := time.Now()
	// Inject address claim so devices show up.
	claimData := make([]byte, 8)
	binary.LittleEndian.PutUint64(claimData, 0x00A0000000000001)
	broker.RxFrames() <- lplex.RxFrame{
		Timestamp: base,
		Header:    lplex.CANHeader{Priority: 2, PGN: 60928, Source: 1, Destination: 0xFF},
		Data:      claimData,
	}
	for i := range count {
		broker.RxFrames() <- lplex.RxFrame{
			Timestamp: base.Add(time.Duration(i+1) * time.Millisecond),
			Header:    lplex.CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{byte(i), 1, 2, 3, 4, 5, 6, 7},
		}
	}
}

func TestTailEphemeralReceivesFrames(t *testing.T) {
	serverURL, broker, cleanup := startTestServer(t)
	defer cleanup()

	injectTestFrames(broker, 20)
	time.Sleep(100 * time.Millisecond)

	// Subscribe ephemerally and verify we receive new frames.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	client := lplexc.NewClient(serverURL)
	sub, err := client.Subscribe(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()

	// Inject more frames while subscribed.
	go func() {
		time.Sleep(100 * time.Millisecond)
		for i := range 5 {
			broker.RxFrames() <- lplex.RxFrame{
				Timestamp: time.Now(),
				Header:    lplex.CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
				Data:      []byte{byte(i + 100), 1, 2, 3, 4, 5, 6, 7},
			}
		}
	}()

	// Read at least one event.
	ev, err := sub.Next()
	if err != nil {
		t.Fatalf("expected event, got error: %v", err)
	}
	if ev.Frame == nil {
		t.Fatal("expected frame event")
	}
	if ev.Frame.PGN != 129025 {
		t.Fatalf("expected PGN 129025, got %d", ev.Frame.PGN)
	}
}

func TestTailLastReplaysHistory(t *testing.T) {
	serverURL, broker, cleanup := startTestServer(t)
	defer cleanup()

	injectTestFrames(broker, 50)
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	client := lplexc.NewClient(serverURL)

	// Create session and set cursor to replay last 10.
	clientID := fmt.Sprintf("test-tail-%d", time.Now().UnixMilli()%10000)
	session, err := client.CreateSession(ctx, lplexc.SessionConfig{
		ClientID:      clientID,
		BufferTimeout: "PT30S",
	})
	if err != nil {
		t.Fatal(err)
	}

	info := session.Info()
	if info.Seq < 10 {
		t.Fatalf("expected seq >= 10, got %d", info.Seq)
	}

	startSeq := info.Seq - 10
	if err := session.Ack(ctx, startSeq); err != nil {
		t.Fatal(err)
	}

	sub, err := session.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()

	var received int
	for received < 10 {
		ev, err := sub.Next()
		if err != nil {
			break
		}
		if ev.Frame != nil {
			received++
		}
	}

	if received < 10 {
		t.Fatalf("expected at least 10 replayed frames, got %d", received)
	}
	t.Logf("received %d frames via --last replay", received)
}

func TestTailAutoReconnect(t *testing.T) {
	serverURL, broker, cleanup := startTestServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	client := lplexc.NewClient(serverURL)
	devices := newDeviceMap()
	var lastSeq atomic.Uint64

	errCh := make(chan error, 1)
	go func() {
		errCh <- runEphemeral(ctx, client, true, false, false, nil, nil, devices, &lastSeq)
	}()

	// Inject a frame.
	time.Sleep(100 * time.Millisecond)
	broker.RxFrames() <- lplex.RxFrame{
		Timestamp: time.Now(),
		Header:    lplex.CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
		Data:      []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil && !strings.Contains(err.Error(), "context") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runEphemeral didn't return after cancel")
	}
}

func TestTailCleanExit(t *testing.T) {
	serverURL, _, cleanup := startTestServer(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(t.Context())

	client := lplexc.NewClient(serverURL)
	devices := newDeviceMap()
	var lastSeq atomic.Uint64

	done := make(chan error, 1)
	go func() {
		done <- tailFollow(ctx, client, nil, nil, devices, &lastSeq, true)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tailFollow didn't exit after cancel")
	}
}
