# Block-Based Binary Journal Format

A technical description of a block-based binary journal for recording time-series event data with per-block checksums, optional compression, sequence-based seeking, and a tiered consumer model. Originally designed for CAN bus frames but generalizable to any fixed-schema binary events.

## Overview

The journal is an append-only sequence of files, each containing a stream of fixed-size blocks. Each block holds a batch of time-ordered events (frames), a metadata table (device table), and a CRC32C checksum. Files rotate by time, size, or event count. An optional block index at end-of-file enables O(1) random access in compressed files. A pull-based consumer reads from a tiered log: journal files (oldest) → in-memory ring buffer (recent) → live notification (newest), enabling seamless replay from any point in history through to realtime.

### Key Design Principles

1. **Block-based, not record-based.** Fixed-size blocks (default 256 KB) are the unit of I/O, compression, and checksumming. No per-record type tags or framing. Partial blocks on crash lose at most one block; all prior blocks are intact.
2. **Pre-serialized, single-writer.** Events are serialized once into a block buffer by a dedicated writer goroutine. No locks in the hot path. A buffered channel (16,384 entries) decouples the event source from the writer.
3. **Delta-encoded timestamps.** Each event stores only the time delta (as a varint) from the previous event in the block. The block header stores the absolute base timestamp. Saves ~6 bytes per event vs. absolute timestamps.
4. **Standard-length optimization.** A flag bit in the event ID field indicates whether the payload is a fixed standard size (eliminating a length varint on ~90-95% of events) or variable-length.
5. **Per-block metadata table.** Each block carries a snapshot of relevant metadata (e.g., device registry) with sequence tracking, so any block can be read independently without prior context.
6. **Block-level compression.** Optional zstd compression at the block level. Each block is built uncompressed, then compressed as a unit. CRC is over uncompressed data. A block index at EOF provides O(1) seeking; forward-scan fallback handles crash-truncated files.
7. **Sequence numbers for tiered replay.** V2 blocks include a base sequence number enabling consumers to seek by sequence across journal files, then seamlessly transition to an in-memory ring buffer and live stream.

---

## Binary Format

All multi-byte integers are **little-endian**. Varints use unsigned LEB128 (Go's `encoding/binary.PutUvarint` / `binary.Uvarint`).

### File Header (16 bytes)

```
Offset  Size  Field
0       3     Magic: "LPJ" (0x4C 0x50 0x4A)
3       1     Version: 0x01 (v1) or 0x02 (v2)
4       4     BlockSize: uint32 LE (bytes, power of 2, min 4096, default 262144)
8       4     Flags: uint32 LE, bits 0-7 = CompressionType
12      4     Reserved: uint32 LE (0)
```

**Version history:**
- **v1**: Original format. Time-based seeking only.
- **v2**: Adds `BaseSeq` (uint64) to each block header, enabling sequence-based seeking for tiered replay.

**CompressionType values:**
- `0` = None (uncompressed, fixed-size blocks)
- `1` = zstd (compressed, variable-size blocks with block index)
- `2` = zstd+dict (per-block dictionary, variable-size blocks with block index)

### Uncompressed Block Layout (CompressionType=0)

Each block is exactly `BlockSize` bytes.

```
┌──────────────────────────────────────────────────────────────┐
│ +0       BaseTime (8 bytes, int64 LE)                        │  Unix microseconds of first event
│ +8       BaseSeq  (8 bytes, uint64 LE) [v2 only]            │  Sequence number of first event
├──────────────────────────────────────────────────────────────┤
│ +8/+16   Event data (variable length)                        │
│          [delta_us] [event_id] [payload]  (repeated)         │
│          ...                                                 │
├──────────────────────────────────────────────────────────────┤
│          Zero padding (fills remaining space)                │
├──────────────────────────────────────────────────────────────┤
│ +(BlockSize - 10 - MetadataTableSize)                        │
│          Metadata table (variable-length entries)            │
├──────────────────────────────────────────────────────────────┤
│ +BlockSize-10   Fixed trailer (10 bytes)                     │
│          MetadataTableSize: uint16 LE                        │
│          EventCount:        uint32 LE                        │
│          Checksum:          uint32 LE (CRC32C of bytes       │
│                             [0..BlockSize-4))                │
└──────────────────────────────────────────────────────────────┘
```

**Offsets:**
- v1: event data starts at offset 8
- v2: event data starts at offset 16

**CRC32C** uses the Castagnoli polynomial (`0x1EDC6F41`). Computed over all bytes from offset 0 to `BlockSize - 4` (everything except the checksum itself).

### Compressed Block Layout (CompressionType=1, zstd)

Variable-size on disk. The decompressed payload is identical to an uncompressed block.

```
┌──────────────────────────────────────────────────────────────┐
│ Block Header                                                 │
│   BaseTime:       int64 LE  (8 bytes, unix microseconds)     │
│   BaseSeq:        uint64 LE (8 bytes) [v2 only]             │
│   CompressedLen:  uint32 LE (4 bytes)                        │
├──────────────────────────────────────────────────────────────┤
│ CompressedData (CompressedLen bytes)                         │
│   zstd-compressed full block (decompresses to BlockSize)     │
└──────────────────────────────────────────────────────────────┘
```

**Header sizes:** 12 bytes (v1) or 20 bytes (v2).

BaseTime and BaseSeq are duplicated outside the compressed payload so that seeking can binary-search block headers without decompressing.

### Dictionary-Compressed Block Layout (CompressionType=2, zstd+dict)

Each block carries its own trained zstd dictionary, making it independently decompressible with zero external state.

```
┌──────────────────────────────────────────────────────────────┐
│ Block Header                                                 │
│   BaseTime:       int64 LE  (8 bytes)                        │
│   BaseSeq:        uint64 LE (8 bytes) [v2 only]             │
│   DictLen:        uint32 LE (4 bytes, dictionary size)       │
│   CompressedLen:  uint32 LE (4 bytes, payload size)          │
├──────────────────────────────────────────────────────────────┤
│ DictData (DictLen bytes)                                     │
│   zstd dictionary trained from this block's data             │
├──────────────────────────────────────────────────────────────┤
│ CompressedData (CompressedLen bytes)                         │
│   zstd-compressed full block using DictData                  │
└──────────────────────────────────────────────────────────────┘
```

**Header sizes:** 16 bytes (v1) or 24 bytes (v2).

**Dictionary training:** The writer splits uncompressed event data into overlapping 256-byte samples (50% overlap), trains an 8 KB zstd dictionary via `zstd.BuildDict`. Training is only attempted if the block has >= 1024 bytes of event data. If training fails or produces no benefit, falls back to plain zstd (DictLen=0).

### Block Index (appended at file close, compressed files only)

Enables O(1) block lookup without scanning the entire file.

```
┌──────────────────────────────────────────────────────────────┐
│ Offset[0]:  uint64 LE (file offset of block 0 header)       │
│ Offset[1]:  uint64 LE                                       │
│ ...                                                         │
│ Offset[N-1]: uint64 LE                                      │
├──────────────────────────────────────────────────────────────┤
│ Count:  uint32 LE (number of blocks)                        │
│ Magic:  "LPJI" (4 bytes, 0x4C 0x50 0x4A 0x49)              │
└──────────────────────────────────────────────────────────────┘
```

**Total overhead:** `Count * 8 + 8` bytes. For 150 blocks/hour: ~1.2 KB.

**Reading:** Seek to EOF-8, read `Count` (4 bytes) + `Magic` (4 bytes). If Magic == "LPJI", seek to `EOF - 8 - Count*8` and read the offset table. If no valid magic (crash/truncation), fall back to forward-scanning block headers.

**Uncompressed files** don't need a block index because block offsets are computed arithmetically: `offset = FileHeaderSize + blockNumber * BlockSize`.

**Maximum index entries:** 262,144 (2 MiB index limit).

---

## Event Encoding

Events are packed sequentially within blocks. Two variants, selected by a flag bit in the event ID:

### Standard-Length Event (flag bit set)

For events with a fixed-size payload (e.g., 8 bytes). Eliminates the length varint on ~90-95% of events.

```
DeltaUs    varint     Microseconds since previous event (0 for first in block)
EventID    uint32 LE  Event identifier | 0x80000000 (bit 31 set = standard length)
Payload    N bytes    Fixed-size payload (e.g., 8 bytes)
```

**Size:** 1-3 (delta) + 4 (ID) + N (payload) = typically 13-15 bytes per event.

### Extended-Length Event (flag bit clear)

For events with variable-length payloads.

```
DeltaUs    varint     Microseconds since previous event
EventID    uint32 LE  Event identifier (bit 31 clear = extended length)
PayloadLen varint     Payload length in bytes
Payload    N bytes    Variable-size payload
```

**Size:** 1-3 (delta) + 4 (ID) + 1-2 (length) + N (payload).

### Reader/Writer Logic

**Writer:** If `len(payload) == standard_size`, set bit 31 on EventID and skip PayloadLen. Otherwise, clear bit 31 and write PayloadLen varint.

**Reader:** Read EventID uint32. If `eventID & 0x80000000 != 0`, mask off bit 31 (`eventID &= 0x7FFFFFFF`) and read `standard_size` bytes. If bit 31 is clear, read PayloadLen varint then that many bytes.

### Delta Time Encoding

- First event in block: delta = 0 (absolute time is in the block's BaseTime header)
- Subsequent events: delta = `event.time - previous_event.time` in microseconds
- Encoded as unsigned LEB128 varint
- To reconstruct absolute time: `BaseTime + sum(deltas[0..i])`

---

## Per-Block Metadata Table

Located at the end of each block, before the 10-byte trailer. Provides context needed to interpret the events in the block without reading prior blocks.

### Structure

```
EntryCount:  uint16 LE (number of entries)

Per entry (variable length):
    Key:         uint8       Entry key/identifier
    Identifier:  uint64 LE   Unique identifier for the entity
    ActiveFrom:  uint32 LE   Event index within block where this entry becomes active
    TypeCode:    uint16 LE   Type/category code
    Field1Len:   uint8       Length of field 1 string
    Field1:      N bytes     String field 1
    Field2Len:   uint8       Length of field 2 string
    Field2:      N bytes     String field 2
    ... (additional length-prefixed string fields)
```

### Semantics

- **ActiveFrom = 0**: Entry was known before this block started (carried over from prior state).
- **ActiveFrom > 0**: Entry was discovered or changed at that event index within the block.
- **Multiple entries for the same key**: The one with the largest `ActiveFrom <= targetIndex` is active at that index.
- **Self-contained blocks**: A reader can open any block and reconstruct the full metadata state by reading the table, without needing prior blocks.

### Writer Logic

When flushing a block, the writer builds the metadata table from:
1. A snapshot of the registry at block start (all entries with ActiveFrom=0)
2. Any metadata-changing events observed during the block (ActiveFrom = event index within block)

---

## Seeking

### Time-Based Seeking

**Uncompressed files:**
1. Compute block count: `(fileSize - FileHeaderSize) / BlockSize`
2. Binary search: read `BaseTime` (int64 LE) at offset `FileHeaderSize + mid * BlockSize`
3. Find block where `BaseTime <= target < nextBlock.BaseTime`
4. Parse events within block to find exact position
5. O(log N) disk reads

**Compressed files:**
1. Read block index from EOF (or forward-scan if missing)
2. Binary search over in-memory `BaseTime` array (zero I/O during search)
3. Seek to block offset, read + decompress block
4. Parse events within decompressed block
5. O(log N) in-memory comparison + 1 disk read + 1 decompress

### Sequence-Based Seeking (v2 only)

Same algorithms as time-based, but searches on `BaseSeq` instead of `BaseTime`. Event at index `i` in a v2 block has sequence number `BaseSeq + i`. Used by the tiered consumer for seamless journal → ring buffer → live transitions.

---

## File Rotation

Files rotate based on configurable triggers, checked at each block flush:

| Trigger | Description | Default |
|---|---|---|
| **Duration** | Wall-clock time since file creation | 1 hour |
| **Size** | Total bytes written to file | 0 (disabled) |
| **Count** | Total events written to file | 0 (disabled) |

**Rotation sequence:**
1. Flush current block
2. Write block index (compressed files only)
3. fsync
4. Close file
5. Fire `OnRotate` callback (for retention/archival integration)
6. Open new file with fresh header

**File naming:** `{dir}/{prefix}-{YYYYMMDD}T{HHMMSS.sss}Z.lpj`

---

## Tiered Consumer Model

A pull-based consumer reads from a three-tier log, providing seamless replay from any historical point through to realtime:

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Journal    │ ──> │ Ring Buffer  │ ──> │    Live     │
│   Files      │     │  (in-memory) │     │ Notification│
│  (oldest)    │     │   (recent)   │     │  (newest)   │
└─────────────┘     └─────────────┘     └─────────────┘
```

### How It Works

Each consumer maintains a **cursor** (sequence number of the next event to read). On each `Next()` call:

1. **Journal tier**: If cursor < ring buffer tail, seek to the correct journal file using binary search on `BaseSeq`. Read events sequentially, advancing the cursor. When the journal catches up to the ring buffer, close the journal reader.

2. **Ring buffer tier**: If cursor is within the ring buffer range, read directly from the in-memory ring (power-of-2 size, lock-free writes, `RLock` for reads). Advance cursor.

3. **Live tier**: If cursor is at the ring buffer head, block on a notification channel. When new events arrive, read from the ring buffer.

### Key Properties

- **Seamless transitions**: No gaps or duplicates when moving between tiers.
- **Per-consumer pace**: Each consumer iterates independently via its own cursor.
- **Lazy journal initialization**: Journal files are only opened when a consumer falls behind the ring buffer.
- **Prefetch**: Journal reader can eagerly decompress the next block while the current block is being consumed.
- **ErrFallenBehind**: Returned when the cursor points to data that is no longer available in any tier (journal files already deleted by retention).
- **Filter support**: Event filters are applied during reads in all tiers, so consumers only receive matching events.

---

## Retention and Archival

A background goroutine manages journal file lifecycle per directory.

### Retention Policy

Three knobs, evaluated per directory with priority `max-size > min-keep > max-age`:

| Setting | Description |
|---|---|
| **max-age** | Delete files older than this duration |
| **min-keep** | Keep files at least this long, even if over max-age |
| **max-size** | Total size cap for all journal files in directory |

Files are evaluated oldest-first. Once a file is kept, all younger files are kept.

### Soft/Hard Threshold System (when max-size + archival both configured)

| Zone | Condition | Behavior |
|---|---|---|
| **Normal** | total <= soft threshold | Standard age-based expiration, archive-then-delete |
| **Soft zone** | soft < total <= hard | Proactively queue oldest non-archived files for archive |
| **Hard zone** | total > hard | Expire files; apply overflow policy if archives failed |

`soft-pct` (default 80) sets the soft threshold as a percentage of `max-size`.

### Overflow Policy

When the hard cap is hit and archives have failed:
- **delete-unarchived** (default): Delete files anyway. Prioritizes continued recording.
- **pause-recording**: Stop journal writes. Prioritizes archive completeness.

### Archive Script Protocol

User-provided external script invoked with file paths as arguments.

**Input (stdin, JSONL):** One JSON object per file:
```json
{"path": "/data/journal/nmea2k-20250615T120000.000Z.lpj", "size": 15728640, "created": "2025-06-15T12:00:00Z"}
```

**Output (stdout, JSONL):** One JSON object per file:
```json
{"path": "/data/journal/nmea2k-20250615T120000.000Z.lpj", "status": "ok"}
```
or
```json
{"path": "/data/journal/nmea2k-20250615T120000.000Z.lpj", "status": "error", "error": "S3 upload failed: timeout"}
```

**Behavior:**
- Successful archives create a zero-byte `.archived` sidecar marker file
- Failed files retry with exponential backoff (1 min → 1 hour cap)
- Two archive triggers: `on-rotate` (immediately after file rotation) or `before-expire` (only when about to be deleted)

---

## Size Estimates

At 200 events/second, ~95% standard-length (8-byte payload):

| Metric | Uncompressed | zstd compressed (~4x) |
|---|---|---|
| Per event | ~13-15 bytes | ~3-4 bytes |
| Per second | ~2.7 KB/s | ~0.7 KB/s |
| Per hour | ~9.5 MB | ~2.4 MB |
| Per day | ~228 MB | ~57 MB |
| Metadata table per block | ~1-1.6 KB | (inside compressed block) |
| Block index per hour | — | ~1.2 KB |

---

## Summary of Constants

| Constant | Value | Description |
|---|---|---|
| `FileHeaderSize` | 16 | File header size in bytes |
| `Magic` | `"LPJ"` (3 bytes) | File magic number |
| `BlockIndexMagic` | `"LPJI"` (4 bytes) | Block index magic number |
| `Version1` | `0x01` | v1 format (time-only seeking) |
| `Version2` | `0x02` | v2 format (time + sequence seeking) |
| `BlockDataOffsetV1` | 8 | Event data offset in v1 blocks |
| `BlockDataOffsetV2` | 16 | Event data offset in v2 blocks |
| `BlockTrailerLen` | 10 | Trailer: MetadataTableSize(2) + EventCount(4) + CRC32C(4) |
| `BlockHeaderLen` | 12 | Compressed block header (v1): BaseTime(8) + CompressedLen(4) |
| `BlockHeaderLenDict` | 16 | Dict-compressed block header (v1): BaseTime(8) + DictLen(4) + CompressedLen(4) |
| `DefaultBlockSize` | 262144 (256 KB) | Default uncompressed block size |
| `MinBlockSize` | 4096 | Minimum block size |
| `JournalChannelSize` | 16384 | Writer input channel buffer |
| `MaxBlockIndexEntries` | 262144 | Max block index entries (2 MiB limit) |
| `StandardPayloadFlag` | `0x80000000` | Bit 31 of EventID: standard-length payload |
| `CRC32C Polynomial` | Castagnoli (`0x1EDC6F41`) | CRC polynomial for block checksums |
