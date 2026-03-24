# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) documenting significant design decisions in lplex.

| ADR | Title | Status |
|-----|-------|--------|
| [001](001-single-goroutine-broker.md) | Single-goroutine broker | Accepted |
| [002](002-pre-serialized-json.md) | Pre-serialized JSON in ring buffer | Accepted |
| [003](003-pull-based-consumers.md) | Pull-based consumer model | Accepted |
| [004](004-journal-at-broker-level.md) | Journal recording at broker level | Accepted |
| [005](005-block-level-compression.md) | Block-level zstd compression | Accepted |
| [006](006-raw-block-replication.md) | Raw block passthrough for replication backfill | Accepted |

## Format

Each ADR follows the format:
- **Status**: Accepted / Superseded / Deprecated
- **Context**: What problem or situation prompted this decision?
- **Decision**: What did we decide?
- **Consequences**: What are the trade-offs?
