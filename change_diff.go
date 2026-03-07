package lplex

import (
	"encoding/binary"
	"math"
	"reflect"
)

// SubKeyFunc extracts a sub-discriminator from raw packet data for multiplexed
// PGNs. Returns 0 for non-multiplexed PGNs (i.e., no sub-key extractor configured).
type SubKeyFunc func(data []byte) uint64

// DiffMethod computes and applies compact binary diffs between packet payloads.
type DiffMethod interface {
	// Diff compares prev and curr, returning whether the change is significant
	// and a compact diff encoding. If not significant, diff is nil.
	Diff(prev, curr []byte) (significant bool, diff []byte)

	// Apply reconstructs curr from prev and a diff produced by Diff.
	Apply(prev, diff []byte) []byte
}

// ByteMaskDiff is the default diff method. Any byte-level change is significant.
//
// Short format (packets <= 8 bytes):
//
//	[mask:1] [changed bytes...]
//
// Extended format (packets > 8 bytes):
//
//	[maskLen:2 LE] [mask bytes...] [changed bytes...]
type ByteMaskDiff struct{}

func (ByteMaskDiff) Diff(prev, curr []byte) (bool, []byte) {
	if len(prev) != len(curr) {
		// Length change can't be diffed; caller should emit a Snapshot.
		return true, nil
	}

	n := len(curr)
	maskLen := (n + 7) / 8
	mask := make([]byte, maskLen)
	var changed []byte

	for i := range n {
		if prev[i] != curr[i] {
			mask[i/8] |= 1 << (i % 8)
			changed = append(changed, curr[i])
		}
	}

	if len(changed) == 0 {
		return false, nil
	}

	if n <= 8 {
		// Short format: single-byte mask.
		out := make([]byte, 1+len(changed))
		out[0] = mask[0]
		copy(out[1:], changed)
		return true, out
	}

	// Extended format: 2-byte LE mask length prefix.
	out := make([]byte, 2+maskLen+len(changed))
	binary.LittleEndian.PutUint16(out[0:2], uint16(maskLen))
	copy(out[2:2+maskLen], mask)
	copy(out[2+maskLen:], changed)
	return true, out
}

func (ByteMaskDiff) Apply(prev, diff []byte) []byte {
	n := len(prev)
	out := make([]byte, n)
	copy(out, prev)

	if n <= 8 {
		// Short format.
		mask := diff[0]
		changed := diff[1:]
		ci := 0
		for i := range n {
			if mask&(1<<(i%8)) != 0 {
				out[i] = changed[ci]
				ci++
			}
		}
		return out
	}

	// Extended format.
	maskLen := int(binary.LittleEndian.Uint16(diff[0:2]))
	mask := diff[2 : 2+maskLen]
	changed := diff[2+maskLen:]
	ci := 0
	for i := range n {
		if mask[i/8]&(1<<(i%8)) != 0 {
			out[i] = changed[ci]
			ci++
		}
	}
	return out
}

// FieldTolerance defines a tolerance threshold for a named field. Changes
// within the tolerance are suppressed (not emitted as significant).
type FieldTolerance struct {
	Field     string
	Tolerance float64
}

// FieldToleranceDiff wraps an inner DiffMethod and uses PGN decode functions
// plus reflection to check field-level tolerances. If all changed fields are
// within their tolerance, the change is suppressed. The encoding is always
// delegated to the inner method (tolerances only gate significance).
//
// The baseline is NOT updated when a change is suppressed, preventing
// tolerance drift over time.
type FieldToleranceDiff struct {
	Inner      DiffMethod
	PGN        uint32
	Decode     func([]byte) (any, error)
	Tolerances []FieldTolerance
}

func (f *FieldToleranceDiff) inner() DiffMethod {
	if f.Inner != nil {
		return f.Inner
	}
	return ByteMaskDiff{}
}

func (f *FieldToleranceDiff) Diff(prev, curr []byte) (bool, []byte) {
	inner := f.inner()

	// Fast path: identical bytes, no change.
	if len(prev) == len(curr) {
		same := true
		for i := range prev {
			if prev[i] != curr[i] {
				same = false
				break
			}
		}
		if same {
			return false, nil
		}
	}

	// If we can't decode, fall back to inner diff.
	if f.Decode == nil {
		return inner.Diff(prev, curr)
	}

	prevDecoded, err := f.Decode(prev)
	if err != nil {
		return inner.Diff(prev, curr)
	}
	currDecoded, err := f.Decode(curr)
	if err != nil {
		return inner.Diff(prev, curr)
	}

	// Build tolerance map.
	tolMap := make(map[string]float64, len(f.Tolerances))
	for _, t := range f.Tolerances {
		tolMap[t.Field] = t.Tolerance
	}

	prevVal := reflect.ValueOf(prevDecoded)
	currVal := reflect.ValueOf(currDecoded)

	// Handle pointer types.
	if prevVal.Kind() == reflect.Pointer {
		prevVal = prevVal.Elem()
	}
	if currVal.Kind() == reflect.Pointer {
		currVal = currVal.Elem()
	}

	if prevVal.Kind() != reflect.Struct || currVal.Kind() != reflect.Struct {
		return inner.Diff(prev, curr)
	}

	fieldIndex := buildFieldIndex(prevVal.Type())

	// Only fields with an explicit tolerance entry are considered for change
	// detection. Fields without a tolerance are ignored (e.g. SID counters).
	// A field that changed beyond its tolerance makes the whole diff significant.
	for i := range prevVal.NumField() {
		pf := prevVal.Field(i)
		cf := currVal.Field(i)

		fieldName, ok := fieldIndex[i]
		if !ok {
			continue
		}

		tol, hasTol := tolMap[fieldName]
		if !hasTol {
			continue
		}

		pv, pOk := toFloat64(pf)
		cv, cOk := toFloat64(cf)

		if pOk && cOk {
			delta := math.Abs(pv - cv)
			if delta > tol {
				return inner.Diff(prev, curr)
			}
			continue
		}

		// Non-numeric field with tolerance: any change is significant.
		if !reflect.DeepEqual(pf.Interface(), cf.Interface()) {
			return inner.Diff(prev, curr)
		}
	}

	// All changes are within tolerance.
	return false, nil
}

func (f *FieldToleranceDiff) Apply(prev, diff []byte) []byte {
	return f.inner().Apply(prev, diff)
}

// toFloat64 converts a reflected value to float64 if it's a numeric type.
func toFloat64(v reflect.Value) (float64, bool) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	default:
		return 0, false
	}
}

// buildFieldIndex maps struct field indices to their JSON tag names (or
// lowercased Go names if no JSON tag). Skips unexported fields.
func buildFieldIndex(t reflect.Type) map[int]string {
	m := make(map[int]string, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Tag.Get("json")
		if name == "" || name == "-" {
			name = f.Name
		}
		// Strip options after comma (e.g. "field,omitempty").
		for j := range len(name) {
			if name[j] == ',' {
				name = name[:j]
				break
			}
		}
		m[i] = name
	}
	return m
}
