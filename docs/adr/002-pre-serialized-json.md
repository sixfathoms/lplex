# ADR 002: Pre-serialized JSON in ring buffer

**Status**: Accepted

## Context

Each CAN frame needs to be sent to multiple SSE clients. Serializing the frame to JSON per-client would multiply CPU cost by the number of active connections. With 10+ concurrent clients, this becomes the dominant cost.

## Decision

Frames are serialized to JSON once when they enter the broker and stored as `[]byte` in the ring buffer. When an SSE client reads a frame, the pre-serialized bytes are written directly to the HTTP response with zero additional serialization.

## Consequences

**Benefits:**
- Serialization cost is O(1) regardless of consumer count
- No allocations per-client on the read path
- SSE write is a simple `w.Write(preSerializedBytes)` — no encoder needed
- Ring buffer entries are fixed-size references, cache-friendly

**Trade-offs:**
- Ring buffer entries are larger (store both raw frame + JSON bytes)
- Cannot customize JSON format per-client (all clients get the same fields)
- Adding decoded fields requires a separate pass (`?decode=true` injects decoded fields at read time via `injectDecoded`)

**Mitigations:**
- Memory overhead is modest: ~150 bytes/frame × 64k ring ≈ 10 MB
- The `?decode=true` query param handles per-client customization at the HTTP layer
