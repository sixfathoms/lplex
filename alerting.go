package lplex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// AlertType identifies the kind of alert.
type AlertType string

const (
	AlertBusSilence              AlertType = "bus_silence"
	AlertBusResumed              AlertType = "bus_resumed"
	AlertDeviceRemoved           AlertType = "device_removed"
	AlertReplicationDisconnected AlertType = "replication_disconnected"
	AlertReplicationReconnected  AlertType = "replication_reconnected"
)

// AlertEvent is the payload sent to the webhook endpoint.
type AlertEvent struct {
	Timestamp  time.Time       `json:"timestamp"`
	Type       AlertType       `json:"type"`
	Severity   string          `json:"severity"` // "warning", "info"
	Summary    string          `json:"summary"`
	InstanceID string          `json:"instance_id,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// AlertManagerConfig configures the alert manager.
type AlertManagerConfig struct {
	WebhookURL  string        // HTTP POST target (empty = alerting disabled)
	Timeout     time.Duration // HTTP request timeout (default 5s)
	DedupWindow time.Duration // suppress duplicate alerts within this window (default 5m)
	InstanceID  string        // identifies the boat/instance
	Logger      *slog.Logger
}

// AlertManager dispatches alert events to a webhook endpoint.
// Alerts are sent asynchronously via a buffered channel to avoid blocking
// the caller. Duplicate alerts of the same type are suppressed within the
// dedup window.
type AlertManager struct {
	cfg    AlertManagerConfig
	client *http.Client
	ch     chan AlertEvent
	logger *slog.Logger

	mu      sync.Mutex
	lastSent map[AlertType]time.Time // dedup tracking
}

// NewAlertManager creates an alert manager. Call Run to start dispatching.
// Returns nil if webhookURL is empty (alerting disabled).
func NewAlertManager(cfg AlertManagerConfig) *AlertManager {
	if cfg.WebhookURL == "" {
		return nil
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.DedupWindow == 0 {
		cfg.DedupWindow = 5 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AlertManager{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		ch:       make(chan AlertEvent, 64),
		logger:   cfg.Logger.With("component", "alerting"),
		lastSent: make(map[AlertType]time.Time),
	}
}

// Fire queues an alert for delivery. Non-blocking; drops the alert if the
// send buffer is full.
func (am *AlertManager) Fire(event AlertEvent) {
	if am == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.InstanceID == "" {
		event.InstanceID = am.cfg.InstanceID
	}

	// Dedup: suppress duplicate alerts within the window.
	am.mu.Lock()
	if last, ok := am.lastSent[event.Type]; ok && time.Since(last) < am.cfg.DedupWindow {
		am.mu.Unlock()
		return
	}
	am.lastSent[event.Type] = time.Now()
	am.mu.Unlock()

	select {
	case am.ch <- event:
	default:
		am.logger.Warn("alert buffer full, dropping alert", "type", event.Type)
	}
}

// Run processes queued alerts until ctx is cancelled.
func (am *AlertManager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-am.ch:
			am.send(ctx, event)
		}
	}
}

func (am *AlertManager) send(ctx context.Context, event AlertEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		am.logger.Error("failed to marshal alert", "type", event.Type, "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, am.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		am.logger.Error("failed to create alert request", "type", event.Type, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := am.client.Do(req)
	if err != nil {
		am.logger.Warn("alert webhook failed", "type", event.Type, "error", err)
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 300 {
		am.logger.Warn("alert webhook returned non-success", "type", event.Type, "status", resp.StatusCode)
		return
	}
	am.logger.Info("alert sent", "type", event.Type, "severity", event.Severity)
}

// FireBusSilence is a convenience method for bus silence alerts.
func (am *AlertManager) FireBusSilence(lastFrame time.Time, elapsed time.Duration) {
	details, _ := json.Marshal(struct {
		LastFrameTime   time.Time `json:"last_frame_time"`
		ElapsedSeconds  float64   `json:"elapsed_seconds"`
	}{lastFrame, elapsed.Seconds()})

	am.Fire(AlertEvent{
		Type:     AlertBusSilence,
		Severity: "warning",
		Summary:  fmt.Sprintf("CAN bus silent for %s", elapsed.Truncate(time.Second)),
		Details:  details,
	})
}

// FireBusResumed is a convenience method for bus activity resumed alerts.
func (am *AlertManager) FireBusResumed(silenceDuration time.Duration) {
	details, _ := json.Marshal(struct {
		SilenceDurationSeconds float64 `json:"silence_duration_seconds"`
	}{silenceDuration.Seconds()})

	am.Fire(AlertEvent{
		Type:     AlertBusResumed,
		Severity: "info",
		Summary:  fmt.Sprintf("CAN bus activity resumed after %s", silenceDuration.Truncate(time.Second)),
		Details:  details,
	})
}

// FireDeviceRemoved is a convenience method for device removal alerts.
func (am *AlertManager) FireDeviceRemoved(src uint8, dev *Device) {
	d := struct {
		Source       uint8  `json:"source"`
		Manufacturer string `json:"manufacturer,omitempty"`
		ModelID      string `json:"model_id,omitempty"`
	}{Source: src}
	if dev != nil {
		d.Manufacturer = dev.Manufacturer
		d.ModelID = dev.ModelID
	}
	details, _ := json.Marshal(d)

	summary := fmt.Sprintf("Device removed: source %d", src)
	if dev != nil && dev.Manufacturer != "" {
		summary = fmt.Sprintf("Device removed: %s (source %d)", dev.Manufacturer, src)
	}

	am.Fire(AlertEvent{
		Type:     AlertDeviceRemoved,
		Severity: "warning",
		Summary:  summary,
		Details:  details,
	})
}

// FireReplicationDisconnected alerts on replication disconnect.
func (am *AlertManager) FireReplicationDisconnected(reason string) {
	details, _ := json.Marshal(struct {
		Reason string `json:"reason"`
	}{reason})

	am.Fire(AlertEvent{
		Type:     AlertReplicationDisconnected,
		Severity: "warning",
		Summary:  "Replication disconnected: " + reason,
		Details:  details,
	})
}

// FireReplicationReconnected alerts when replication reconnects.
func (am *AlertManager) FireReplicationReconnected() {
	am.Fire(AlertEvent{
		Type:     AlertReplicationReconnected,
		Severity: "info",
		Summary:  "Replication reconnected",
	})
}
