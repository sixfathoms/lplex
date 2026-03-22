package lplex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAlertManagerFireAndDeliver(t *testing.T) {
	received := make(chan AlertEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event AlertEvent
		if err := json.Unmarshal(body, &event); err != nil {
			t.Errorf("unmarshal: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	am := NewAlertManager(AlertManagerConfig{
		WebhookURL:  srv.URL,
		DedupWindow: time.Millisecond, // very short for testing
		Logger:      slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go am.Run(ctx)

	am.Fire(AlertEvent{
		Type:     AlertBusSilence,
		Severity: "warning",
		Summary:  "test alert",
	})

	select {
	case event := <-received:
		if event.Type != AlertBusSilence {
			t.Errorf("type = %q, want %q", event.Type, AlertBusSilence)
		}
		if event.Severity != "warning" {
			t.Errorf("severity = %q, want %q", event.Severity, "warning")
		}
		if event.Summary != "test alert" {
			t.Errorf("summary = %q, want %q", event.Summary, "test alert")
		}
		if event.Timestamp.IsZero() {
			t.Error("timestamp should be set")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert")
	}
}

func TestAlertManagerDedup(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	am := NewAlertManager(AlertManagerConfig{
		WebhookURL:  srv.URL,
		DedupWindow: time.Minute, // long window
		Logger:      slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go am.Run(ctx)

	// Fire the same alert type 3 times
	for range 3 {
		am.Fire(AlertEvent{Type: AlertBusSilence, Severity: "warning", Summary: "test"})
	}
	time.Sleep(200 * time.Millisecond)

	// Only 1 should have been delivered
	if count != 1 {
		t.Errorf("expected 1 delivery (dedup), got %d", count)
	}
}

func TestAlertManagerDedupDifferentTypes(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	am := NewAlertManager(AlertManagerConfig{
		WebhookURL:  srv.URL,
		DedupWindow: time.Minute,
		Logger:      slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go am.Run(ctx)

	// Fire different alert types — all should be delivered
	am.Fire(AlertEvent{Type: AlertBusSilence, Severity: "warning", Summary: "a"})
	am.Fire(AlertEvent{Type: AlertBusResumed, Severity: "info", Summary: "b"})
	am.Fire(AlertEvent{Type: AlertDeviceRemoved, Severity: "warning", Summary: "c"})
	time.Sleep(200 * time.Millisecond)

	if count != 3 {
		t.Errorf("expected 3 deliveries (different types), got %d", count)
	}
}

func TestAlertManagerNilSafe(t *testing.T) {
	// nil AlertManager should not panic
	var am *AlertManager
	am.Fire(AlertEvent{Type: AlertBusSilence})
	am.FireBusSilence(time.Now(), time.Second)
	am.FireBusResumed(time.Second)
	am.FireDeviceRemoved(1, nil)
	am.FireReplicationDisconnected("test")
	am.FireReplicationReconnected()
}

func TestAlertManagerDisabledWhenNoURL(t *testing.T) {
	am := NewAlertManager(AlertManagerConfig{
		WebhookURL: "",
	})
	if am != nil {
		t.Error("expected nil AlertManager when webhook URL is empty")
	}
}

func TestAlertManagerInstanceID(t *testing.T) {
	received := make(chan AlertEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event AlertEvent
		_ = json.Unmarshal(body, &event)
		received <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	am := NewAlertManager(AlertManagerConfig{
		WebhookURL:  srv.URL,
		InstanceID:  "boat-1",
		DedupWindow: time.Millisecond,
		Logger:      slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go am.Run(ctx)

	am.Fire(AlertEvent{Type: AlertBusSilence, Severity: "warning", Summary: "test"})

	select {
	case event := <-received:
		if event.InstanceID != "boat-1" {
			t.Errorf("instance_id = %q, want %q", event.InstanceID, "boat-1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestFireBusSilenceConvenience(t *testing.T) {
	received := make(chan AlertEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event AlertEvent
		_ = json.Unmarshal(body, &event)
		received <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	am := NewAlertManager(AlertManagerConfig{
		WebhookURL:  srv.URL,
		DedupWindow: time.Millisecond,
		Logger:      slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go am.Run(ctx)

	am.FireBusSilence(time.Now().Add(-30*time.Second), 30*time.Second)

	select {
	case event := <-received:
		if event.Type != AlertBusSilence {
			t.Errorf("type = %q, want %q", event.Type, AlertBusSilence)
		}
		if event.Details == nil {
			t.Error("expected non-nil details")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
