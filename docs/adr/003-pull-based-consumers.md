# ADR 003: Pull-based consumer model

**Status**: Accepted

## Context

Buffered clients need to catch up from their last acknowledged position, which may be behind the ring buffer (in journal files) or at the ring buffer head (live). A push-based model (broker pushes to each client's channel) would require the broker to track per-client state and handle slow consumers.

## Decision

Buffered clients use a pull-based Consumer that reads from a tiered log:
1. Journal files (oldest data)
2. Ring buffer (recent data)
3. Live notification channel (blocking wait for new frames)

Each consumer iterates at its own pace via `Next(ctx)`. The broker only maintains a notification channel; consumers handle their own position tracking and fallback logic.

## Consequences

**Benefits:**
- Consumers iterate independently — a slow consumer reading from journal doesn't affect a fast consumer at the ring head
- No per-consumer state in the broker's hot path
- Natural backpressure: consumers that can't keep up simply read slower
- Clean separation: sessions store metadata (cursor, filter, timeout); consumers handle I/O

**Trade-offs:**
- More complex consumer implementation (tiered fallback, journal file discovery, seq-based seeking)
- Consumers must handle `ErrFallenBehind` when data is no longer available in any tier
- Journal reads require file I/O, which is slower than ring buffer reads

**Mitigations:**
- Block-level prefetch overlaps I/O and decompression with frame processing
- Journal v2 format supports O(log n) sequence-based seeking via block index
- Ring buffer is sized to cover typical reconnection gaps (64k entries ≈ 10 min at 100 fps)
