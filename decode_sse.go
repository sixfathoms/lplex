package lplex

import (
	"encoding/hex"
	"encoding/json"

	"github.com/sixfathoms/lplex/pgn"
)

// injectDecoded takes a pre-serialized frame JSON, decodes the PGN data
// via pgn.Registry, and returns new JSON with a "decoded" field added.
// Returns the original data unchanged if decoding fails or no decoder exists.
func injectDecoded(data []byte) []byte {
	var frame struct {
		Seq  uint64 `json:"seq"`
		Ts   string `json:"ts"`
		Prio uint8  `json:"prio"`
		PGN  uint32 `json:"pgn"`
		Src  uint8  `json:"src"`
		Dst  uint8  `json:"dst"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		return data
	}

	info, ok := pgn.Registry[frame.PGN]
	if !ok || info.Decode == nil {
		return data
	}

	raw, err := hex.DecodeString(frame.Data)
	if err != nil {
		return data
	}

	decoded, err := info.Decode(raw)
	if err != nil {
		return data
	}

	// Build output with decoded field
	out := struct {
		Seq     uint64 `json:"seq"`
		Ts      string `json:"ts"`
		Prio    uint8  `json:"prio"`
		PGN     uint32 `json:"pgn"`
		Src     uint8  `json:"src"`
		Dst     uint8  `json:"dst"`
		Data    string `json:"data"`
		Decoded any    `json:"decoded"`
	}{
		Seq:     frame.Seq,
		Ts:      frame.Ts,
		Prio:    frame.Prio,
		PGN:     frame.PGN,
		Src:     frame.Src,
		Dst:     frame.Dst,
		Data:    frame.Data,
		Decoded: decoded,
	}

	result, err := json.Marshal(out)
	if err != nil {
		return data
	}
	return result
}
