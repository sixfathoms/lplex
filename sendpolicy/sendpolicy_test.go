package sendpolicy

import (
	"testing"
)

func TestParseSendRule(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		allow   bool
		hasPGN  bool
		hasName bool
	}{
		{"pgn:59904", false, true, true, false},
		{"pgn:59904,126208", false, true, true, false},
		{"pgn:129025-129029", false, true, true, false},
		{"pgn:59904 name:001c6e4000200000", false, true, true, true},
		{"name:001c6e4000200000", false, true, false, true},
		{"!pgn:65280-65535", false, false, true, false},
		{"!name:001c6e4000200000", false, false, false, true},
		{"pgn:59904,65280-65535 name:001c6e4000200000,001c6e4000200001", false, true, true, true},
		// errors
		{"", true, false, false, false},
		{"badtoken", true, false, false, false},
		{"pgn:", true, false, false, false},
		{"foo:bar", true, false, false, false},
		{"pgn:abc", true, false, false, false},
		{"name:notahex", true, false, false, false},
		{"pgn:100-50", true, false, false, false}, // lo > hi
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			rule, err := ParseSendRule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rule.Allow != tt.allow {
				t.Errorf("Allow: got %v, want %v", rule.Allow, tt.allow)
			}
			if (rule.PGNs != nil) != tt.hasPGN {
				t.Errorf("PGNs: got %v, want hasPGN=%v", rule.PGNs, tt.hasPGN)
			}
			if (len(rule.Names) > 0) != tt.hasName {
				t.Errorf("Names: got %v, want hasName=%v", rule.Names, tt.hasName)
			}
		})
	}
}

func TestPGNMatcherContains(t *testing.T) {
	m := &PGNMatcher{
		Singles: []uint32{59904, 126208},
		Ranges:  [][2]uint32{{129025, 129029}, {65280, 65535}},
	}

	tests := []struct {
		pgn  uint32
		want bool
	}{
		{59904, true},
		{126208, true},
		{129025, true},  // range start
		{129027, true},  // range middle
		{129029, true},  // range end
		{129030, false}, // just outside
		{65280, true},
		{65535, true},
		{65000, false}, // gap between singles and second range
		{0, false},
	}

	for _, tt := range tests {
		if got := m.Contains(tt.pgn); got != tt.want {
			t.Errorf("Contains(%d): got %v, want %v", tt.pgn, got, tt.want)
		}
	}
}

func TestSendRuleMatches(t *testing.T) {
	// Rule: allow pgn:59904 name:001c6e4000200000
	rule := SendRule{
		Allow: true,
		PGNs:  &PGNMatcher{Singles: []uint32{59904}},
		Names: []uint64{0x001c6e4000200000},
	}

	// Exact match.
	if !rule.Matches(59904, 0x001c6e4000200000, true) {
		t.Error("should match exact PGN+NAME")
	}

	// Wrong PGN.
	if rule.Matches(126208, 0x001c6e4000200000, true) {
		t.Error("should not match wrong PGN")
	}

	// Wrong NAME.
	if rule.Matches(59904, 0x001c6e4000200001, true) {
		t.Error("should not match wrong NAME")
	}

	// NAME unknown (broadcast case).
	if rule.Matches(59904, 0, false) {
		t.Error("should not match when NAME is unknown")
	}

	// Wildcard PGN rule.
	wildcardPGN := SendRule{Allow: true, Names: []uint64{0x001c6e4000200000}}
	if !wildcardPGN.Matches(99999, 0x001c6e4000200000, true) {
		t.Error("wildcard PGN should match any PGN")
	}

	// Wildcard NAME rule.
	wildcardName := SendRule{Allow: true, PGNs: &PGNMatcher{Singles: []uint32{59904}}}
	if !wildcardName.Matches(59904, 0, false) {
		t.Error("wildcard NAME should match any destination")
	}
	if !wildcardName.Matches(59904, 0x001c6e4000200000, true) {
		t.Error("wildcard NAME should match known destination too")
	}
}

func TestParseSendRules(t *testing.T) {
	rules, err := ParseSendRules([]string{
		"!pgn:65280-65535",
		"pgn:59904",
		"pgn:129025-129029 name:001c6e4000200000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}
	if rules[0].Allow {
		t.Error("rule 0 should be deny")
	}
	if !rules[1].Allow {
		t.Error("rule 1 should be allow")
	}
	if len(rules[2].Names) != 1 {
		t.Errorf("rule 2 should have 1 name, got %d", len(rules[2].Names))
	}
}

func TestParseSendRulesError(t *testing.T) {
	_, err := ParseSendRules([]string{"pgn:59904", "badtoken"})
	if err == nil {
		t.Fatal("expected error for invalid rule")
	}
}
