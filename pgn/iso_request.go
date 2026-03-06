package pgn

import "fmt"

// ISORequest represents PGN 59904 — ISO Request.
// A 3-byte single-frame message requesting transmission of a specific PGN.
//
//	[0:3]  Requested PGN  uint24, little-endian
type ISORequest struct {
	RequestedPGN uint32 `json:"requested_pgn"`
}

func (ISORequest) PGN() uint32 { return 59904 }

func DecodeISORequest(data []byte) (ISORequest, error) {
	if len(data) < 3 {
		return ISORequest{}, fmt.Errorf("pgn 59904: need at least 3 bytes, got %d", len(data))
	}
	pgn := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16
	return ISORequest{RequestedPGN: pgn}, nil
}

func init() {
	Registry[59904] = PGNInfo{
		PGN:         59904,
		Description: "ISO Request",
		Decode:      func(data []byte) (any, error) { return DecodeISORequest(data) },
	}
}
