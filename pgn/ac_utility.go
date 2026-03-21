package pgn

import (
	"encoding/binary"
)

// AC utility power monitoring PGNs (65009-65016). These are J1939-derived
// standard PGNs for reporting AC power from a utility/shore connection.
//
// Power PGNs (65009, 65010, 65013, 65016) share identical structure:
//   [0:4]  Real Power      uint32, offset -2000000000, unit W
//   [4:8]  Apparent Power   uint32, offset -2000000000, unit VA
//
// Basic AC quantity PGNs (65011, 65014) share identical structure:
//   [0:2]  Line-Line Voltage    uint16, unit V
//   [2:4]  Line-Neutral Voltage uint16, unit V
//   [4:6]  AC Frequency         uint16, scale 1/128, unit Hz
//   [6:8]  AC RMS Current       uint16, unit A

const acPowerOffset = 2000000000

// UtilityACPower represents PGNs 65009/65010/65013/65016 — AC Power.
type UtilityACPower struct {
	RealPower     *float64 `json:"real_power"`     // W
	ApparentPower *float64 `json:"apparent_power"` // VA
}

func decodeACPower(data []byte) (UtilityACPower, error) {
	if len(data) < 8 {
		padded := make([]byte, 8)
		for i := range padded {
			padded[i] = 0xFF
		}
		copy(padded, data)
		data = padded
	}
	var m UtilityACPower
	if v := binary.LittleEndian.Uint32(data[0:4]); v != 0xFFFFFFFF {
		f := float64(v) - acPowerOffset
		m.RealPower = &f
	}
	if v := binary.LittleEndian.Uint32(data[4:8]); v != 0xFFFFFFFF {
		f := float64(v) - acPowerOffset
		m.ApparentPower = &f
	}
	return m, nil
}

// UtilityACBasicQuantities represents PGNs 65011/65014 — Basic AC Quantities.
type UtilityACBasicQuantities struct {
	LineLineVoltage    *float64 `json:"line_line_voltage"`    // V
	LineNeutralVoltage *float64 `json:"line_neutral_voltage"` // V
	ACFrequency        *float64 `json:"ac_frequency"`         // Hz
	ACRMSCurrent       *float64 `json:"ac_rms_current"`       // A
}

func decodeACBasicQuantities(data []byte) (UtilityACBasicQuantities, error) {
	if len(data) < 8 {
		padded := make([]byte, 8)
		for i := range padded {
			padded[i] = 0xFF
		}
		copy(padded, data)
		data = padded
	}
	var m UtilityACBasicQuantities
	if v := binary.LittleEndian.Uint16(data[0:2]); v != 0xFFFF {
		f := float64(v)
		m.LineLineVoltage = &f
	}
	if v := binary.LittleEndian.Uint16(data[2:4]); v != 0xFFFF {
		f := float64(v)
		m.LineNeutralVoltage = &f
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != 0xFFFF {
		f := float64(v) / 128.0
		m.ACFrequency = &f
	}
	if v := binary.LittleEndian.Uint16(data[6:8]); v != 0xFFFF {
		f := float64(v)
		m.ACRMSCurrent = &f
	}
	return m, nil
}

// Typed PGN methods for interface compliance.
func (UtilityACPower) PGN() uint32            { return 0 } // varies
func (UtilityACBasicQuantities) PGN() uint32  { return 0 } // varies

func init() {
	// Phase A
	Registry[65013] = PGNInfo{
		PGN: 65013, Description: "Utility Phase A AC Power",
		Decode: func(data []byte) (any, error) { return decodeACPower(data) },
	}
	Registry[65014] = PGNInfo{
		PGN: 65014, Description: "Utility Phase A Basic AC Quantities",
		Decode: func(data []byte) (any, error) { return decodeACBasicQuantities(data) },
	}

	// Phase B
	Registry[65010] = PGNInfo{
		PGN: 65010, Description: "Utility Phase B AC Power",
		Decode: func(data []byte) (any, error) { return decodeACPower(data) },
	}
	Registry[65011] = PGNInfo{
		PGN: 65011, Description: "Utility Phase B Basic AC Quantities",
		Decode: func(data []byte) (any, error) { return decodeACBasicQuantities(data) },
	}

	// Total
	Registry[65016] = PGNInfo{
		PGN: 65016, Description: "Utility Total AC Power",
		Decode: func(data []byte) (any, error) { return decodeACPower(data) },
	}
}
