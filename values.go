package lplex

import (
	"cmp"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

// valueFilter holds precomputed filter sets for fast lookup during snapshot iteration.
type valueFilter struct {
	pgns        map[uint32]struct{}
	excludePGNs map[uint32]struct{}
	buses       map[string]struct{} // nil = all buses
	hasDev      bool                // true if any device-based criteria are set
	devFunc     func(bus string, src uint8) bool
}

// newValueFilter builds a valueFilter from an EventFilter and device registry.
// Returns nil if no filtering is needed.
func newValueFilter(f *EventFilter, devices *DeviceRegistry) *valueFilter {
	if f.IsEmpty() {
		return nil
	}

	vf := &valueFilter{}

	if len(f.Buses) > 0 {
		vf.buses = make(map[string]struct{}, len(f.Buses))
		for _, b := range f.Buses {
			vf.buses[b] = struct{}{}
		}
	}

	if len(f.PGNs) > 0 {
		vf.pgns = make(map[uint32]struct{}, len(f.PGNs))
		for _, p := range f.PGNs {
			vf.pgns[p] = struct{}{}
		}
	}

	if len(f.ExcludePGNs) > 0 {
		vf.excludePGNs = make(map[uint32]struct{}, len(f.ExcludePGNs))
		for _, p := range f.ExcludePGNs {
			vf.excludePGNs[p] = struct{}{}
		}
	}

	if len(f.ExcludeNames) > 0 {
		vf.hasDev = true
		excludeNames := f.ExcludeNames // capture for closure
		prev := vf.devFunc
		vf.devFunc = func(bus string, src uint8) bool {
			dev := devices.Get(bus, src)
			if dev != nil && slices.Contains(excludeNames, dev.NAME) {
				return false
			}
			if prev != nil {
				return prev(bus, src)
			}
			return true
		}
	}

	if len(f.Manufacturers) > 0 || len(f.Names) > 0 || len(f.Instances) > 0 {
		vf.hasDev = true
		prev := vf.devFunc
		vf.devFunc = func(bus string, src uint8) bool {
			if prev != nil && !prev(bus, src) {
				return false
			}
			dev := devices.Get(bus, src)
			if dev == nil || dev.NAME == 0 {
				return false
			}
			return f.matchesDevice(dev)
		}
	}

	return vf
}

// valueKey identifies a unique value slot: one per (bus, source address, PGN) tuple.
type valueKey struct {
	Bus    string
	Source uint8
	PGN    uint32
}

// valueEntry is the most recent frame data for a given (bus, source, PGN).
type valueEntry struct {
	Timestamp time.Time
	Data      []byte
	Seq       uint64
}

// ValueStore tracks the last-seen frame data for each (bus, source, PGN) tuple.
// The broker goroutine writes via Record; HTTP handlers read via Snapshot.
type ValueStore struct {
	mu     sync.RWMutex
	values map[valueKey]*valueEntry
}

// NewValueStore creates an empty value store.
func NewValueStore() *ValueStore {
	return &ValueStore{
		values: make(map[valueKey]*valueEntry),
	}
}

// RemoveSource deletes all stored values for the given (bus, source) pair.
func (vs *ValueStore) RemoveSource(bus string, source uint8) {
	vs.mu.Lock()
	for k := range vs.values {
		if k.Bus == bus && k.Source == source {
			delete(vs.values, k)
		}
	}
	vs.mu.Unlock()
}

// Record updates the stored value for the given (bus, source, PGN).
// Called by the broker goroutine on every frame.
func (vs *ValueStore) Record(bus string, source uint8, pgn uint32, ts time.Time, data []byte, seq uint64) {
	key := valueKey{Bus: bus, Source: source, PGN: pgn}

	vs.mu.Lock()
	entry := vs.values[key]
	if entry == nil {
		entry = &valueEntry{}
		vs.values[key] = entry
	}
	entry.Timestamp = ts
	entry.Data = append(entry.Data[:0], data...) // reuse backing array
	entry.Seq = seq
	vs.mu.Unlock()
}

// PGNValue is a single PGN's last-known value in the JSON response.
type PGNValue struct {
	PGN  uint32 `json:"pgn"`
	Ts   string `json:"ts"`
	Data string `json:"data"`
	Seq  uint64 `json:"seq"`
}

// deviceGroupKey identifies a unique device for grouping values.
type deviceGroupKey struct {
	Bus    string
	Source uint8
}

// DeviceValues groups PGN values by device in the JSON response.
type DeviceValues struct {
	Name         string     `json:"name"`
	Bus          string     `json:"bus,omitempty"`
	Source       uint8      `json:"src"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	ModelID      string     `json:"model_id,omitempty"`
	Values       []PGNValue `json:"values"`
}

// Snapshot returns the current values grouped by device, resolved against
// the device registry for NAME and manufacturer info. An optional filter
// restricts results by PGN, bus, and/or device criteria (manufacturer, name, instance).
func (vs *ValueStore) Snapshot(devices *DeviceRegistry, filter *EventFilter) []DeviceValues {
	vf := newValueFilter(filter, devices)

	// Snapshot the values under RLock, then release before touching the device registry.
	vs.mu.RLock()
	type entry struct {
		key valueKey
		val valueEntry
	}
	entries := make([]entry, 0, len(vs.values))
	for k, v := range vs.values {
		if vf != nil && vf.buses != nil {
			if _, ok := vf.buses[k.Bus]; !ok {
				continue
			}
		}
		if vf != nil && vf.pgns != nil {
			if _, ok := vf.pgns[k.PGN]; !ok {
				continue
			}
		}
		if vf != nil && vf.excludePGNs != nil {
			if _, ok := vf.excludePGNs[k.PGN]; ok {
				continue
			}
		}
		entries = append(entries, entry{key: k, val: *v})
	}
	vs.mu.RUnlock()

	// Group by (bus, source).
	byDevice := make(map[deviceGroupKey][]PGNValue)
	deviceSet := make(map[deviceGroupKey]struct{})
	for _, e := range entries {
		dk := deviceGroupKey{Bus: e.key.Bus, Source: e.key.Source}
		deviceSet[dk] = struct{}{}
		byDevice[dk] = append(byDevice[dk], PGNValue{
			PGN:  e.key.PGN,
			Ts:   e.val.Timestamp.UTC().Format(time.RFC3339Nano),
			Data: hex.EncodeToString(e.val.Data),
			Seq:  e.val.Seq,
		})
	}

	// Build sorted device list.
	sortedDevices := make([]deviceGroupKey, 0, len(deviceSet))
	for dk := range deviceSet {
		sortedDevices = append(sortedDevices, dk)
	}
	slices.SortFunc(sortedDevices, func(a, b deviceGroupKey) int {
		if c := cmp.Compare(a.Bus, b.Bus); c != 0 {
			return c
		}
		return cmp.Compare(a.Source, b.Source)
	})

	result := make([]DeviceValues, 0, len(sortedDevices))
	for _, dk := range sortedDevices {
		if vf != nil && vf.hasDev && !vf.devFunc(dk.Bus, dk.Source) {
			continue
		}

		vals := byDevice[dk]
		slices.SortFunc(vals, func(a, b PGNValue) int {
			return cmp.Compare(a.PGN, b.PGN)
		})

		dv := DeviceValues{
			Bus:    dk.Bus,
			Source: dk.Source,
			Values: vals,
		}

		// Resolve device identity from the registry.
		if dev := devices.Get(dk.Bus, dk.Source); dev != nil && dev.NAME != 0 {
			dv.Name = fmt.Sprintf("0x%016x", dev.NAME)
			dv.Manufacturer = dev.Manufacturer
			dv.ModelID = dev.ModelID
		}

		result = append(result, dv)
	}

	return result
}

// SnapshotJSON returns the snapshot as pre-serialized JSON.
func (vs *ValueStore) SnapshotJSON(devices *DeviceRegistry, filter *EventFilter) json.RawMessage {
	snap := vs.Snapshot(devices, filter)
	b, _ := json.Marshal(snap)
	return b
}

// DecodedPGNValue is a single PGN's last-known value decoded into named fields.
type DecodedPGNValue struct {
	PGN         uint32 `json:"pgn"`
	Description string `json:"description"`
	Ts          string `json:"ts"`
	Seq         uint64 `json:"seq"`
	Fields      any    `json:"fields"`
}

// DecodedDeviceValues groups decoded PGN values by device.
type DecodedDeviceValues struct {
	Name         string            `json:"name"`
	Bus          string            `json:"bus,omitempty"`
	Source       uint8             `json:"src"`
	Manufacturer string            `json:"manufacturer,omitempty"`
	ModelID      string            `json:"model_id,omitempty"`
	Values       []DecodedPGNValue `json:"values"`
}

// DecodedSnapshot returns the current values grouped by device with PGN data
// decoded into named fields using the pgn.Registry. PGNs not in the registry
// or that fail to decode are omitted.
func (vs *ValueStore) DecodedSnapshot(devices *DeviceRegistry, filter *EventFilter) []DecodedDeviceValues {
	vf := newValueFilter(filter, devices)

	vs.mu.RLock()
	type entry struct {
		key valueKey
		val valueEntry
	}
	entries := make([]entry, 0, len(vs.values))
	for k, v := range vs.values {
		if vf != nil && vf.buses != nil {
			if _, ok := vf.buses[k.Bus]; !ok {
				continue
			}
		}
		if vf != nil && vf.pgns != nil {
			if _, ok := vf.pgns[k.PGN]; !ok {
				continue
			}
		}
		if vf != nil && vf.excludePGNs != nil {
			if _, ok := vf.excludePGNs[k.PGN]; ok {
				continue
			}
		}
		entries = append(entries, entry{key: k, val: *v})
	}
	vs.mu.RUnlock()

	// Group by (bus, source), decoding each value.
	byDevice := make(map[deviceGroupKey][]DecodedPGNValue)
	deviceSet := make(map[deviceGroupKey]struct{})
	for _, e := range entries {
		info, ok := pgn.Registry[e.key.PGN]
		if !ok || info.Decode == nil {
			continue
		}
		decoded, err := info.Decode(e.val.Data)
		if err != nil {
			continue
		}
		dk := deviceGroupKey{Bus: e.key.Bus, Source: e.key.Source}
		deviceSet[dk] = struct{}{}
		byDevice[dk] = append(byDevice[dk], DecodedPGNValue{
			PGN:         e.key.PGN,
			Description: info.Description,
			Ts:          e.val.Timestamp.UTC().Format(time.RFC3339Nano),
			Seq:         e.val.Seq,
			Fields:      decoded,
		})
	}

	sortedDevices := make([]deviceGroupKey, 0, len(deviceSet))
	for dk := range deviceSet {
		sortedDevices = append(sortedDevices, dk)
	}
	slices.SortFunc(sortedDevices, func(a, b deviceGroupKey) int {
		if c := cmp.Compare(a.Bus, b.Bus); c != 0 {
			return c
		}
		return cmp.Compare(a.Source, b.Source)
	})

	result := make([]DecodedDeviceValues, 0, len(sortedDevices))
	for _, dk := range sortedDevices {
		if vf != nil && vf.hasDev && !vf.devFunc(dk.Bus, dk.Source) {
			continue
		}

		vals := byDevice[dk]
		slices.SortFunc(vals, func(a, b DecodedPGNValue) int {
			return cmp.Compare(a.PGN, b.PGN)
		})

		dv := DecodedDeviceValues{
			Bus:    dk.Bus,
			Source: dk.Source,
			Values: vals,
		}

		if dev := devices.Get(dk.Bus, dk.Source); dev != nil && dev.NAME != 0 {
			dv.Name = fmt.Sprintf("0x%016x", dev.NAME)
			dv.Manufacturer = dev.Manufacturer
			dv.ModelID = dev.ModelID
		}

		result = append(result, dv)
	}

	return result
}

// DecodedSnapshotJSON returns the decoded snapshot as pre-serialized JSON.
func (vs *ValueStore) DecodedSnapshotJSON(devices *DeviceRegistry, filter *EventFilter) json.RawMessage {
	snap := vs.DecodedSnapshot(devices, filter)
	b, _ := json.Marshal(snap)
	return b
}
