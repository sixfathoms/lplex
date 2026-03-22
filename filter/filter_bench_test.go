package filter

import (
	"testing"
)

func BenchmarkCompile(b *testing.B) {
	b.Run("simple_pgn", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Compile("pgn == 127250") //nolint:errcheck
		}
	})

	b.Run("two_conditions", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Compile("pgn == 130310 && src == 35") //nolint:errcheck
		}
	})

	b.Run("complex", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Compile("(pgn == 130310 || pgn == 130306) && src != 0 && prio <= 3") //nolint:errcheck
		}
	})

	b.Run("decoded_field", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Compile("wind_speed > 10.5 && wind_reference == 2") //nolint:errcheck
		}
	})
}

func BenchmarkMatch(b *testing.B) {
	b.Run("header_only/pgn_eq", func(b *testing.B) {
		f, _ := Compile("pgn == 127250")
		ctx := &EvalContext{PGN: 127250, Src: 35, Dst: 0xFF, Prio: 2}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("header_only/pgn_miss", func(b *testing.B) {
		f, _ := Compile("pgn == 127250")
		ctx := &EvalContext{PGN: 129025, Src: 35, Dst: 0xFF, Prio: 2}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("header_only/and", func(b *testing.B) {
		f, _ := Compile("pgn == 127250 && src == 35")
		ctx := &EvalContext{PGN: 127250, Src: 35, Dst: 0xFF, Prio: 2}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("header_only/or", func(b *testing.B) {
		f, _ := Compile("pgn == 127250 || pgn == 129025")
		ctx := &EvalContext{PGN: 127250, Src: 35, Dst: 0xFF, Prio: 2}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("header_only/complex", func(b *testing.B) {
		f, _ := Compile("(pgn == 130310 || pgn == 130306) && src != 0 && prio <= 3")
		ctx := &EvalContext{PGN: 130306, Src: 35, Dst: 0xFF, Prio: 2}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("decoded_field/float", func(b *testing.B) {
		type windData struct {
			WindSpeed     float64 `json:"wind_speed"`
			WindAngle     float64 `json:"wind_angle"`
			WindReference uint8   `json:"wind_reference"`
		}
		f, _ := Compile("wind_speed > 10.5")
		ctx := &EvalContext{
			PGN:     130306,
			Src:     35,
			Decoded: windData{WindSpeed: 15.2, WindAngle: 1.5, WindReference: 2},
		}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("decoded_field/string", func(b *testing.B) {
		type decodedPGN struct {
			State string `json:"state"`
			Value uint8  `json:"value"`
		}
		f, _ := Compile(`state == "active"`)
		ctx := &EvalContext{
			PGN:     61184,
			Src:     35,
			Decoded: decodedPGN{State: "active", Value: 1},
		}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("lookup_field", func(b *testing.B) {
		f, _ := Compile(`register.name == "State of Charge"`)
		ctx := &EvalContext{
			PGN: 61184,
			Src: 35,
			Lookups: map[string]string{
				"register": "State of Charge",
			},
		}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})

	b.Run("nil_decoded", func(b *testing.B) {
		f, _ := Compile("wind_speed > 10.5")
		ctx := &EvalContext{PGN: 130306, Src: 35}
		b.ReportAllocs()
		for b.Loop() {
			f.Match(ctx)
		}
	})
}
