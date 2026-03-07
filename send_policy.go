package lplex

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SendPolicy controls whether the /send and /query endpoints are enabled and
// which frames are permitted. Rules are evaluated top-to-bottom; first match
// wins. If no rule matches, the request is denied. An empty Rules list with
// Enabled=true allows all frames (backwards compatible).
type SendPolicy struct {
	Enabled bool       // must be true for /send and /query to accept requests
	Rules   []SendRule // ordered rules; first match wins
}

// SendRule is a single allow or deny rule that matches frames by PGN and/or
// destination CAN NAME. Either matcher may be nil (wildcard).
type SendRule struct {
	Allow bool        // true = allow, false = deny
	PGNs  *PGNMatcher // nil = match any PGN
	Names []uint64    // nil/empty = match any destination NAME
}

// PGNMatcher matches PGN numbers against a set of individual values and ranges.
type PGNMatcher struct {
	Singles []uint32
	Ranges  [][2]uint32 // [lo, hi] inclusive
}

// Contains returns true if pgn is in the matcher's set.
func (m *PGNMatcher) Contains(pgn uint32) bool {
	for _, s := range m.Singles {
		if s == pgn {
			return true
		}
	}
	for _, r := range m.Ranges {
		if pgn >= r[0] && pgn <= r[1] {
			return true
		}
	}
	return false
}

// Matches returns true if the rule matches the given PGN and destination NAME.
// A nil PGNs matcher matches any PGN; an empty Names list matches any NAME.
func (r *SendRule) Matches(pgn uint32, dstNAME uint64, nameKnown bool) bool {
	if r.PGNs != nil && !r.PGNs.Contains(pgn) {
		return false
	}
	if len(r.Names) > 0 {
		if !nameKnown {
			return false
		}
		if !slices.Contains(r.Names, dstNAME) {
			return false
		}
	}
	return true
}

// ParseSendRule parses a single rule string. Syntax:
//
//	[!] [pgn:<spec>] [name:<hex>,...]
//
// where <spec> is comma-separated PGN values or ranges (e.g. "59904",
// "59904,126208", "129025-129029", "59904,65280-65535").
//
// A '!' prefix makes the rule a deny rule; otherwise it's an allow rule.
// Omitting pgn: matches all PGNs. Omitting name: matches all destinations.
//
// Examples:
//
//	"pgn:59904"                              — allow PGN 59904 to any device
//	"pgn:59904,126208 name:001c6e4000200000" — allow two PGNs to one device
//	"pgn:129025-129029"                      — allow a PGN range
//	"!pgn:65280-65535"                       — deny proprietary PGN range
//	"name:001c6e4000200000"                  — allow any PGN to one device
func ParseSendRule(s string) (SendRule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SendRule{}, fmt.Errorf("empty rule")
	}

	rule := SendRule{Allow: true}
	if s[0] == '!' {
		rule.Allow = false
		s = strings.TrimSpace(s[1:])
	}

	for _, token := range strings.Fields(s) {
		key, val, ok := strings.Cut(token, ":")
		if !ok {
			return SendRule{}, fmt.Errorf("invalid token %q: expected key:value", token)
		}
		switch key {
		case "pgn":
			m, err := parsePGNSpec(val)
			if err != nil {
				return SendRule{}, fmt.Errorf("invalid pgn spec %q: %w", val, err)
			}
			rule.PGNs = m
		case "name":
			names, err := parseNameList(val)
			if err != nil {
				return SendRule{}, err
			}
			rule.Names = names
		default:
			return SendRule{}, fmt.Errorf("unknown key %q", key)
		}
	}

	return rule, nil
}

// ParseSendRules parses multiple rule strings.
func ParseSendRules(rules []string) ([]SendRule, error) {
	var result []SendRule
	for i, s := range rules {
		r, err := ParseSendRule(s)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		result = append(result, r)
	}
	return result, nil
}

// parsePGNSpec parses a comma-separated list of PGN values and ranges.
// Examples: "59904", "59904,126208", "129025-129029", "59904,65280-65535"
func parsePGNSpec(s string) (*PGNMatcher, error) {
	m := &PGNMatcher{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			loVal, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid PGN %q: %w", lo, err)
			}
			hiVal, err := strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid PGN %q: %w", hi, err)
			}
			if loVal > hiVal {
				return nil, fmt.Errorf("invalid PGN range %d-%d: lo > hi", loVal, hiVal)
			}
			m.Ranges = append(m.Ranges, [2]uint32{uint32(loVal), uint32(hiVal)})
		} else {
			v, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid PGN %q: %w", part, err)
			}
			m.Singles = append(m.Singles, uint32(v))
		}
	}
	if len(m.Singles) == 0 && len(m.Ranges) == 0 {
		return nil, fmt.Errorf("empty PGN spec")
	}
	return m, nil
}

// parseNameList parses a comma-separated list of 64-bit hex CAN NAMEs.
func parseNameList(s string) ([]uint64, error) {
	var names []uint64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseUint(part, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid CAN NAME %q: must be hex: %w", part, err)
		}
		names = append(names, v)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("empty name list")
	}
	return names, nil
}
