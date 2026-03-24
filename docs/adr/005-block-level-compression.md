# ADR 005: Block-level zstd compression

**Status**: Accepted

## Context

Journal files store CAN frames that have high redundancy (same PGNs repeating at fixed intervals, similar payloads). Compression reduces storage significantly, but file-level compression would prevent random access to individual blocks.

## Decision

Each journal block (default 256 KB) is compressed independently with zstd. A block index at the end of the file provides O(1) offset lookup for any block. Compressed blocks have a 12-byte header (BaseTime + CompressedLen).

## Consequences

**Benefits:**
- ~4x compression ratio on typical CAN bus data
- Random access to any block without decompressing the entire file
- Block index enables O(log n) time-based and sequence-based seeking
- Forward-scan fallback handles crash-truncated files gracefully

**Trade-offs:**
- Per-block compression has lower ratio than file-level (no cross-block dictionary)
- Each block has a 12-byte header overhead
- Decompression is required for every block read (CPU cost on replay)

**Mitigations:**
- zstd decompression is very fast (~1 GB/s on modern hardware)
- Block-level prefetch decompresses the next block in a background goroutine
- Dictionary compression (`zstd-dict`) is supported for higher ratios when needed
- Block size is configurable (larger blocks = better ratio, slower random access)
