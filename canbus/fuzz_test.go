package canbus

import "testing"

func FuzzParseCANIDRoundTrip(f *testing.F) {
	// Seed with common CAN IDs and boundary values.
	f.Add(uint32(0x09F80102))           // PGN 129025, src 2, prio 0
	f.Add(uint32(0x0DF80501))           // PGN 129029, src 1, prio 3
	f.Add(uint32(0x18EA0001))           // ISO Request (PGN 59904), PDU1
	f.Add(uint32(0x00000000))           // all zeros
	f.Add(uint32(0x1DFFFFFF))           // all significant bits set (bit 25 reserved, always 0)
	f.Add(uint32(0x18EF00FF))           // PGN 61184 (proprietary)
	f.Add(uint32(0x09F01002))           // PF=240 boundary (PDU2)
	f.Add(uint32(0x09EF0002))           // PF=239 boundary (PDU1)

	f.Fuzz(func(t *testing.T, id uint32) {
		// Mask to 29 bits and clear the reserved bit (bit 25), which NMEA 2000
		// defines as always 0. ParseCANID ignores it, so round-trip only holds
		// when it's clear.
		id &= 0x1DFFFFFF

		h := ParseCANID(id)

		// Priority must fit in 3 bits.
		if h.Priority > 7 {
			t.Errorf("Priority %d > 7 for id 0x%08X", h.Priority, id)
		}

		// Round-trip: BuildCANID(ParseCANID(x)) must equal x.
		rebuilt := BuildCANID(h)
		if rebuilt != id {
			t.Errorf("round-trip failed: ParseCANID(0x%08X) = %+v, BuildCANID() = 0x%08X",
				id, h, rebuilt)
		}
	})
}
