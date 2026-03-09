package pgn

import (
	"encoding/binary"
	"fmt"
)

// NavigationRouteWPInformation represents PGN 129285.
// Variable-length: 9-byte header + STRING_LAU route name + 1 reserved byte,
// followed by repeated waypoint entries (ID, STRING_LAU name, lat, lon).
//
//	Header layout:
//	  [0:2]   Start RPS# (uint16)
//	  [2:4]   Item count (uint16)
//	  [4:6]   Database ID (uint16)
//	  [6:8]   Route ID (uint16)
//	  [8]     bits 0-2: Nav direction, bits 3-4: Supplementary data, bits 5-7: reserved
//	  [9:...]  Route Name (STRING_LAU)
//	  [+1]    Reserved (0xFF)
//
//	Per waypoint (repeated Items times):
//	  [+0:+2]  WP ID (uint16)
//	  [+2:...] WP Name (STRING_LAU)
//	  [+N:+N+4] WP Latitude (int32, scale 1e-7, deg)
//	  [+N+4:+N+8] WP Longitude (int32, scale 1e-7, deg)
type NavigationRouteWPInformation struct {
	StartRPS            uint16          `json:"start_rps"`
	Items               uint16          `json:"items"`
	DatabaseID          uint16          `json:"database_id"`
	RouteID             uint16          `json:"route_id"`
	NavigationDirection uint8           `json:"navigation_direction"`
	SupplementaryData   uint8           `json:"supplementary_data"`
	RouteName           string          `json:"route_name"`
	Waypoints           []RouteWaypoint `json:"waypoints,omitempty"`
}

// RouteWaypoint is a single waypoint entry within PGN 129285.
type RouteWaypoint struct {
	ID        uint16  `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`  // deg
	Longitude float64 `json:"longitude"` // deg
}

func (NavigationRouteWPInformation) PGN() uint32 { return 129285 }

// DecodeNavigationRouteWPInformation decodes PGN 129285 from raw bytes.
func DecodeNavigationRouteWPInformation(data []byte) (NavigationRouteWPInformation, error) {
	if len(data) < 11 {
		return NavigationRouteWPInformation{}, fmt.Errorf("pgn 129285: need at least 11 bytes, got %d", len(data))
	}

	var m NavigationRouteWPInformation
	m.StartRPS = binary.LittleEndian.Uint16(data[0:2])
	m.Items = binary.LittleEndian.Uint16(data[2:4])
	m.DatabaseID = binary.LittleEndian.Uint16(data[4:6])
	m.RouteID = binary.LittleEndian.Uint16(data[6:8])
	m.NavigationDirection = data[8] & 0x07
	m.SupplementaryData = (data[8] >> 3) & 0x03

	off := 9

	// Route name (STRING_LAU).
	name, n := decodeLAU(data[off:])
	if n == 0 {
		return m, nil
	}
	m.RouteName = name
	off += n

	// Reserved byte.
	if off >= len(data) {
		return m, nil
	}
	off++

	// Waypoint entries.
	count := int(m.Items)
	m.Waypoints = make([]RouteWaypoint, 0, count)
	for range count {
		if off+2 > len(data) {
			break
		}
		var wp RouteWaypoint
		wp.ID = binary.LittleEndian.Uint16(data[off : off+2])
		off += 2

		wpName, n := decodeLAU(data[off:])
		if n == 0 {
			break
		}
		wp.Name = wpName
		off += n

		if off+8 > len(data) {
			break
		}
		wp.Latitude = float64(int32(binary.LittleEndian.Uint32(data[off:off+4]))) * 1e-7
		off += 4
		wp.Longitude = float64(int32(binary.LittleEndian.Uint32(data[off:off+4]))) * 1e-7
		off += 4

		m.Waypoints = append(m.Waypoints, wp)
	}
	return m, nil
}

// decodeLAU decodes an NMEA 2000 STRING_LAU (length-and-unicode) field.
//
//	Byte 0: total length (includes itself and the encoding byte)
//	Byte 1: encoding (0 = UTF-16LE, 1 = ASCII)
//	Remaining: string data
//
// Returns the decoded string and the total number of bytes consumed.
func decodeLAU(data []byte) (string, int) {
	if len(data) < 2 {
		return "", 0
	}
	totalLen := int(data[0])
	if totalLen < 2 {
		return "", 2
	}
	if totalLen > len(data) {
		totalLen = len(data)
	}
	encoding := data[1]
	payload := data[2:totalLen]
	if encoding == 0 && len(payload) >= 2 {
		// UTF-16LE
		runes := make([]rune, 0, len(payload)/2)
		for i := 0; i+1 < len(payload); i += 2 {
			runes = append(runes, rune(binary.LittleEndian.Uint16(payload[i:i+2])))
		}
		return string(runes), totalLen
	}
	return string(payload), totalLen
}

func init() {
	Registry[129285] = PGNInfo{
		PGN:         129285,
		Description: "Navigation Route WP Information",
		FastPacket:  true,
		Decode:      func(data []byte) (any, error) { return DecodeNavigationRouteWPInformation(data) },
	}
}
