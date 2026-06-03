package pgn

import "encoding/binary"

// Garmin proprietary PGN 126720 messages.
//
// PGN 126720 ("Proprietary Fast Packet") is multi-vendor. Frames begin with the
// standard manufacturer header (little-endian uint16: bits 0-10 = manufacturer
// code, bits 13-15 = industry code). Garmin = 229, industry = 4 (marine).
//
// A Garmin GPSMAP chartplotter broadcasts the active route over two sub-types,
// keyed by bytes [2:4], at ~1 Hz whenever a destination/route is set — this is
// emitted regardless of whether an autopilot is engaged:
//
//   (0x61,0x06) -> active (next) waypoint navigation  [GarminActiveWaypoint]
//   (0x12,0x07) -> final destination navigation       [GarminDestinationNav]
//
// Field layouts were reverse-engineered and validated against live GPSMAP
// 1042xsv frames by correlating each field against the boat's GPS track: the
// distance fields count down to match boat->waypoint range, the ETA fields match
// the time/date the waypoint was reached, and the positions step through the
// route as the boat progresses. Bytes whose meaning is not yet established are
// intentionally not exposed. A latitude of 0x7FFFFFFF means "no active route".
//
// Both decoders are registered onto Registry[126720] via init() below, so all
// consumers (lplex dump --decode, /events?decode=true, /history, the Go pgn
// package) pick them up automatically.

const garminManufacturerCode = 229

// GarminActiveWaypoint is Garmin proprietary PGN 126720, sub-type (0x61,0x06):
// navigation to the active (next) waypoint of the current route.
type GarminActiveWaypoint struct {
	DistanceToWaypoint *float64 `json:"distance_to_waypoint,omitempty"` // m
	EtaTime            *float64 `json:"eta_time,omitempty"`             // s (seconds of day, UTC)
	EtaDate            *uint16  `json:"eta_date,omitempty"`             // days since 1970-01-01
	WaypointLatitude   *float64 `json:"waypoint_latitude,omitempty"`    // deg
	WaypointLongitude  *float64 `json:"waypoint_longitude,omitempty"`   // deg
}

// PGN returns the PGN number this message is carried on.
func (GarminActiveWaypoint) PGN() uint32 { return 126720 }

// GarminDestinationNav is Garmin proprietary PGN 126720, sub-type (0x12,0x07):
// navigation to the final destination of the current route.
type GarminDestinationNav struct {
	DistanceToDestination *float64 `json:"distance_to_destination,omitempty"` // m
	EtaTime               *float64 `json:"eta_time,omitempty"`                // s (seconds of day, UTC)
	EtaDate               *uint16  `json:"eta_date,omitempty"`                // days since 1970-01-01
	DestinationId         *uint32  `json:"destination_id,omitempty"`
	DestinationLatitude   *float64 `json:"destination_latitude,omitempty"`  // deg
	DestinationLongitude  *float64 `json:"destination_longitude,omitempty"` // deg
}

// PGN returns the PGN number this message is carried on.
func (GarminDestinationNav) PGN() uint32 { return 126720 }

// optU32Scaled reads a little-endian uint32 at off, returning nil when all bits
// are set (the n/a sentinel), else value*scale.
func optU32Scaled(d []byte, off int, scale float64) *float64 {
	if v := binary.LittleEndian.Uint32(d[off : off+4]); v != 0xFFFFFFFF {
		f := float64(v) * scale
		return &f
	}
	return nil
}

// optLatLon reads a little-endian int32 at off as a 1e-7 degree value, returning
// nil for the 0x7FFFFFFF "not available / no active route" sentinel.
func optLatLon(d []byte, off int) *float64 {
	if v := int32(binary.LittleEndian.Uint32(d[off : off+4])); v != 0x7FFFFFFF {
		f := float64(v) * 1e-7
		return &f
	}
	return nil
}

func decodeGarminActiveWaypoint(d []byte) GarminActiveWaypoint {
	var m GarminActiveWaypoint
	if len(d) < 34 {
		return m
	}
	m.DistanceToWaypoint = optU32Scaled(d, 6, 0.01) // cm -> m
	m.EtaTime = optU32Scaled(d, 14, 0.0001)
	if v := binary.LittleEndian.Uint16(d[18:20]); v != 0xFFFF {
		m.EtaDate = &v
	}
	m.WaypointLatitude = optLatLon(d, 26)
	m.WaypointLongitude = optLatLon(d, 30)
	return m
}

func decodeGarminDestinationNav(d []byte) GarminDestinationNav {
	var m GarminDestinationNav
	if len(d) < 29 {
		return m
	}
	m.DistanceToDestination = optU32Scaled(d, 7, 0.01) // cm -> m
	m.EtaTime = optU32Scaled(d, 11, 0.0001)
	if v := binary.LittleEndian.Uint16(d[15:17]); v != 0xFFFF {
		m.EtaDate = &v
	}
	if v := binary.LittleEndian.Uint32(d[17:21]); v != 0xFFFFFFFF {
		m.DestinationId = &v
	}
	m.DestinationLatitude = optLatLon(d, 21)
	m.DestinationLongitude = optLatLon(d, 25)
	return m
}

// decodeProprietaryFastPacket dispatches PGN 126720 by manufacturer and sub-type.
// Returns (nil, nil) for vendors/sub-types without a decoder, preserving the
// "undecoded" behaviour for everything except the known Garmin messages.
func decodeProprietaryFastPacket(data []byte) (any, error) {
	if len(data) < 4 {
		return nil, nil
	}
	if binary.LittleEndian.Uint16(data[0:2])&0x07FF != garminManufacturerCode {
		return nil, nil
	}
	switch {
	case data[2] == 0x61 && data[3] == 0x06:
		return decodeGarminActiveWaypoint(data), nil
	case data[2] == 0x12 && data[3] == 0x07:
		return decodeGarminDestinationNav(data), nil
	default:
		return nil, nil
	}
}

func init() {
	Registry[126720] = PGNInfo{
		PGN:         126720,
		Description: "Proprietary Fast Packet",
		FastPacket:  true,
		Decode:      decodeProprietaryFastPacket,
	}
}
