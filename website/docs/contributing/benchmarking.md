---
sidebar_position: 4
title: Benchmarking
---

# Benchmarking

lplex has a comprehensive benchmark suite covering all performance-critical paths. Benchmarks live alongside the code they measure as `*_bench_test.go` files.

## Running benchmarks

```bash
# Run all benchmarks
go test -bench=. -run=^$ ./...

# Run benchmarks in a specific package
go test -bench=. -run=^$ ./pgn/
go test -bench=. -run=^$ ./filter/

# Run a specific benchmark by name
go test -bench=BenchmarkDecode -run=^$ ./pgn/

# Run with memory allocation reporting (already enabled via b.ReportAllocs())
go test -bench=. -benchmem -run=^$ ./...

# Run with longer duration for more stable results
go test -bench=. -benchtime=1s -count=5 -run=^$ ./...
```

## Comparing performance

Use `benchstat` to compare benchmark results across changes:

```bash
# Install benchstat
go install golang.org/x/perf/cmd/benchstat@latest

# Capture baseline
git checkout main
go test -bench=. -count=10 -run=^$ ./... > old.txt

# Capture with changes
git checkout my-branch
go test -bench=. -count=10 -run=^$ ./... > new.txt

# Compare
benchstat old.txt new.txt
```

## Benchmark coverage

### Root package (`broker_bench_test.go`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkFrameJSONSerialization` | `json.Marshal` of a pre-built `frameJSON` struct |
| `BenchmarkFrameJSONSerializationFull` | Full serialization path: time format + hex encode + JSON marshal |
| `BenchmarkHexEncodeData` | `hex.EncodeToString` for 8-byte CAN payloads |
| `BenchmarkTimeFormat` | `time.Format(RFC3339Nano)` for timestamps |
| `BenchmarkRingBufferWrite` | Writing a pre-serialized entry to the ring buffer (lock + assign + advance) |
| `BenchmarkEventFilterMatches` | `EventFilter.matches()` with nil, single PGN, multiple PGNs, exclude filters |
| `BenchmarkResolvedFilterMatches` | `resolvedFilter.matches()` with map-based PGN and source lookups |
| `BenchmarkFanOut` | Fan-out to 0, 1, 10, and 100 subscribers (with and without filters) |

### Root package (`fastpacket_bench_test.go`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkFastPacketProcess` | Complete fast-packet reassembly (single transfer, assembler reuse, concurrent sources) |
| `BenchmarkFragmentFastPacket` | Splitting payloads into CAN frames (20-byte and 223-byte payloads) |
| `BenchmarkIsFastPacket` | Registry lookup for fast-packet flag |
| `BenchmarkPurgeStale` | Cleanup of timed-out in-progress assemblies |

### Root package (`journal_writer_bench_test.go`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkJournalAppendFrame` | Full journal write pipeline — frame encoding, block flush, and file I/O (uncompressed and zstd) |
| `BenchmarkJournalFrameEncoding` | Raw frame encoding into a block buffer (varint + CAN ID + data copy) |
| `BenchmarkBuildCANID` | Constructing a 29-bit CAN identifier from a `CANHeader` |

### Filter package (`filter/filter_bench_test.go`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkCompile` | Lexing + parsing filter expressions (simple to complex) |
| `BenchmarkMatch` | Evaluating compiled filters against header fields, decoded struct fields (float, string), lookup fields, and nil decoded values |

### PGN package (`pgn/pgn_bench_test.go`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkDecode` | Decoding raw bytes for VesselHeading, WindData, Temperature, EngineParametersRapidUpdate, BatteryStatus |
| `BenchmarkEncode` | Encoding decoded structs back to raw bytes |
| `BenchmarkDecodeEncode` | Full decode + encode round-trip |
| `BenchmarkRegistryLookup` | Map lookup in `pgn.Registry` (known PGN, unknown PGN, lookup + decode) |

## Writing new benchmarks

Follow these conventions when adding benchmarks:

1. **File naming**: Use `*_bench_test.go` to keep benchmarks separate from tests.
2. **Always call `b.ReportAllocs()`** so memory allocations are tracked.
3. **Use `b.Loop()`** (Go 1.24+) for the benchmark loop.
4. **Use `b.ResetTimer()`** after setup code to exclude setup from measurement.
5. **Use subtests** (`b.Run("name", ...)`) to group related benchmarks.
6. **Avoid struct literals for generated types** — use `Decode*` functions to construct test values, since generated field types may change.

Example:

```go
func BenchmarkMyOperation(b *testing.B) {
    // Setup (not measured)
    data := prepareTestData()

    b.ReportAllocs()
    b.ResetTimer()
    for b.Loop() {
        myOperation(data)
    }
}
```

## Performance characteristics

The broker hot path is designed for minimal allocation and lock contention:

- **PGN decode**: ~1.5–7 ns/op, zero allocations
- **Ring buffer write**: ~8 ns/op, zero allocations
- **Filter matching**: ~2–6 ns/op for resolved filters, zero allocations
- **JSON pre-serialization**: ~200 ns/op (dominated by `json.Marshal` + `time.Format`)
- **Fan-out**: scales linearly with subscriber count (~6 ns/subscriber)
- **Fast-packet reassembly**: ~70 ns/op per complete transfer
- **Journal frame encoding**: ~2–3 ns/op (raw encoding), ~26–28 ns/op (amortized with block flush and I/O)

These numbers are from an Apple M5 Pro. Absolute values will differ across hardware, but relative comparisons via `benchstat` are meaningful on any machine.
