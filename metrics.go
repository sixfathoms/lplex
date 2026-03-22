package lplex

import (
	"fmt"
	"io"
	"net/http"
)

// MetricsHandler returns an http.HandlerFunc that serves Prometheus-format
// metrics from the broker's Stats(). Optional callbacks provide journal and
// replication metrics for deployments that use those features.
func MetricsHandler(broker *Broker, replStatus func() *ReplicationStatus, journalStats func() *JournalWriterStats) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := broker.Stats()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, stats, replStatus, journalStats)
	}
}

func writeMetrics(w io.Writer, s BrokerStats, replStatus func() *ReplicationStatus, journalStats func() *JournalWriterStats) {
	// Frame throughput
	fmt.Fprintf(w, "# HELP lplex_frames_total Total CAN frames processed by the broker.\n")
	fmt.Fprintf(w, "# TYPE lplex_frames_total counter\n")
	fmt.Fprintf(w, "lplex_frames_total %d\n", s.FramesTotal)

	// Ring buffer
	fmt.Fprintf(w, "# HELP lplex_ring_buffer_entries Current number of entries in the ring buffer.\n")
	fmt.Fprintf(w, "# TYPE lplex_ring_buffer_entries gauge\n")
	fmt.Fprintf(w, "lplex_ring_buffer_entries %d\n", s.RingEntries)

	fmt.Fprintf(w, "# HELP lplex_ring_buffer_capacity Total capacity of the ring buffer.\n")
	fmt.Fprintf(w, "# TYPE lplex_ring_buffer_capacity gauge\n")
	fmt.Fprintf(w, "lplex_ring_buffer_capacity %d\n", s.RingCapacity)

	utilization := float64(0)
	if s.RingCapacity > 0 {
		utilization = float64(s.RingEntries) / float64(s.RingCapacity)
	}
	fmt.Fprintf(w, "# HELP lplex_ring_buffer_utilization Ring buffer utilization ratio (0-1).\n")
	fmt.Fprintf(w, "# TYPE lplex_ring_buffer_utilization gauge\n")
	fmt.Fprintf(w, "lplex_ring_buffer_utilization %.6f\n", utilization)

	// Head sequence
	fmt.Fprintf(w, "# HELP lplex_broker_head_seq Next sequence number to be assigned.\n")
	fmt.Fprintf(w, "# TYPE lplex_broker_head_seq gauge\n")
	fmt.Fprintf(w, "lplex_broker_head_seq %d\n", s.HeadSeq)

	// Connections
	fmt.Fprintf(w, "# HELP lplex_active_sessions Number of buffered client sessions.\n")
	fmt.Fprintf(w, "# TYPE lplex_active_sessions gauge\n")
	fmt.Fprintf(w, "lplex_active_sessions %d\n", s.ActiveSessions)

	fmt.Fprintf(w, "# HELP lplex_active_subscribers Number of ephemeral SSE subscribers.\n")
	fmt.Fprintf(w, "# TYPE lplex_active_subscribers gauge\n")
	fmt.Fprintf(w, "lplex_active_subscribers %d\n", s.ActiveSubscribers)

	fmt.Fprintf(w, "# HELP lplex_active_consumers Number of pull-based consumers.\n")
	fmt.Fprintf(w, "# TYPE lplex_active_consumers gauge\n")
	fmt.Fprintf(w, "lplex_active_consumers %d\n", s.ActiveConsumers)

	// Consumer lag
	fmt.Fprintf(w, "# HELP lplex_consumer_max_lag_seqs Maximum consumer lag in sequences across all consumers.\n")
	fmt.Fprintf(w, "# TYPE lplex_consumer_max_lag_seqs gauge\n")
	fmt.Fprintf(w, "lplex_consumer_max_lag_seqs %d\n", s.ConsumerMaxLag)

	// Devices
	fmt.Fprintf(w, "# HELP lplex_devices_total Number of discovered NMEA 2000 devices.\n")
	fmt.Fprintf(w, "# TYPE lplex_devices_total gauge\n")
	fmt.Fprintf(w, "lplex_devices_total %d\n", s.DeviceCount)

	fmt.Fprintf(w, "# HELP lplex_devices_added_total Cumulative device discovery events.\n")
	fmt.Fprintf(w, "# TYPE lplex_devices_added_total counter\n")
	fmt.Fprintf(w, "lplex_devices_added_total %d\n", s.DevicesAdded)

	fmt.Fprintf(w, "# HELP lplex_devices_removed_total Cumulative device eviction events (idle timeout or address change).\n")
	fmt.Fprintf(w, "# TYPE lplex_devices_removed_total counter\n")
	fmt.Fprintf(w, "lplex_devices_removed_total %d\n", s.DevicesRemoved)

	// Last frame timestamp
	fmt.Fprintf(w, "# HELP lplex_last_frame_timestamp_seconds Unix timestamp of the most recent frame.\n")
	fmt.Fprintf(w, "# TYPE lplex_last_frame_timestamp_seconds gauge\n")
	if s.LastFrameTime.IsZero() {
		fmt.Fprintf(w, "lplex_last_frame_timestamp_seconds 0\n")
	} else {
		fmt.Fprintf(w, "lplex_last_frame_timestamp_seconds %.3f\n", float64(s.LastFrameTime.UnixNano())/1e9)
	}

	// Journal drops
	fmt.Fprintf(w, "# HELP lplex_journal_drops_total Frames dropped due to full journal channel.\n")
	fmt.Fprintf(w, "# TYPE lplex_journal_drops_total counter\n")
	fmt.Fprintf(w, "lplex_journal_drops_total %d\n", s.JournalDrops)

	// Journal write metrics (optional)
	if journalStats != nil {
		js := journalStats()
		if js != nil {
			fmt.Fprintf(w, "# HELP lplex_journal_blocks_written_total Total journal blocks flushed to disk.\n")
			fmt.Fprintf(w, "# TYPE lplex_journal_blocks_written_total counter\n")
			fmt.Fprintf(w, "lplex_journal_blocks_written_total %d\n", js.BlocksWritten)

			fmt.Fprintf(w, "# HELP lplex_journal_bytes_written_total Total bytes written to journal files.\n")
			fmt.Fprintf(w, "# TYPE lplex_journal_bytes_written_total counter\n")
			fmt.Fprintf(w, "lplex_journal_bytes_written_total %d\n", js.BytesWritten)

			fmt.Fprintf(w, "# HELP lplex_journal_last_block_write_duration_seconds Duration of the last journal block write.\n")
			fmt.Fprintf(w, "# TYPE lplex_journal_last_block_write_duration_seconds gauge\n")
			fmt.Fprintf(w, "lplex_journal_last_block_write_duration_seconds %.6f\n", js.LastBlockWriteDuration.Seconds())
		}
	}

	// Replication metrics (optional)
	if replStatus != nil {
		rs := replStatus()
		if rs != nil {
			connected := 0
			if rs.Connected {
				connected = 1
			}
			fmt.Fprintf(w, "# HELP lplex_replication_connected Whether the replication client is connected (0/1).\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_connected gauge\n")
			fmt.Fprintf(w, "lplex_replication_connected %d\n", connected)

			fmt.Fprintf(w, "# HELP lplex_replication_live_lag_seqs Number of sequences the cloud is behind the local head.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_live_lag_seqs gauge\n")
			fmt.Fprintf(w, "lplex_replication_live_lag_seqs %d\n", rs.LiveLag)

			fmt.Fprintf(w, "# HELP lplex_replication_backfill_remaining_seqs Number of sequences remaining in backfill holes.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_backfill_remaining_seqs gauge\n")
			fmt.Fprintf(w, "lplex_replication_backfill_remaining_seqs %d\n", rs.BackfillRemainingSeqs)

			fmt.Fprintf(w, "# HELP lplex_replication_last_ack_timestamp_seconds Unix timestamp of the last replication ACK.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_last_ack_timestamp_seconds gauge\n")
			if rs.LastAck.IsZero() {
				fmt.Fprintf(w, "lplex_replication_last_ack_timestamp_seconds 0\n")
			} else {
				fmt.Fprintf(w, "lplex_replication_last_ack_timestamp_seconds %.3f\n", float64(rs.LastAck.UnixNano())/1e9)
			}

			fmt.Fprintf(w, "# HELP lplex_replication_live_frames_sent_total Total frames sent via live replication stream.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_live_frames_sent_total counter\n")
			fmt.Fprintf(w, "lplex_replication_live_frames_sent_total %d\n", rs.LiveFramesSent)

			fmt.Fprintf(w, "# HELP lplex_replication_backfill_blocks_sent_total Total blocks sent via backfill stream.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_backfill_blocks_sent_total counter\n")
			fmt.Fprintf(w, "lplex_replication_backfill_blocks_sent_total %d\n", rs.BackfillBlocksSent)

			fmt.Fprintf(w, "# HELP lplex_replication_backfill_bytes_sent_total Total bytes sent via backfill stream.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_backfill_bytes_sent_total counter\n")
			fmt.Fprintf(w, "lplex_replication_backfill_bytes_sent_total %d\n", rs.BackfillBytesSent)

			fmt.Fprintf(w, "# HELP lplex_replication_reconnects_total Total replication reconnection attempts.\n")
			fmt.Fprintf(w, "# TYPE lplex_replication_reconnects_total counter\n")
			fmt.Fprintf(w, "lplex_replication_reconnects_total %d\n", rs.Reconnects)
		}
	}
}
