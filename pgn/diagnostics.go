package pgn

// Diagnostic / alarm helpers for NMEA 2000 fault reporting.
//
// No device observed on the bus emits the standard NMEA 2000 Alert PGNs
// (126983-126988). Real alarms arrive via two mechanisms that ARE decoded:
//
//   - PGN 130762 (RV-C ISO Diagnostics / J1939 DM1): active diagnostic trouble
//     codes from the WS500 alternator regulator — red/yellow lamp + FMI + SPN.
//   - PGN 61184 (Victron proprietary register): the "Charger Error Code"
//     register (0xEDDA) carries Victron charger faults.
//
// These helpers turn the raw decoded structs into human-readable alarm detail.

// FMIDescription maps a J1939 Failure Mode Identifier (0-31) to its standard
// text. FMI 31 means "condition exists / not available" (no specific mode).
func FMIDescription(fmi uint8) string {
	switch fmi {
	case 0:
		return "data above normal (most severe)"
	case 1:
		return "data below normal (most severe)"
	case 2:
		return "data erratic or incorrect"
	case 3:
		return "voltage above normal / shorted high"
	case 4:
		return "voltage below normal / shorted low"
	case 5:
		return "current below normal / open circuit"
	case 6:
		return "current above normal / grounded"
	case 7:
		return "mechanical system not responding"
	case 8:
		return "abnormal frequency / pulse width"
	case 9:
		return "abnormal update rate"
	case 10:
		return "abnormal rate of change"
	case 11:
		return "root cause not known"
	case 12:
		return "bad intelligent device or component"
	case 13:
		return "out of calibration"
	case 14:
		return "special instruction"
	case 15:
		return "data above normal (least severe)"
	case 16:
		return "data above normal (moderately severe)"
	case 17:
		return "data below normal (least severe)"
	case 18:
		return "data below normal (moderately severe)"
	case 19:
		return "received network data in error"
	case 20:
		return "data drifted high"
	case 21:
		return "data drifted low"
	case 31:
		return "condition exists / not available"
	default:
		return "reserved"
	}
}

// SPN reconstructs the 19-bit J1939/RV-C Suspect Parameter Number from the
// three octets the WS500 splits it across (big-endian: msb<<11 | isb<<3 | lsb).
// Returns nil when there is no active fault (all bits set).
func (m RVCISODiagnostics) SPN() *uint32 {
	if m.SpnMsb == nil || m.SpnIsb == nil || m.SpnLsb == nil {
		return nil
	}
	spn := uint32(*m.SpnMsb)<<11 | uint32(*m.SpnIsb)<<3 | uint32(*m.SpnLsb)
	if spn == 0x7FFFF {
		return nil
	}
	return &spn
}

// IsActiveFault reports whether the diagnostic message represents an active
// alarm: either a warning lamp is lit or a specific failure mode is present.
func (m RVCISODiagnostics) IsActiveFault() bool {
	red := m.RedLamp != nil && *m.RedLamp == RVCLampStatusOn
	yellow := m.YellowLamp != nil && *m.YellowLamp == RVCLampStatusOn
	return red || yellow || m.Fmi != nil
}

// Severity returns "red" (severe), "yellow" (warning), or "none" based on the
// diagnostic lamp status.
func (m RVCISODiagnostics) Severity() string {
	if m.RedLamp != nil && *m.RedLamp == RVCLampStatusOn {
		return "red"
	}
	if m.YellowLamp != nil && *m.YellowLamp == RVCLampStatusOn {
		return "yellow"
	}
	return "none"
}

// VictronChargerErrorRegister is the Victron register ID (0xEDDA) that carries
// the charger error code in PGN 61184.
const VictronChargerErrorRegister = 0xEDDA

// VictronChargerErrorText maps common Victron charger error codes to text.
// Unknown codes return "" so callers can fall back to the numeric code.
func VictronChargerErrorText(code uint32) string {
	switch code {
	case 0:
		return "no error"
	case 2:
		return "battery voltage too high"
	case 17:
		return "charger temperature too high"
	case 18:
		return "charger over-current"
	case 20:
		return "bulk time limit exceeded"
	case 26:
		return "terminals overheated"
	case 33:
		return "input voltage too high"
	case 34:
		return "input current too high"
	case 38:
		return "input shutdown (battery over-voltage)"
	case 67:
		return "BMS connection lost"
	case 116:
		return "calibration data lost"
	case 119:
		return "settings data invalid/corrupt"
	default:
		return ""
	}
}
