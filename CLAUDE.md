# lplex

CAN bus HTTP bridge for NMEA 2000. Reads raw CAN frames from one or more SocketCAN interfaces, reassembles fast-packets, tracks device discovery, and streams frames to clients over SSE with session management, filtering, and replay. Supports multiple CAN buses simultaneously (e.g. can0 + can1 for separate engine/navigation buses) with per-bus filtering. Supports cloud replication over gRPC for remote access to boat data.

## Prerequisites

Requires Go 1.25+.

```bash
# macOS
brew install go

# Linux (Debian/Ubuntu) — check that the repo version is 1.25+, otherwise use https://go.dev/dl/
sudo apt install -y golang-go

# Other / manual
# Download from https://go.dev/dl/
```

## Build & Test

```bash
go build ./...                  # build all
go build -o lplex-server ./cmd/lplex-server   # build server
go build -o lplex-cloud ./cmd/lplex-cloud  # build cloud server
go build -o lplex ./cmd/lplex
go test ./... -v -count=1       # run tests
golangci-lint run               # lint (must pass before pushing)
make proto                      # regenerate protobuf (requires protoc + plugins)
go generate ./pgn/...           # regenerate PGN decoders from pgn/defs/*.pgn
```

## Release

Tags trigger GoReleaser via GitHub Actions, which builds binaries, .deb, Docker images, and pushes the Homebrew formula.

```bash
git tag -a v0.2.0 -m "v0.2.0" && git push origin v0.2.0
```

**Release artifacts**:
- `lplex-server` server: Linux amd64/arm64 only (no macOS, needs SocketCAN)
- `lplex-cloud` cloud server: Linux amd64/arm64
- `lplex` client: Linux + macOS (amd64/arm64)
- `.deb` package: bundles both binaries + systemd unit
- Docker: `ghcr.io/sixfathoms/lplex` (linux/amd64 + linux/arm64)
- Homebrew: `sixfathoms/tap/lplex` (client only, pushed to `sixfathoms/homebrew-tap`)

**Secrets**: `HOMEBREW_TAP_TOKEN` (fine-grained PAT with Contents read/write on `sixfathoms/homebrew-tap`)

## Deployment

- **Host**: `inuc1.local` (Linux x86_64)
- **Install**: `.deb` package from GitHub Releases
- **Service**: `lplex-server.service` (systemd)
- **Config**: `/etc/lplex/lplex-server.conf` (HOCON) or `/etc/default/lplex-server` (`LPLEX_ARGS="-interface can0 -port 8089"`)
- **CAN interface(s)**: `can0` (single bus via `-interface`), or `can0,can1` (multi-bus via `-interfaces`)
- **HTTP port**: 8089

## Architecture

Single-goroutine broker design, no locks in the hot path:

```
CANBus (SocketCANBus or LoopbackBus)
    |  one per interface (can0, can1, ...)
    |  ReadFrames goroutine reads CAN frames
    |  reassembles fast-packets (multi-frame PGNs)
    |  tags each frame with bus name (interface name)
    |
    v
rxFrames chan (shared across all buses)
    |
Broker goroutine (single writer, owns all state)
    |  assigns monotonic sequence numbers
    |  appends pre-serialized JSON to ring buffer (64k entries, power-of-2)
    |  updates device registry (PGN 60928 address claim, PGN 126996 product info)
    |  evicts stale devices (NAME-based dedup on address change, idle timeout expiry)
    |  fans out to ephemeral subscribers (with per-client filtering incl. bus)
    |  notifies pull-based consumers (non-blocking send to notify channels)
    |  sends ISO requests to discover new devices on the correct bus
    |  non-blocking send to journal channel (if enabled)
    |
    +---> ring buffer ([]ringEntry, lock-free writes, RLock for consumer reads)
    +---> DeviceRegistry (RWMutex, keyed by (bus, source) pair)
    +---> ValueStore (RWMutex, last frame per (bus, source, PGN))
    +---> consumers map (pull-based: cursor, filter, notify chan)
    +---> sessions map (buffered client metadata: cursor, filter, timeout)
    +---> subscribers map (ephemeral clients: channels, filters, no state)
    +---> journal chan (16384-entry buffer, optional)
    |
    v
HTTP Server (:8089)                    JournalWriter goroutine
    |                                       |  reads from journal chan
    +-- GET  /events                        |  encodes frames into 256KB blocks
    +-- PUT  /clients/{id}                  |  writes blocks with CRC32C checksums
    +-- GET  /clients/{id}/events           |  rotates files by duration/size
    +-- PUT  /clients/{id}/ack              |  tracks device table per block
    +-- POST /send                          v
    +-- POST /query                        .lpj journal files (v2, with BaseSeq)
    +-- GET  /devices
    +-- GET  /values
    +-- GET  /replication/status
                                       Consumer (pull-based reader)
CANBus.WriteFrames goroutine(s)             |  reads from tiered log:
    |  reads from per-bus TX chan          |  1. journal files (oldest)
    |  fragments fast-packets for TX      |  2. ring buffer (recent)
    |  writes to CAN bus (or loopback)    |  3. live notification (blocking wait)
    |  (TX dispatcher routes by bus)

ReplicationClient (optional)
    |  gRPC connection to cloud
    +-- Live goroutine: Consumer at head -> LiveFrame stream
    +-- Backfill goroutine: raw journal blocks -> Block stream
    +-- Reconnect loop: exponential backoff, re-handshake

JournalKeeper (optional)
    |  single goroutine per binary
    +-- OnRotate notifications from JournalWriter/BlockWriter
    +-- Periodic directory scans (5min)
    +-- Retention: max-age / min-keep / max-size with soft/hard thresholds
    +-- Soft zone: proactive archiving at soft-pct% of max-size
    +-- Overflow policy: delete-unarchived or pause-recording at hard cap
    +-- Archive: exec script with JSONL stdin/stdout protocol
    +-- Marker files (.archived) for archive state
    +-- Retry with exponential backoff (1min -> 1h cap)
    +-- Per-directory pause state with callback (OnPauseChange)
```

### Cloud Architecture

```
lplex-cloud process
  +-- gRPC server (:9443, mTLS)
  |     +-- Handshake: verify cert CN, load/create InstanceState
  |     +-- Live handler: feed frames into instance Broker (replica mode)
  |     +-- Backfill handler: write raw blocks via BlockWriter
  +-- HTTP server (:8080)
  |     +-- GET /instances
  |     +-- GET /instances/{id}/status
  |     +-- GET /instances/{id}/events (SSE)
  |     +-- GET /instances/{id}/devices
  |     +-- GET /instances/{id}/values
  |     +-- GET /instances/{id}/replication/events
  +-- InstanceManager
  |     +-- Per-instance state (InstanceState with HoleTracker + EventLog)
  |     +-- Lazy Broker lifecycle (~3MB RAM + 2 goroutines per active instance)
  |     +-- Persistent state (state.json per instance)
  |     +-- SetOnRotate: threads OnRotate callback to JournalWriter/BlockWriter
  +-- JournalKeeper (optional, shared across all instances)
        +-- DirFunc dynamically discovers instance journal dirs
        +-- Retention + archive applied per-instance directory
```

## Package Structure

| Package | Owns |
|---|---|
| `lplex` (root) | Public core: `Broker`, `Server`, `Consumer`, `CANBus` (interface), `SocketCANBus`, `LoopbackBus`, `CANReader`, `CANWriter`, `JournalWriter`, `DeviceRegistry`, `ValueStore`, `FastPacketAssembler`, `ReplicationClient`, `ReplicationServer`, `InstanceManager`, `HoleTracker`, `BlockWriter`, `EventLog`, filters, ring buffer. Embeddable by external Go services. |
| `cmd/lplex-server/` | Boat server: flag parsing, HOCON config, signal handling, mDNS registration, wires broker + CAN I/O + HTTP + optional replication |
| `cmd/lplex-cloud/` | Cloud server: gRPC + HTTP servers, InstanceManager, mTLS, HOCON config |
| `cmd/lplex/` | Multi-subcommand CLI: `dump` (streaming/replay), `tail` (simple live follow with optional replay), `dashboard` (interactive TUI with live data), `simulate` (journal replay through full HTTP server), `inspect` (journal inspection), `verify` (journal integrity verification), `doctor` (system diagnostics), `completion` (shell completion scripts for bash/zsh/fish/powershell), `devices`, `values`, `send`, `request`, `switches`. Uses cobra. Pretty-print, device table, PGN decoding (`--decode`), change tracking (`--changes`), display filter expressions (`--where`), auto-reconnect. |
| `cmd/pgngen/` | Code generator: reads `.pgn` DSL files, outputs Go structs/decoders/encoders, Protobuf, JSON Schema |
| `lplexc/` | Public Go client library: Subscribe, Devices, Send, Session, mDNS discovery |
| `sendpolicy/` | `SendPolicy`, `SendRule`, `PGNMatcher`, rule DSL parser and evaluator for `/send` and `/query` gating |
| `changetracker/` | `ChangeTracker`, `Config`, `ChangeEvent`, `DiffMethod`, `ByteMaskDiff`, `FieldToleranceDiff`, `SubKeyFunc`, `ChangeReplayer`. Per-(source, PGN, subkey) change tracking with idle timeout and tolerance-aware diffing. |
| `keeper/` | `JournalKeeper`, `KeeperConfig`, `KeeperDir`, `RotatedFile`, `ArchiveTrigger`, `OverflowPolicy`. Retention algorithm (max-age/min-keep/max-size with soft/hard thresholds), archive script execution, marker file tracking, per-directory pause state. |
| `filter/` | BPF-inspired display filter expressions for `lplex dump --where`. Lexer, recursive-descent parser, AST, and evaluator with reflection-based field access on decoded PGN structs. Supports header fields (`pgn`, `src`, `dst`, `prio`), decoded struct fields by JSON tag, and lookup sub-accessors (`register.name`). |
| `canbus/` | Public CAN ID parsing (`CANHeader`, `ParseCANID`, `BuildCANID`) and ISO NAME decoding |
| `journal/` | Public journal format: `Device`, `Reader` (with opt-in block prefetch via `EnablePrefetch`), `CompressionType`, block constants, length-prefixed string helpers |
| `pgn/` | Generated Go types for NMEA 2000 PGNs: structs, `Decode*`/`Encode` methods, `Registry` map (~123 entries, includes `PGNInfo` with `FastPacket`, `Interval`, `OnDemand`, `Draft`, `Tolerances` metadata). Name-only PGNs have `Decode: nil` but still carry description and metadata. Generated from `pgn/defs/*.pgn` via `go generate`. Generated output files (`pgn_gen.go`, `helpers_gen.go`, `proto/pgn.proto`, `schema.json`) are gitignored and must be regenerated after DSL changes. Hand-written helpers live alongside generated code (e.g. `victron.go` for register name lookup, `gnss_sats.go` for variable-length PGN 129540). AIS PGNs with field definitions: 129038 (Class A Position), 129039 (Class B Position), 129041 (AtoN), 129793 (UTC/Date), 129809 (Static Part A), 129810 (Static Part B). Table-driven packet tests in `packets_test.go` verify decode/encode against reference hex data (capture from `lplex dump --decode --json`). |
| `pgngen/` | PGN DSL parser and code generators (Go, Protobuf, JSON Schema). AST, bit-level field layout, scaling, enums, lookup tables, value-based dispatch for proprietary PGNs, `repeat=N` for repeated fields (generates slices or maps), field-level tolerances for change tracking (`tolerance=`), PGN-level metadata (`fast_packet`, `interval=`, `on_demand`, `draft`). Supports name-only PGNs (no braces = no field layout, `Decode: nil`) and unknown fields (`?` marker for observed but undocumented data). |
| `proto/replication/v1/` | Protobuf + gRPC definitions for replication protocol |
| `website/` | Docusaurus docs site, deployed to GitHub Pages. See [`website/CLAUDE.md`](website/CLAUDE.md) for structure and sync rules. |

### Root Package File Map

| File | Owns |
|---|---|
| `broker.go` | `Broker`, `BrokerConfig` (including `ReplicaMode`, `InitialHead`, `DeviceIdleTimeout`), `ClientSession`, `subscriber`, `EventFilter` (including `Buses` for multi-bus filtering), ring buffer, fan-out, session lifecycle, ephemeral subscriptions, consumer registry, journal feed, value store feed, device idle expiry, `device_removed` events |
| `errors.go` | Typed errors: `SequenceGapError`, `SessionNotFoundError`, `DeviceNotFoundError` |
| `consumer.go` | `Consumer`, `Frame`, `ErrFallenBehind`, pull-based tiered reader (journal -> ring -> live), journal fallback with file discovery, seq-based seeking, and block-level prefetch |
| `server.go` | `Server`, HTTP handlers, ephemeral + buffered SSE streaming, filter query param parsing, ISO 8601 duration parser, last-values endpoint, on-demand PGN query (`POST /query` via ISO Request PGN 59904), WebSocket upgrade bypass for compression/tracing wrappers |
| `websocket.go` | `HandleWebSocket`, bidirectional WebSocket transport (`GET /ws`). Streams filtered CAN frames to client, accepts `send` messages for CAN bus transmission. Same filter params as `/events`. |
| `decode_sse.go` | `injectDecoded`, adds decoded PGN fields to pre-serialized frame JSON. Used by `/events?decode=true`, `/ws?decode=true`, and `/history?decode=true`. |
| `history.go` | `handleHistory`, `GET /history` endpoint for querying journal files by time range with PGN/source/interval filters and optional decode. Returns JSON array of matching frames. |
| `mqtt.go` | `MQTTBridge`, `MQTTBridgeConfig`, publishes CAN frames to an MQTT broker. Subscribes to the broker's frame stream and publishes to `{prefix}/frames` topics. Auto-reconnect via paho MQTT client. |
| `can_bus.go` | `CANBus` interface (`ReadFrames`, `WriteFrames`, `Name`), `SocketCANBus` (wraps `CANReader`/`CANWriter`), `LoopbackBus` (in-memory echo for testing/macOS dev) |
| `can.go` | `CANReader` (SocketCAN rx + fast-packet reassembly), `CANWriter` (SocketCAN tx + fragmentation) |
| `canid.go` | Thin wrappers re-exporting `canbus.ParseCANID`, `canbus.BuildCANID` |
| `fastpacket.go` | `FastPacketAssembler`, `FragmentFastPacket`, `IsFastPacket` (checks `pgn.Registry` for `FastPacket` flag) |
| `devices.go` | `DeviceRegistry` (keyed by `(bus, source)` pair via `deviceKey`), `BusSource`, PGN 60928/126996 decoding, manufacturer lookup table, NAME-based eviction (same NAME on same bus at new source evicts old), idle expiry (`ExpireIdle`) |
| `journal_writer.go` | `JournalWriter`, `JournalWriterStats`, `JournalConfig` (including `OnRotate` callback), block encoding, zstd compression, block index, file rotation, device table tracking (with product info), atomic write metrics (blocks/bytes written, last block duration) |
| `alerting.go` | `AlertManager`, `AlertManagerConfig`, `AlertEvent`, `AlertType`, webhook-based alerting with dedup window. Fires on bus silence/resume, device removal, replication disconnect/reconnect |
| `tracing.go` | `TracingConfig`, `InitTracing`, `Tracer`, OpenTelemetry setup with OTLP/gRPC exporter, probabilistic sampling, W3C Trace Context propagation |
| `metrics.go` | `MetricsHandler`, Prometheus text format exposition. Broker metrics (frames, ring, consumers, devices, journal drops), journal write metrics (blocks, bytes, latency), replication metrics (lag, backfill progress, frames/blocks sent, reconnects) |
| `replication.go` | `SeqRange`, `HoleTracker`, `SyncState`, hole tracking algorithm |
| `replication_client.go` | `ReplicationClient`, `ReplicationClientConfig`, `ReplicationStatus`, live stream + backfill + reconnect loop, atomic replication counters (frames sent, blocks sent, reconnects) |
| `replication_events.go` | `EventLog`, `ReplicationEvent`, `ReplicationEventType`, per-instance ring buffer (1024 entries) for diagnostic events (live start/stop, backfill start/stop, block received, checkpoint) |
| `replication_server.go` | `ReplicationServer`, `InstanceManager` (including `SetOnRotate`), `InstanceState`, `InstanceStatus`, `InstanceSummary`, gRPC handlers (Handshake, Live, Backfill), mTLS verification, state persistence, event recording |
| `block_writer.go` | `BlockWriter`, `BlockWriterConfig` (including `OnRotate` callback), raw block append to journal files with file rotation |
| `values.go` | `ValueStore`, `DeviceValues`, `PGNValue`, last-seen frame tracking per (bus, source, PGN) tuple, snapshot with device resolution, `RemoveSource` for cleanup on device eviction |
| `doc.go` | Package documentation with embedding example |

## Client Modes

Two connection modes, ephemeral is the default:

### Ephemeral (default)

`GET /events` with optional query param filters (`?pgn=129025&manufacturer=Garmin&bus=can0`). No session, no replay, no ACK.

```
lplex dump --server http://inuc1.local:8089
```

### Buffered

`PUT /clients/{id}` with `buffer_timeout`, then `GET /clients/{id}/events`, and periodic `PUT /clients/{id}/ack`.

```
lplex dump --server http://inuc1.local:8089 --buffer-timeout PT5M
```

### PGN Decoding

`--decode` uses the `pgn.Registry` to decode known PGNs into human-readable field values. In terminal mode, decoded JSON appears on a continuation line below each frame. In JSON mode (`--json` or piped stdout), a `"decoded"` object is added to each frame. Works in all modes (ephemeral, buffered, journal replay).

```
lplex dump --decode
lplex dump --file recording.lpj --decode
```

## Cloud Replication

Boat-to-cloud data replication over gRPC with mTLS. Three RPCs: unary Handshake, bidirectional Live stream (realtime frames), bidirectional Backfill stream (raw journal blocks). See [docs/cloud-replication.md](docs/cloud-replication.md) for the full protocol specification.

Key design decisions:
- **Live-first**: On connect, live frames flow immediately. Backfill runs concurrently.
- **Raw block passthrough**: Backfill sends journal blocks byte-for-byte. Zero decompression/re-encoding.
- **Lazy Broker per instance**: Cloud starts a replica Broker on demand, stops after idle timeout.
- **Separate streams**: Live and backfill have independent flow control and lifecycle.
- **Hole tracking**: Sorted interval list tracks gaps. Handshake creates holes on reconnect, backfill fills them.

## Journal Retention and Archival

Both `lplex-server` and `lplex-cloud` support automatic journal cleanup and archival via `JournalKeeper`. Configured with `-journal-retention-*` and `-journal-archive-*` flags (or HOCON under `journal.retention.*` / `journal.archive.*`).

**Retention**: three knobs (max-age, min-keep, max-size) evaluated per directory. Priority: max-size overrides min-keep overrides max-age. Files sorted oldest-first; once a file is kept, all younger files are kept too. When `max-size` and archival are both configured, a soft/hard threshold system applies:

- **Normal** (total <= soft): standard age-based expiration, archive-then-delete
- **Soft zone** (soft < total <= hard): proactively queue oldest non-archived files for archive
- **Hard zone** (total > hard): expire files; if archives have failed, apply overflow policy

`soft-pct` (default 80) sets the soft threshold as a percentage of `max-size`. `overflow-policy` controls behavior when the hard cap is hit and archives have failed: `delete-unarchived` (default, prioritizes continued recording) or `pause-recording` (stops journal writes via `JournalWriter.SetPaused`, prioritizes archive completeness). Pause state is tracked per-directory and propagated via `OnPauseChange` callback.

**Archival**: user-provided script invoked with file paths as args and JSONL metadata on stdin. Per-file status ("ok"/"error") read from stdout. Failed files retry with exponential backoff (1min to 1h). Successful archives create a zero-byte `.archived` sidecar marker.

**Triggers**: `on-rotate` (archive immediately after rotation) or `before-expire` (archive only when about to be deleted by retention).

**Cloud**: single keeper goroutine manages all instance directories via `DirFunc`. `InstanceManager.SetOnRotate` threads the callback to each instance's JournalWriter and BlockWriter. `InstanceManager.SetInstancePaused` propagates pause state to the correct instance's JournalWriter.

## Key Design Decisions

- **Pull-based Consumer model**: buffered clients use a Kafka/Kinesis-style Consumer that reads from a tiered log (journal -> ring buffer -> live notification). Each consumer iterates at its own pace via `Next(ctx)`. Sessions store only metadata (cursor, filter, timeout); the HTTP handler creates a Consumer on each connection.
- **Pre-serialized JSON in ring buffer**: frames are serialized once when received, not per-client.
- **Resolved filters**: device-based filters are flattened to source addresses at consumer creation time, avoiding device registry lookups during iteration. Bus filters are also pre-resolved into a map for O(1) lookup.
- **Ephemeral subscribers are separate from sessions**: ephemeral `/events` uses push-based channels. Session-based `/clients/{id}/events` uses pull-based Consumer.
- **ISO Request on unknown source**: broker discovers devices automatically.
- **Journal v2 format**: blocks include `BaseSeq` for O(log n) sequence-based seeking. Reader supports both v1 (time-only) and v2 (time + seq). Consumer falls back to journal files when behind the ring buffer, returning `ErrFallenBehind` when data is unavailable.
- **Journal at broker level**: records reassembled frames (not raw CAN fragments), tapped via non-blocking channel send after fan-out. See `docs/format.md` for the `.lpj` binary format spec.
- **Block-level zstd compression**: journal blocks are compressed individually with zstd (default enabled, ~4x ratio at 256KB blocks). Each compressed block has a 12-byte header (BaseTime + CompressedLen). A block index at EOF provides O(1) offset lookup; forward-scan fallback handles crash-truncated files.
- **Broker replica mode**: `ReplicaMode` flag makes the broker honor external sequence numbers (from replication) instead of auto-incrementing. Skips ISO requests since there's no CAN bus.

## Documentation

The documentation site lives in `website/` (Docusaurus). See [`website/CLAUDE.md`](website/CLAUDE.md) for the full doc structure and build instructions.

**When making code changes, update the docs.** If a change affects CLI flags, HTTP API, config options, the PGN DSL, the Go client, journal format, or any user-facing behavior, update the corresponding page in `website/docs/` and the root `README.md`. Stale docs are worse than no docs.

## Conventions

- Run `golangci-lint run` before pushing (CI enforces it, config in `.golangci.yml`)
- Go 1.25+, modern patterns (enhanced ServeMux routing, slog, `slices`)
- No mocks in tests, real instances only
- CAN ID is 29-bit extended (NMEA 2000 only)
- All data encodings follow NMEA 2000: little-endian, 0xFF padding, fast-packet protocol
- AIS string fields use `@` (0x40) or space (0x20) padding per the ITU spec; use the `trim="@ "` DSL attribute to right-trim these at decode time (see `pgn/defs/ais.pgn`)
- Sequence numbers start at 1 (0 means "never ACK'd")
- Protobuf regeneration: `make proto` (requires `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`)
- PGN packet tests: add reference test vectors to `pgn/packets_test.go` (hex from `lplex dump --decode --json` → expected struct). Framework auto-verifies decode and encode round-trip.

## Configuration

lplex-server supports HOCON config files (`-config path` or auto-discovered from `./lplex-server.conf`, `/etc/lplex/lplex-server.conf`, with backward-compat fallback to `./lplex.conf`, `/etc/lplex/lplex.conf`). CLI flags always override config file values (detected via `flag.Visit()`). Config values are applied through `flag.Set()` so they share the same parsing path as CLI flags. The mapping from HOCON paths to flag names lives in `configToFlag` in `cmd/lplex-server/config.go`.

lplex-cloud uses the same pattern with `lplex-cloud.conf` (auto-discovered from `./lplex-cloud.conf`, `/etc/lplex-cloud/lplex-cloud.conf`). Mapping in `cmd/lplex-cloud/config.go`.

`lplex-server -validate-config` parses and validates the config file (or CLI flags) without starting the server. It checks all durations, sizes, enum values, send rules, and virtual device config, reporting `[OK]`/`[FAIL]`/`[WARN]` per setting. Exit code 0 means valid, 1 means errors found.

lplex-server supports multiple CAN interfaces: `-interfaces can0,can1` (comma-separated) starts a `CANBus` per interface, all feeding into a single broker. The `-interface` flag still works for single-bus setups. HOCON path: `interfaces` (string, comma-separated). In multi-bus mode, a TX dispatcher routes outgoing frames to the correct interface based on the `Bus` field in `TxRequest`. Devices are keyed by `(bus, source)` so address collisions across buses are handled correctly. CAN I/O is abstracted behind the `CANBus` interface: `SocketCANBus` for real hardware, `LoopbackBus` for development/testing without SocketCAN (enabled via `-loopback` flag or `loopback = true` in HOCON).

lplex-server has send policy flags to gate the `/send` and `/query` endpoints: `-send-enabled` (default false) and `-send-rules` (semicolon-separated rule strings). HOCON paths: `send.enabled`, `send.rules` (string or object array). CLI uses DSL syntax: `[!] [pgn:<spec>] [name:<hex>,...]` where `<spec>` supports values, comma-separated lists, and ranges (e.g. `pgn:59904,126208`, `pgn:65280-65535`). `!` prefix = deny. Omit pgn/name for wildcard. HOCON config also supports native object rules: `{ deny = true/false, pgn = "<spec>", name = "<hex>" or ["<hex>", ...] }`. Both string and object forms can be mixed in the same array. Rules are evaluated top-to-bottom, first match wins. No matching rule = deny. Empty rules + enabled = allow all. These do not affect the broker's internal ISO requests for device discovery.

Both binaries support `-device-idle-timeout` (default `5m`, `0` = disabled) to remove devices that haven't been seen within the timeout. HOCON path: `device.idle-timeout`. When a device is evicted (either by idle expiry or by NAME dedup on address change), its ValueStore entries are cleaned up and a `device_removed` event is sent to subscribers.

Both binaries support `-otel-endpoint` (OTLP gRPC collector endpoint, empty = disabled) and `-otel-sample-ratio` (0.0-1.0, default 1.0) for distributed tracing. When enabled, traces propagate via W3C Trace Context through HTTP (otelhttp middleware) and gRPC (otelgrpc interceptors) for end-to-end observability across boat → cloud replication.

Both binaries share the same retention/archive flags: `-journal-retention-max-age`, `-journal-retention-min-keep`, `-journal-retention-max-size`, `-journal-retention-soft-pct`, `-journal-retention-overflow-policy`, `-journal-archive-command`, `-journal-archive-trigger`. HOCON paths: `journal.retention.max-age`, `journal.retention.min-keep`, `journal.retention.max-size`, `journal.retention.soft-pct`, `journal.retention.overflow-policy`, `journal.archive.command`, `journal.archive.trigger`. See [`lplex-server.conf.example`](lplex-server.conf.example) and [`lplex-cloud.conf.example`](lplex-cloud.conf.example).

## Dependencies

- `go.einride.tech/can` - SocketCAN bindings
- `github.com/grandcat/zeroconf` - mDNS service discovery
- `github.com/klauspost/compress` - zstd compression for journal blocks
- `github.com/gurkankaymak/hocon` - HOCON config file parser
- `google.golang.org/grpc` - gRPC framework for replication protocol
- `google.golang.org/protobuf` - Protocol Buffers runtime
- `github.com/spf13/cobra` - CLI framework for lplex subcommands
- `go.opentelemetry.io/otel` - OpenTelemetry distributed tracing (OTLP/gRPC exporter, HTTP + gRPC instrumentation)
- `github.com/coder/websocket` - WebSocket transport for bidirectional client communication
- `github.com/eclipse/paho.mqtt.golang` - MQTT client for bridge publishing to Home Assistant, Node-RED, etc.
- `github.com/charmbracelet/bubbletea` - Terminal UI framework for interactive dashboard
- `github.com/charmbracelet/lipgloss` - Style definitions for terminal UI rendering
