package pgn

import (
	"encoding/binary"
	"encoding/hex"
)

// VictronRegister represents PGN 61184 from Victron Energy (manufacturer 358).
//
//	[0:2]  Manufacturer/Industry header (uint16 LE, lower 11 bits = mfr, upper 3 = industry)
//	[2:4]  Register ID (uint16 LE)
//	[4:8]  Payload (uint32 LE, interpretation depends on register)
type VictronRegister struct {
	ManufacturerCode uint16 `json:"manufacturer_code"`
	IndustryCode     uint8  `json:"industry_code"`
	RegisterID       uint16 `json:"register_id"`
	RegisterName     string `json:"register_name,omitempty"`
	Payload          uint32 `json:"payload"`
}

func (VictronRegister) PGN() uint32 { return 61184 }

// ProprietarySingleFrame represents PGN 61184 from an unrecognized manufacturer.
type ProprietarySingleFrame struct {
	ManufacturerCode uint16 `json:"manufacturer_code"`
	IndustryCode     uint8  `json:"industry_code"`
	Data             string `json:"data"` // hex-encoded bytes after manufacturer header
}

func (ProprietarySingleFrame) PGN() uint32 { return 61184 }

// victronRegisterNames maps known Victron VE.Can register IDs to names.
// Source: canboat, esphome-victron-vedirect, Victron VE.Can registers public doc.
var victronRegisterNames = map[uint16]string{
	0x0100: "Product ID",
	0x0200: "Device Mode",
	0x0201: "Device State",
	0x0205: "Device Off Reason",
	0x031C: "Warning Reason",
	0x031E: "Alarm Reason",
	0x0FFF: "State of Charge",
	0x0FFE: "Time to Go",
	0xED8D: "DC Channel 1 Voltage",
	0xED8E: "DC Channel 1 Power",
	0xED8F: "DC Channel 1 Current",
	0xEDAD: "Load Current",
	0xEDB3: "MPPT Tracker Mode",
	0xEDBB: "Panel Voltage",
	0xEDBC: "Panel Power",
	0xEDBD: "Panel Current",
	0xEDD0: "Max Power Yesterday",
	0xEDD1: "Yield Yesterday",
	0xEDD2: "Max Power Today",
	0xEDD3: "Yield Today",
	0xEDD5: "Charger Voltage",
	0xEDD7: "Charger Current",
	0xEDDA: "Charger Error Code",
	0xEDDB: "Charger Internal Temp",
	0xEDDC: "User Yield",
	0xEDDD: "System Yield",
	0xEDEC: "Battery Temperature",
	0xEDF0: "Battery Max Current",
	0xEDF1: "Battery Type",
	0xEDF6: "Battery Float Voltage",
	0xEDF7: "Battery Absorption Voltage",
	0xEEFF: "Discharge Since Full",
}

func decodeProprietarySingleFrame(data []byte) (any, error) {
	if len(data) < 8 {
		padded := make([]byte, 8)
		for i := range padded {
			padded[i] = 0xFF
		}
		copy(padded, data)
		data = padded
	}

	mfr := binary.LittleEndian.Uint16(data[0:2])
	mfrCode := mfr & 0x7FF
	industryCode := uint8(mfr >> 13)

	if mfrCode == 358 { // Victron Energy
		regID := binary.LittleEndian.Uint16(data[2:4])
		payload := binary.LittleEndian.Uint32(data[4:8])
		return VictronRegister{
			ManufacturerCode: mfrCode,
			IndustryCode:     industryCode,
			RegisterID:       regID,
			RegisterName:     victronRegisterNames[regID],
			Payload:          payload,
		}, nil
	}

	return ProprietarySingleFrame{
		ManufacturerCode: mfrCode,
		IndustryCode:     industryCode,
		Data:             hex.EncodeToString(data[2:]),
	}, nil
}

func init() {
	Registry[61184] = PGNInfo{
		PGN:         61184,
		Description: "Proprietary Single Frame",
		Decode:      func(data []byte) (any, error) { return decodeProprietarySingleFrame(data) },
	}
}
