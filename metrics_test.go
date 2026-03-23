package lplex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandler(t *testing.T) {
	b := newTestBroker()
	go b.Run(context.Background())
	defer close(b.rxFrames)

	// Inject a frame so counters are nonzero.
	injectFrame(b, 60928, 1, make([]byte, 8))
	time.Sleep(50 * time.Millisecond) // let broker process

	handler := MetricsHandler(b, nil, nil)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type: got %q, want text/plain", ct)
	}

	body := w.Body.String()
	for _, metric := range []string{
		"lplex_frames_total",
		"lplex_ring_buffer_entries",
		"lplex_ring_buffer_capacity",
		"lplex_ring_buffer_utilization",
		"lplex_broker_head_seq",
		"lplex_active_sessions",
		"lplex_active_subscribers",
		"lplex_active_consumers",
		"lplex_consumer_max_lag_seqs",
		"lplex_devices_total",
		"lplex_devices_added_total",
		"lplex_devices_removed_total",
		"lplex_last_frame_timestamp_seconds",
		"lplex_journal_drops_total",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing metric %q in output", metric)
		}
	}

	// Should not contain replication or journal write metrics when callbacks are nil.
	if strings.Contains(body, "lplex_replication_connected") {
		t.Error("unexpected replication metric when replStatus is nil")
	}
	if strings.Contains(body, "lplex_journal_blocks_written_total") {
		t.Error("unexpected journal write metric when journalStats is nil")
	}
}

func TestMetricsHandlerWithReplication(t *testing.T) {
	b := newTestBroker()
	go b.Run(context.Background())
	defer close(b.rxFrames)

	replFn := func() *ReplicationStatus {
		return &ReplicationStatus{
			Connected:             true,
			LiveLag:               42,
			BackfillRemainingSeqs: 100,
			LastAck:               time.Now(),
			LiveFramesSent:        500,
			BackfillBlocksSent:    10,
			BackfillBytesSent:     2048,
			Reconnects:            3,
		}
	}

	handler := MetricsHandler(b, replFn, nil)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	for _, metric := range []string{
		"lplex_replication_connected 1",
		"lplex_replication_live_lag_seqs 42",
		"lplex_replication_backfill_remaining_seqs 100",
		"lplex_replication_last_ack_timestamp_seconds",
		"lplex_replication_live_frames_sent_total 500",
		"lplex_replication_backfill_blocks_sent_total 10",
		"lplex_replication_backfill_bytes_sent_total 2048",
		"lplex_replication_reconnects_total 3",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing replication metric %q in output", metric)
		}
	}
}

func TestMetricsHandlerWithJournalStats(t *testing.T) {
	b := newTestBroker()
	go b.Run(context.Background())
	defer close(b.rxFrames)

	journalFn := func() *JournalWriterStats {
		return &JournalWriterStats{
			BlocksWritten:          25,
			BytesWritten:           6553600,
			LastBlockWriteDuration: 500 * time.Microsecond,
		}
	}

	handler := MetricsHandler(b, nil, journalFn)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	for _, metric := range []string{
		"lplex_journal_blocks_written_total 25",
		"lplex_journal_bytes_written_total 6553600",
		"lplex_journal_last_block_write_duration_seconds",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing journal metric %q in output", metric)
		}
	}
}

func TestMetricsDeviceChurn(t *testing.T) {
	b := newConsumerTestBroker()
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	// Inject an address claim with a non-zero NAME to trigger device discovery.
	name := make([]byte, 8)
	name[0] = 0x01 // non-zero NAME
	injectFrame(b, 60928, 1, name)
	time.Sleep(100 * time.Millisecond)

	// Also drain the ISO request the broker sends after the address claim.
	drainTxFrame(b, 100*time.Millisecond)

	stats := b.Stats()
	if stats.DevicesAdded == 0 {
		t.Error("expected DevicesAdded > 0 after address claim")
	}
}
