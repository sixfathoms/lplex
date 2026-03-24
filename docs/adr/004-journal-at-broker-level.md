# ADR 004: Journal recording at broker level

**Status**: Accepted

## Context

Journal files could record either raw CAN frames (before fast-packet reassembly) or reassembled frames (after the broker processes them). Recording raw frames would preserve the exact bus traffic, while recording reassembled frames would be simpler for replay and analysis.

## Decision

The journal records reassembled frames as they exit the broker, not raw CAN fragments. The journal channel is tapped via non-blocking send after the broker's fan-out, in its own goroutine.

## Consequences

**Benefits:**
- Journal entries are complete, self-contained frames — no reassembly needed on replay
- Simpler journal reader: each entry is one logical frame
- Replay via `lplex simulate` feeds directly into a broker without a CAN layer
- Sequence numbers in journal match broker sequence numbers exactly

**Trade-offs:**
- Cannot reconstruct the exact CAN bus timing of individual fast-packet fragments
- Raw CAN-level debugging requires a separate tool (e.g., `candump`)
- Journal writes lag slightly behind CAN reception (non-blocking channel send)

**Mitigations:**
- Fast-packet fragment timing is rarely needed for analysis — the reassembled payload is what matters
- The 16384-entry journal channel buffer absorbs write latency spikes
- Journal drops are tracked as a metric (`JournalDrops`) for monitoring
