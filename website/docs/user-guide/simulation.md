---
sidebar_position: 2
title: Simulation & Testing
---

# Simulation & Testing

`lplex simulate` replays recorded journal files through a full HTTP server, simulating a live boat without a CAN bus. This is invaluable for development, integration testing, and demos.

Unlike `lplex dump --file` (which outputs frames to stdout), `simulate` starts a real lplex HTTP server that clients can connect to exactly as they would a live boat.

## Basic usage

```bash
# Real-time replay on default port 8090
lplex simulate --file recording.lpj

# 10x speed on a custom port
lplex simulate --file recording.lpj --speed 10 --port 8080

# As fast as possible (no timing delays)
lplex simulate --file recording.lpj --speed 0
```

All standard HTTP endpoints are available: `/events` (SSE), `/ws` (WebSocket), `/devices`, `/values`, `/values/decoded`, and `/history`. The `/send` and `/query` endpoints are disabled since there is no real CAN bus.

## Replaying a directory of journal files

Use `--dir` to replay all `.lpj` files in a directory, sorted by filename. This is useful when you have multiple journal files from a recording session (e.g. rotated journal files from a long voyage).

```bash
# Replay all journals in a directory at real-time speed
lplex simulate --dir /var/lib/lplex/journal/

# Replay at 10x speed
lplex simulate --dir /var/lib/lplex/journal/ --speed 10
```

Files are played in lexicographic order by filename. Since lplex journal files include timestamps in their names (e.g. `lplex-20240315T100000.000Z.lpj`), alphabetical order matches chronological order. Timing gaps between files are preserved — if the last frame in file A is at 10:05:00 and the first frame in file B is at 10:05:02, the replay sleeps for 2 seconds (adjusted by `--speed`).

## Exit behavior

By default, `simulate` keeps the server running after replay finishes so clients can still query `/devices`, `/values`, etc. Use `--exit-when-done` to exit automatically when all frames have been replayed:

```bash
# Exit after replay completes
lplex simulate --file recording.lpj --exit-when-done

# Useful in CI/scripts — replay at max speed and exit
lplex simulate --dir ./test-data/ --speed 0 --exit-when-done
```

## Docker

The lplex Docker image includes both `lplex-server` and the `lplex` client CLI. To run a simulation, override the entrypoint to use the `lplex` binary and mount your journal directory as a volume:

```bash
docker run --rm \
  -v /path/to/journals:/data:ro \
  -p 8090:8090 \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex:latest \
  simulate --dir /data --exit-when-done
```

This starts an HTTP server on port 8090 that replays all `.lpj` files in the mounted directory, then exits when done.

### Common Docker patterns

**Real-time replay for client development:**

```bash
docker run --rm \
  -v ./recordings:/data:ro \
  -p 8090:8090 \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex:latest \
  simulate --dir /data
```

Connect your client to `http://localhost:8090` — it behaves identically to a live boat.

**Fast replay for integration testing (CI):**

```bash
docker run --rm \
  -v ./test-fixtures:/data:ro \
  -p 8090:8090 \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex:latest \
  simulate --dir /data --speed 0 --exit-when-done
```

The container exits with code 0 after all frames are replayed. Combine with your test runner to verify client behavior against known data.

**Looping replay for long-running demos:**

```bash
docker run -d --name lplex-demo \
  -v ./demo-data:/data:ro \
  -p 8090:8090 \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex:latest \
  simulate --dir /data --loop

# Clients connect to http://localhost:8090
# Stop with: docker stop lplex-demo
```

**Docker Compose for integration testing:**

```yaml
services:
  lplex:
    image: ghcr.io/sixfathoms/lplex:latest
    entrypoint: ["/lplex"]
    command: ["simulate", "--dir", "/data", "--speed", "0", "--exit-when-done"]
    volumes:
      - ./test-fixtures:/data:ro
    ports:
      - "8090:8090"

  my-app:
    build: .
    depends_on:
      lplex:
        condition: service_started
    environment:
      LPLEX_URL: http://lplex:8090
```

## Flags reference

| Flag | Default | Description |
|---|---|---|
| `--file` | — | Path to a single `.lpj` journal file |
| `--dir` | — | Directory of `.lpj` files to replay (sorted by name) |
| `--port` | `8090` | HTTP listen port |
| `--speed` | `1.0` | Playback speed: `0` = max throughput, `1.0` = real-time, `10` = 10x faster |
| `--start` | — | Seek to this time (RFC 3339) before playing |
| `--loop` | `false` | Restart from beginning when all files end |
| `--exit-when-done` | `false` | Exit after replay instead of keeping the server running |
| `--ring-size` | `65536` | Ring buffer size for the broker (must be power of 2) |
| `--slots` | — | Pre-configured client slots as JSON (see below) |

`--file` and `--dir` are mutually exclusive; one is required.

## Pre-configured client slots

Slots pre-create named buffered sessions at startup, so clients can connect to `GET /clients/{id}/events` immediately without first calling `PUT /clients/{id}`. This is useful for integration tests where the test client needs a known session ID.

### Via `--slots` flag (simulate)

Pass a JSON array of slot definitions:

```bash
lplex simulate --dir ./test-data/ --speed 0 --exit-when-done \
  --slots '[{"id":"test-client","buffer_timeout":"PT5M"},{"id":"nav","buffer_timeout":"PT2M","filter":{"pgn":[129025,129026]}}]'
```

### Via HOCON config (lplex-server)

```hocon
clients {
  slots = [
    {
      id = "dashboard"
      buffer-timeout = "PT5M"
    }
    {
      id = "nav-only"
      buffer-timeout = "PT2M"
      filter {
        pgn = [129025, 129026, 129029]
        bus = ["can0"]
      }
    }
  ]
}
```

### Docker with slots

```bash
docker run --rm -p 8090:8090 \
  -v ./journals:/data:ro \
  --entrypoint /lplex \
  ghcr.io/sixfathoms/lplex:latest \
  simulate --dir /data --speed 0 --exit-when-done \
  --slots '[{"id":"test","buffer_timeout":"PT5M"}]'
```

Each slot supports the same filter options as `PUT /clients/{id}`:

| Field | Type | Description |
|---|---|---|
| `id` | string | Session ID (1-64 alphanumeric, hyphens, underscores) |
| `buffer_timeout` | string | Buffer duration (ISO 8601, e.g. `PT5M`) |
| `filter.pgn` | `[uint32]` | Include only these PGNs |
| `filter.exclude_pgn` | `[uint32]` | Exclude these PGNs |
| `filter.manufacturer` | `[string]` | Filter by manufacturer name or code |
| `filter.instance` | `[uint8]` | Filter by device instance |
| `filter.name` | `[string]` | Include CAN NAMEs (hex) |
| `filter.exclude_name` | `[string]` | Exclude CAN NAMEs (hex) |
| `filter.bus` | `[string]` | Filter by CAN bus name |

## Integration testing patterns

### Deterministic test data

Record a journal from a real boat, then replay it in tests. The same journal always produces the same sequence of frames, making tests deterministic.

```bash
# Record from a live boat
lplex dump --server http://boat:8089 --journal ./test-data/

# Replay in tests
lplex simulate --dir ./test-data/ --speed 0 --exit-when-done --port 8090 &
LPLEX_PID=$!

# Run your test suite against the simulated server
pytest --lplex-url http://localhost:8090
wait $LPLEX_PID
```

### Wait for the server to be ready

When scripting, wait for the HTTP server to accept connections before running tests:

```bash
# Start simulate in the background
lplex simulate --dir ./test-data/ --speed 0 --exit-when-done --port 8090 &

# Wait for the server to be ready
until curl -sf http://localhost:8090/healthz > /dev/null 2>&1; do
  sleep 0.1
done

# Now run tests
npm test
```

### Testing decoded PGN values

Connect to the simulated server with `decode=true` to test PGN decoding end-to-end:

```bash
# Start a simulation
lplex simulate --file recording.lpj --speed 0 &

# Query decoded values
curl -s http://localhost:8090/values/decoded | jq '.[] | select(.pgn == 130310)'
```
