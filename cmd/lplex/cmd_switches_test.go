package main

import "testing"

func TestParseSwitchArg(t *testing.T) {
	tests := []struct {
		arg       string
		wantNum   int
		wantState uint8
		wantErr   bool
	}{
		{"1=on", 1, 1, false},
		{"1=ON", 1, 1, false},
		{"3=off", 3, 0, false},
		{"28=on", 28, 1, false},
		{"1=1", 1, 1, false},
		{"1=0", 1, 0, false},

		// Errors.
		{"0=on", 0, 0, true},    // switch 0 invalid (1-based)
		{"29=on", 0, 0, true},   // switch 29 out of range
		{"-1=on", 0, 0, true},   // negative
		{"1=maybe", 0, 0, true}, // bad state
		{"on", 0, 0, true},      // no equals
		{"=on", 0, 0, true},     // empty switch number
		{"1=", 0, 0, true},      // empty state
	}

	for _, tt := range tests {
		num, state, err := parseSwitchArg(tt.arg)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSwitchArg(%q): expected error, got num=%d state=%d", tt.arg, num, state)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSwitchArg(%q): unexpected error: %v", tt.arg, err)
			continue
		}
		if num != tt.wantNum || state != tt.wantState {
			t.Errorf("parseSwitchArg(%q) = (%d, %d), want (%d, %d)", tt.arg, num, state, tt.wantNum, tt.wantState)
		}
	}
}
