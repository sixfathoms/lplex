package filter

import "testing"

func FuzzCompile(f *testing.F) {
	// Seed corpus with valid and edge-case expressions.
	seeds := []string{
		`pgn == 129025`,
		`src == 1 && pgn == 130310`,
		`pgn == 61184 || pgn == 130310`,
		`!(pgn == 129025)`,
		`water_temperature < 280`,
		`register.name == "State of Charge"`,
		`prio >= 2 && dst != 255`,
		`pgn == 129025 && src > 0 && src < 100`,
		``,
		`"unterminated`,
		`pgn ==`,
		`== 1`,
		`((((pgn == 1))))`,
		`a.b.c == 1`,
		`pgn == 0 || pgn == 65535 || pgn == 129025`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		// Compile must not panic on any input.
		// Errors are fine — we're testing robustness, not correctness.
		_, _ = Compile(expr)
	})
}
