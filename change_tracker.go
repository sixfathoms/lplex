package lplex

import (
	"errors"
	"fmt"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

// ChangeEventType identifies the kind of change event.
type ChangeEventType uint8

const (
	// Snapshot is the first observation for a key. Contains full packet data.
	Snapshot ChangeEventType = 1
	// Delta means a significant change was detected. Contains a compact diff.
	Delta ChangeEventType = 2
	// Idle means a source stopped producing for this key.
	Idle ChangeEventType = 3
)

func (t ChangeEventType) String() string {
	switch t {
	case Snapshot:
		return "snapshot"
	case Delta:
		return "delta"
	case Idle:
		return "idle"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// ChangeEvent is emitted by the ChangeTracker when a meaningful state change
// (or silence) is detected for a (source, PGN, subkey) tracking key.
type ChangeEvent struct {
	Type      ChangeEventType
	Timestamp time.Time
	Source    uint8
	PGN      uint32
	SubKey   uint64
	Seq      uint64
	Data     []byte // Full data for Snapshot, diff for Delta, nil for Idle.
}

// trackerKey identifies a unique tracking slot.
type trackerKey struct {
	Source uint8
	PGN    uint32
	SubKey uint64
}

// trackedPair holds the per-key state for change detection.
type trackedPair struct {
	lastData    []byte
	lastSeen    time.Time
	idleEmitted bool
}

// ChangeTrackerConfig configures the ChangeTracker.
type ChangeTrackerConfig struct {
	// DefaultMethod is the diff method used for PGNs without a specific override.
	// Nil defaults to ByteMaskDiff.
	DefaultMethod DiffMethod

	// Methods maps specific PGNs to custom diff methods.
	Methods map[uint32]DiffMethod

	// SubKeys maps specific PGNs to sub-key extractor functions for
	// multiplexed PGNs (e.g., Victron registers on PGN 61184).
	SubKeys map[uint32]SubKeyFunc

	// DefaultIdleTimeout is the fallback idle timeout when no PGN-specific
	// timeout can be resolved. Defaults to 5s.
	DefaultIdleTimeout time.Duration

	// IdleMultiplier is applied to the PGN registry interval to compute
	// the idle timeout. Defaults to 3.
	IdleMultiplier int

	// IdleTimeouts maps specific PGNs to explicit idle timeout overrides.
	IdleTimeouts map[uint32]time.Duration
}

// ChangeTracker tracks per-(source, PGN, subkey) state and emits compact
// change events. Not goroutine-safe; designed for single-goroutine callers
// (like the broker's handleFrame).
type ChangeTracker struct {
	cfg   ChangeTrackerConfig
	pairs map[trackerKey]*trackedPair
}

// NewChangeTracker creates a ChangeTracker with the given configuration.
// Automatically wires FieldToleranceDiff for any PGN in the registry that
// declares field-level tolerances, unless an explicit method is already set.
func NewChangeTracker(cfg ChangeTrackerConfig) *ChangeTracker {
	if cfg.DefaultIdleTimeout == 0 {
		cfg.DefaultIdleTimeout = 5 * time.Second
	}
	if cfg.IdleMultiplier == 0 {
		cfg.IdleMultiplier = 3
	}

	// Auto-wire tolerance-aware diff methods from the PGN registry.
	for pgnID, info := range pgn.Registry {
		if len(info.Tolerances) == 0 || info.Decode == nil {
			continue
		}
		if cfg.Methods != nil {
			if _, explicit := cfg.Methods[pgnID]; explicit {
				continue
			}
		}
		tols := make([]FieldTolerance, 0, len(info.Tolerances))
		for field, tol := range info.Tolerances {
			tols = append(tols, FieldTolerance{Field: field, Tolerance: tol})
		}
		if cfg.Methods == nil {
			cfg.Methods = make(map[uint32]DiffMethod)
		}
		cfg.Methods[pgnID] = &FieldToleranceDiff{
			PGN:        pgnID,
			Decode:     info.Decode,
			Tolerances: tols,
		}
	}

	return &ChangeTracker{
		cfg:   cfg,
		pairs: make(map[trackerKey]*trackedPair),
	}
}

// Process handles an incoming frame and returns a ChangeEvent if the frame
// represents a meaningful state change. Returns nil for no-ops (unchanged
// data within tolerance).
func (ct *ChangeTracker) Process(ts time.Time, source uint8, pgnID uint32, data []byte, seq uint64) *ChangeEvent {
	var subKey uint64
	if fn, ok := ct.cfg.SubKeys[pgnID]; ok && fn != nil {
		subKey = fn(data)
	}

	key := trackerKey{Source: source, PGN: pgnID, SubKey: subKey}
	pair := ct.pairs[key]

	if pair == nil {
		// First observation for this key.
		ct.pairs[key] = &trackedPair{
			lastData: append([]byte(nil), data...),
			lastSeen: ts,
		}
		return &ChangeEvent{
			Type:      Snapshot,
			Timestamp: ts,
			Source:    source,
			PGN:      pgnID,
			SubKey:   subKey,
			Seq:      seq,
			Data:     append([]byte(nil), data...),
		}
	}

	pair.lastSeen = ts
	pair.idleEmitted = false

	// Data length changed: can't diff, emit a new Snapshot.
	if len(pair.lastData) != len(data) {
		pair.lastData = append(pair.lastData[:0], data...)
		return &ChangeEvent{
			Type:      Snapshot,
			Timestamp: ts,
			Source:    source,
			PGN:      pgnID,
			SubKey:   subKey,
			Seq:      seq,
			Data:     append([]byte(nil), data...),
		}
	}

	method := ct.methodFor(pgnID)
	sig, diff := method.Diff(pair.lastData, data)
	if !sig {
		return nil
	}

	// Update baseline only on significant change.
	pair.lastData = append(pair.lastData[:0], data...)

	return &ChangeEvent{
		Type:      Delta,
		Timestamp: ts,
		Source:    source,
		PGN:      pgnID,
		SubKey:   subKey,
		Seq:      seq,
		Data:     diff,
	}
}

// Tick checks all tracked pairs for idle timeouts and returns Idle events
// for any that have exceeded their timeout. Call this periodically.
func (ct *ChangeTracker) Tick(now time.Time) []ChangeEvent {
	var events []ChangeEvent
	for key, pair := range ct.pairs {
		if pair.idleEmitted {
			continue
		}
		timeout := ct.idleTimeout(key.PGN)
		if now.Sub(pair.lastSeen) >= timeout {
			pair.idleEmitted = true
			events = append(events, ChangeEvent{
				Type:      Idle,
				Timestamp: now,
				Source:    key.Source,
				PGN:      key.PGN,
				SubKey:   key.SubKey,
			})
		}
	}
	return events
}

// Reset clears all tracked state.
func (ct *ChangeTracker) Reset() {
	clear(ct.pairs)
}

// Remove removes tracking state for a specific key.
func (ct *ChangeTracker) Remove(source uint8, pgnID uint32, subKey uint64) {
	delete(ct.pairs, trackerKey{Source: source, PGN: pgnID, SubKey: subKey})
}

// TrackedPairs returns the number of actively tracked (source, PGN, subkey) pairs.
func (ct *ChangeTracker) TrackedPairs() int {
	return len(ct.pairs)
}

// methodFor returns the DiffMethod for the given PGN.
func (ct *ChangeTracker) methodFor(pgnID uint32) DiffMethod {
	if m, ok := ct.cfg.Methods[pgnID]; ok {
		return m
	}
	if ct.cfg.DefaultMethod != nil {
		return ct.cfg.DefaultMethod
	}
	return ByteMaskDiff{}
}

// idleTimeout resolves the idle timeout for a PGN in priority order:
// 1. Per-PGN override from config
// 2. PGN registry interval * multiplier
// 3. Global default fallback
func (ct *ChangeTracker) idleTimeout(pgnID uint32) time.Duration {
	if t, ok := ct.cfg.IdleTimeouts[pgnID]; ok {
		return t
	}
	if info, ok := pgn.Registry[pgnID]; ok && info.Interval > 0 {
		return info.Interval * time.Duration(ct.cfg.IdleMultiplier)
	}
	return ct.cfg.DefaultIdleTimeout
}

// ChangeReplayer reconstructs full packet data from a stream of ChangeEvents.
// Maintains per-key state and applies diffs to recover the original packets.
type ChangeReplayer struct {
	methods map[uint32]DiffMethod
	subKeys map[uint32]SubKeyFunc
	state   map[trackerKey][]byte
}

// NewChangeReplayer creates a replayer. Pass the same methods and subkeys
// config used by the tracker that produced the events.
func NewChangeReplayer(methods map[uint32]DiffMethod, subKeys map[uint32]SubKeyFunc) *ChangeReplayer {
	return &ChangeReplayer{
		methods: methods,
		subKeys: subKeys,
		state:   make(map[trackerKey][]byte),
	}
}

// Apply processes a ChangeEvent and returns the reconstructed full packet data.
// Returns nil for Idle events (state is preserved but no data emitted).
// Returns an error if a Delta arrives without a prior Snapshot.
func (r *ChangeReplayer) Apply(event ChangeEvent) ([]byte, error) {
	key := trackerKey{Source: event.Source, PGN: event.PGN, SubKey: event.SubKey}

	switch event.Type {
	case Snapshot:
		r.state[key] = append([]byte(nil), event.Data...)
		return append([]byte(nil), event.Data...), nil

	case Delta:
		prev, ok := r.state[key]
		if !ok {
			return nil, errors.New("delta without prior snapshot")
		}
		method := r.methodFor(event.PGN)
		full := method.Apply(prev, event.Data)
		r.state[key] = full
		return append([]byte(nil), full...), nil

	case Idle:
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown event type %d", event.Type)
	}
}

// State returns the last known full packet data for a key, or nil if unknown.
func (r *ChangeReplayer) State(source uint8, pgnID uint32, subKey uint64) []byte {
	data := r.state[trackerKey{Source: source, PGN: pgnID, SubKey: subKey}]
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

func (r *ChangeReplayer) methodFor(pgnID uint32) DiffMethod {
	if r.methods != nil {
		if m, ok := r.methods[pgnID]; ok {
			return m
		}
	}
	return ByteMaskDiff{}
}
