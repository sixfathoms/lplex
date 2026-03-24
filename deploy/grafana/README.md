# Grafana Dashboard

Ready-made Grafana dashboard for monitoring lplex.

## Import

1. In Grafana, go to **Dashboards → Import**
2. Upload `lplex-dashboard.json` or paste its contents
3. Select your Prometheus data source
4. Click **Import**

## Prerequisites

- Prometheus scraping `http://lplex-server:8089/metrics`
- Add to your `prometheus.yml`:
  ```yaml
  scrape_configs:
    - job_name: lplex
      static_configs:
        - targets: ['lplex-server:8089']
  ```

## Panels

| Panel | Metrics Used |
|---|---|
| Frame Rate | `rate(lplex_broker_head_seq)` |
| Ring Buffer Utilization | `lplex_ring_buffer_utilization` |
| Connections | `lplex_active_sessions`, `_subscribers`, `_consumers` |
| Devices | `lplex_devices_total`, `_added_total`, `_removed_total` |
| Consumer Lag | `lplex_consumer_max_lag_seqs` |
| Bus Silence | `lplex_last_frame_timestamp_seconds` |
| Journal Drops | `lplex_journal_drops_total` |
| Journal Write Rate | `lplex_journal_bytes_written_total`, `_blocks_written_total` |
| Journal Write Latency | `lplex_journal_last_block_write_duration_seconds` |
| Replication Status | `lplex_replication_connected` |
| Replication Lag | `lplex_replication_live_lag_seqs`, `_backfill_remaining_seqs` |
| Replication Throughput | `lplex_replication_live_frames_sent_total`, `_backfill_bytes_sent_total` |
| Replication Reconnects | `lplex_replication_reconnects_total` |
