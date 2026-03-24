# Operations Runbook

Common operational tasks for lplex-server and lplex-cloud.

## Table of Contents

- [Service Management](#service-management)
- [Upgrading lplex-server](#upgrading-lplex-server)
- [Rotating TLS Certificates](#rotating-tls-certificates)
- [Journal Corruption Recovery](#journal-corruption-recovery)
- [Debugging Replication Lag](#debugging-replication-lag)
- [CAN Bus Troubleshooting](#can-bus-troubleshooting)
- [Disk Space Management](#disk-space-management)
- [Performance Troubleshooting](#performance-troubleshooting)

---

## Service Management

### Check status
```bash
systemctl status lplex-server
journalctl -u lplex-server -f          # follow logs
journalctl -u lplex-server --since "1h ago"  # last hour
```

### Restart
```bash
systemctl restart lplex-server
```

### Reload config (no restart)
```bash
# Edit config, then:
kill -HUP $(pidof lplex-server)
# or:
systemctl reload lplex-server
```

Hot-reloadable settings: send rules, API key, read-only mode, rate limits. Settings requiring restart: interfaces, port, ring size, journal dir, replication.

### Validate config before applying
```bash
lplex-server -validate-config -config /etc/lplex/lplex-server.conf
```

---

## Upgrading lplex-server

### Via .deb package
```bash
# Download new version
curl -fsSL "https://github.com/sixfathoms/lplex/releases/download/v0.2.0/lplex_0.2.0_linux_$(dpkg --print-architecture).deb" -o /tmp/lplex.deb

# Install (preserves config files)
sudo dpkg -i /tmp/lplex.deb

# Restart
sudo systemctl restart lplex-server

# Verify
lplex-server -version
systemctl status lplex-server
```

### Via APT repository
```bash
sudo apt update
sudo apt upgrade lplex
sudo systemctl restart lplex-server
```

### Rollback
```bash
# If the new version has issues, install the previous .deb:
sudo dpkg -i /tmp/lplex_previous.deb
sudo systemctl restart lplex-server
```

---

## Rotating TLS Certificates

### Boat-side (replication client)
```bash
# 1. Place new cert files
sudo cp new-client.crt /etc/lplex/client.crt
sudo cp new-client.key /etc/lplex/client.key

# 2. Restart to pick up new certs
sudo systemctl restart lplex-server

# 3. Verify replication reconnects
journalctl -u lplex-server | grep -i "handshake"
```

### Cloud-side (replication server)
```bash
# 1. Update the CA certificate if the issuer changed
sudo cp new-ca.crt /etc/lplex-cloud/ca.crt

# 2. Update server cert if needed
sudo cp new-server.crt /etc/lplex-cloud/server.crt
sudo cp new-server.key /etc/lplex-cloud/server.key

# 3. Restart
sudo systemctl restart lplex-cloud

# 4. Verify boats reconnect
journalctl -u lplex-cloud | grep -i "handshake"
```

### Kubernetes (Helm)
```bash
# Update the TLS secret
kubectl create secret tls lplex-tls \
  --cert=new-server.crt --key=new-server.key \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart pods to pick up new secret
kubectl rollout restart deployment lplex-cloud
```

---

## Journal Corruption Recovery

### Detect corruption
```bash
# Verify all journal files
lplex verify /var/log/lplex/*.lpj

# Inspect a specific file
lplex inspect /var/log/lplex/nmea2k-20250615T120000.000Z.lpj
```

### Recover from corrupt files
```bash
# 1. Stop the service
sudo systemctl stop lplex-server

# 2. Move corrupt files aside (don't delete yet)
mkdir -p /var/log/lplex/corrupt
mv /var/log/lplex/nmea2k-CORRUPT-FILE.lpj /var/log/lplex/corrupt/

# 3. Restart — a new journal file will be created
sudo systemctl start lplex-server

# 4. Verify frames are flowing
curl -s http://localhost:8089/devices | jq .
```

### Recover partial data from corrupt files
```bash
# lplex dump will read frames up to the corrupt block
lplex dump --file /var/log/lplex/corrupt/nmea2k-CORRUPT.lpj --json > recovered.jsonl
```

---

## Debugging Replication Lag

### Check replication status
```bash
# On the boat
curl -s http://localhost:8089/replication/status | jq .
```

Key fields:
- `connected`: should be `true`
- `cloud_cursor`: cloud's last acknowledged sequence
- `boat_head`: boat's current head sequence
- `holes`: number of gaps in cloud's data
- `reconnects`: number of reconnections (high = unstable link)

### Common issues

**Large lag (boat_head >> cloud_cursor)**
- Check network connectivity: `ping cloud.example.com`
- Check for bandwidth limitations on satellite/cellular link
- Monitor backfill progress: holes should decrease over time

**Frequent reconnects**
```bash
# Check replication logs
journalctl -u lplex-server | grep -i "replication\|reconnect\|handshake"
```
- Certificate expiry: check `openssl x509 -enddate -noout -in /etc/lplex/client.crt`
- Cloud-side rate limiting: check cloud logs for "rate limit exceeded"
- Network instability: check for TCP resets

**Holes not filling**
- Verify journal files exist: `ls -la /var/log/lplex/*.lpj`
- Check journal dir is configured: `grep journal /etc/lplex/lplex-server.conf`
- Backfill requires journal files — holes from before journaling was enabled cannot be filled

### Cloud-side debugging
```bash
# List instances
curl -s http://cloud:8080/instances | jq .

# Check specific instance
curl -s http://cloud:8080/instances/boat-001/status | jq .

# View replication events
curl -s http://cloud:8080/instances/boat-001/replication/events | jq .
```

---

## CAN Bus Troubleshooting

### Check interface status
```bash
ip -details link show can0
# Look for: state UP, bitrate 250000

# Quick diagnostic
lplex doctor --server http://localhost:8089
```

### No frames arriving
```bash
# 1. Check CAN interface is up
ip link show can0

# 2. Check for raw CAN traffic (requires can-utils)
candump can0 -t A

# 3. Check lplex-server sees the interface
journalctl -u lplex-server | grep "CAN reader"

# 4. Check bus silence alerts
curl -s http://localhost:8089/healthz | jq .
```

### Restart CAN interface
```bash
sudo ip link set down can0
sudo ip link set up can0
sudo systemctl restart lplex-server
```

---

## Disk Space Management

### Check journal disk usage
```bash
du -sh /var/log/lplex/
ls -lhS /var/log/lplex/*.lpj | head -10  # largest files
```

### Manual cleanup
```bash
# Delete files older than 30 days
find /var/log/lplex -name "*.lpj" -mtime +30 -delete
# Also clean up archive markers
find /var/log/lplex -name "*.archived" -mtime +30 -delete
```

### Adjust retention
Edit `/etc/lplex/lplex-server.conf`:
```hocon
journal {
  retention {
    max-age = P14D    # reduce from 30 to 14 days
    max-size = 5368709120  # 5 GB hard cap
  }
}
```
Then reload: `kill -HUP $(pidof lplex-server)` (note: retention changes require restart).

---

## Performance Troubleshooting

### Check broker metrics
```bash
curl -s http://localhost:8089/metrics | grep lplex_
```

Key metrics:
- `lplex_broker_frames_total`: total frames processed
- `lplex_broker_ring_entries`: current ring buffer usage
- `lplex_broker_journal_drops`: frames dropped due to full journal channel (should be 0)
- `lplex_broker_consumer_max_lag`: max frames a consumer is behind

### High consumer lag
Consumer lag means a client can't keep up with frame rate.
```bash
# Check active consumers
curl -s http://localhost:8089/metrics | grep consumer

# Increase ring buffer if consumers are falling behind
# Edit config: ring-size = 131072 (double the default)
```

### High memory usage
```bash
# Check process memory
ps aux | grep lplex-server

# Ring buffer memory ≈ ring-size × 150 bytes
# 65536 entries × 150 bytes ≈ 10 MB (normal)
# 131072 entries × 150 bytes ≈ 20 MB
```

### Journal write latency
```bash
curl -s http://localhost:8089/metrics | grep journal_write
# If write latency is high, check disk I/O:
iostat -x 1
```
