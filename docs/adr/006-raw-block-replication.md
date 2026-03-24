# ADR 006: Raw block passthrough for replication backfill

**Status**: Accepted

## Context

When a boat reconnects to the cloud after a gap, it needs to send historical data (backfill) to fill the hole. This data exists as compressed journal blocks on disk. Options: (a) decompress, re-encode as protobuf frames, and stream individually; (b) send raw blocks byte-for-byte.

## Decision

Backfill sends raw journal blocks byte-for-byte to the cloud. The cloud writes them directly via BlockWriter. Zero decompression or re-encoding on either side during backfill.

## Consequences

**Benefits:**
- Minimal CPU on the boat during backfill (just read from disk and send)
- Minimal CPU on the cloud (just write to disk)
- Network bandwidth equals on-disk size (already compressed)
- Backfill throughput is limited only by network bandwidth, not CPU
- Block CRC32C checksums are preserved end-to-end for integrity

**Trade-offs:**
- Cloud must understand the journal block format (tight coupling between boat and cloud journal formats)
- Cannot filter or transform data during backfill
- Cloud journal files have the same compression settings as the boat
- Version skew between boat and cloud journal formats would break backfill

**Mitigations:**
- Journal format is versioned (v1/v2) with explicit version bytes
- Both boat and cloud use the same lplex binary, minimizing version skew
- Live stream (separate from backfill) handles frame-level processing
- Backfill and live run concurrently with independent flow control
