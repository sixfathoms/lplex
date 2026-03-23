package pgn

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
	"testing/quick"
)

// TestPropertyRoundTrip uses testing/quick to verify that for every PGN with
// an Encode method, decode(encode(decode(data))) == decode(data). This tests
// the idempotency property: once data has been through one decode/encode cycle,
// subsequent cycles must produce identical results.
func TestPropertyRoundTrip(t *testing.T) {
	for pgnNum, info := range Registry {
		if info.Decode == nil {
			continue
		}
		pgnNum, info := pgnNum, info
		t.Run(fmt.Sprintf("PGN_%d", pgnNum), func(t *testing.T) {
			t.Parallel()
			err := quick.Check(func(seed uint64) bool {
				data := randomFrame(seed, info.FastPacket)

				// First decode.
				v1, err := info.Decode(data)
				if err != nil || v1 == nil {
					return true // invalid input or unknown variant, skip
				}

				// Encode (via reflection for pointer receiver).
				encoded := tryEncode(v1)
				if encoded == nil {
					return true // no Encode method (variable-width PGN)
				}

				// Second decode from re-encoded bytes.
				v2, err := info.Decode(encoded)
				if err != nil {
					t.Logf("re-decode failed for PGN %d: %v (encoded %x)", pgnNum, err, encoded)
					return false
				}

				// Compare: v1 and v2 must be equal within float tolerance.
				return valuesEqual(v1, v2, 1e-6)
			}, &quick.Config{MaxCount: 500})
			if err != nil {
				t.Error(err)
			}
		})
	}
}

// randomFrame generates a pseudo-random CAN frame from a seed.
// Standard frames are 8 bytes; fast-packet frames are 8-223 bytes.
func randomFrame(seed uint64, fastPacket bool) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0xDEADBEEF))
	size := 8
	if fastPacket {
		size = 8 + r.IntN(216) // 8 to 223 bytes
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(r.UintN(256))
	}
	return data
}

// tryEncode calls v.Encode() via reflection if available.
// Returns nil if the type has no Encode method.
func tryEncode(v any) []byte {
	rv := reflect.ValueOf(v)
	m := rv.MethodByName("Encode")
	if !m.IsValid() {
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		m = ptr.MethodByName("Encode")
	}
	if !m.IsValid() {
		return nil
	}
	results := m.Call(nil)
	return results[0].Bytes()
}

// valuesEqual compares two decoded PGN structs via JSON with float tolerance.
func valuesEqual(a, b any, epsilon float64) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)

	var am, bm map[string]any
	if json.Unmarshal(aj, &am) != nil || json.Unmarshal(bj, &bm) != nil {
		return string(aj) == string(bj)
	}

	return mapsEqual(am, bm, epsilon)
}

func mapsEqual(a, b map[string]any, epsilon float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !jsonValuesEqual(av, bv, epsilon) {
			return false
		}
	}
	return true
}

func jsonValuesEqual(a, b any, epsilon float64) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(av) && math.IsNaN(bv) {
			return true
		}
		return math.Abs(av-bv) <= epsilon
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return false
		}
		return mapsEqual(av, bv, epsilon)
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValuesEqual(av[i], bv[i], epsilon) {
				return false
			}
		}
		return true
	default:
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		return string(aj) == string(bj)
	}
}
