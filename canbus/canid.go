// Package canbus provides shared NMEA 2000 / CAN bus types and functions
// importable by both the server internals and client tools.
package canbus

// CANHeader holds the parsed fields from a 29-bit CAN ID.
type CANHeader struct {
	Priority    uint8
	PGN         uint32
	Source      uint8
	Destination uint8 // 0xFF for broadcast (PDU2)
}

// ParseCANID extracts priority, PGN, source address, and destination
// from a 29-bit CAN identifier per NMEA 2000 / ISO 11783.
//
// CAN ID bit layout (29 bits):
//
//	bits 28-26: priority (3 bits)
//	bit  25:    reserved (always 0 on NMEA 2000)
//	bit  24:    data page (DP)
//	bits 23-16: PDU format (PF)
//	bits 15-8:  PDU specific (PS)
//	bits 7-0:   source address
//
// PF >= 240 (PDU2, broadcast): PGN = DP<<16 | PF<<8 | PS
// PF < 240  (PDU1, addressed): PGN = DP<<16 | PF<<8, PS = destination
func ParseCANID(id uint32) CANHeader {
	source := uint8(id & 0xFF)
	ps := uint8((id >> 8) & 0xFF)
	pf := uint8((id >> 16) & 0xFF)
	dp := uint8((id >> 24) & 0x01)
	priority := uint8((id >> 26) & 0x07)

	var pgn uint32
	var dest uint8

	if pf >= 240 {
		pgn = uint32(dp)<<16 | uint32(pf)<<8 | uint32(ps)
		dest = 0xFF
	} else {
		pgn = uint32(dp)<<16 | uint32(pf)<<8
		dest = ps
	}

	return CANHeader{
		Priority:    priority,
		PGN:         pgn,
		Source:      source,
		Destination: dest,
	}
}

// BuildCANID constructs a 29-bit CAN identifier from a CANHeader.
// Inverse of ParseCANID: BuildCANID(ParseCANID(x)) == x for valid IDs.
func BuildCANID(h CANHeader) uint32 {
	dp := (h.PGN >> 16) & 0x01
	pf := uint8((h.PGN >> 8) & 0xFF)
	var ps uint8
	if pf >= 240 {
		ps = uint8(h.PGN & 0xFF)
	} else {
		ps = h.Destination
	}
	return uint32(h.Priority)<<26 | dp<<24 | uint32(pf)<<16 | uint32(ps)<<8 | uint32(h.Source)
}
