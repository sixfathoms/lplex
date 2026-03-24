# ADR 001: Single-goroutine broker

**Status**: Accepted

## Context

The broker is the central hub that receives CAN frames, assigns sequence numbers, updates the device registry, appends to the ring buffer, and fans out to subscribers and consumers. Multiple goroutines accessing shared state would require locks on every operation in the hot path.

## Decision

The broker runs in a single goroutine. All mutable frame routing state (ring buffer writes, device registry updates, session management, subscriber fan-out) is owned by this one goroutine. No locks are needed on the write path.

Consumers and HTTP handlers read shared state through RLock (ring buffer) or RWMutex (device registry, value store), but the single-writer design means contention is minimal.

## Consequences

**Benefits:**
- Simple to reason about: all state mutations happen in one place
- No lock contention on the hot path (frame ingestion → ring → fan-out)
- Deterministic sequence numbering without atomic compare-and-swap
- Easy to add new per-frame logic without worrying about synchronization

**Trade-offs:**
- Broker throughput is bounded by one CPU core (~467k frames/sec measured)
- Long-running operations in the broker loop would block all frame processing
- Must use non-blocking channel sends for journal and subscriber fan-out

**Mitigations:**
- Journal writes happen in a separate goroutine (non-blocking channel send)
- Subscriber channels are buffered; slow subscribers are dropped rather than blocking
- At 250 kbit/s CAN bus speed, theoretical max is ~1800 frames/sec — well within single-core capacity
