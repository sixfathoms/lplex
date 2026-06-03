package pgn

import "testing"

func TestFMIDescription(t *testing.T) {
	cases := map[uint8]string{
		2:  "data erratic or incorrect",
		14: "special instruction",
		20: "data drifted high",
		31: "condition exists / not available",
		99: "reserved",
	}
	for fmi, want := range cases {
		if got := FMIDescription(fmi); got != want {
			t.Errorf("FMIDescription(%d) = %q, want %q", fmi, got, want)
		}
	}
}

func TestRVCISODiagnosticsSPN(t *testing.T) {
	// Active fault: msb=254, isb=5, lsb=2 -> 254<<11 | 5<<3 | 2 = 520234.
	active := RVCISODiagnostics{
		SpnMsb: ptrTo[uint8](254), SpnIsb: ptrTo[uint8](5), SpnLsb: ptrTo[uint8](2),
	}
	spn := active.SPN()
	if spn == nil || *spn != 520234 {
		t.Errorf("SPN() = %v, want 520234", spn)
	}

	// No fault: all bits set -> 0x7FFFF -> nil.
	none := RVCISODiagnostics{
		SpnMsb: ptrTo[uint8](255), SpnIsb: ptrTo[uint8](255), SpnLsb: ptrTo[uint8](7),
	}
	if got := none.SPN(); got != nil {
		t.Errorf("SPN() = %v, want nil for all-ones", got)
	}

	// Missing octets -> nil.
	if got := (RVCISODiagnostics{}).SPN(); got != nil {
		t.Errorf("SPN() = %v, want nil when octets absent", got)
	}
}

func TestRVCISODiagnosticsSeverity(t *testing.T) {
	red := RVCISODiagnostics{RedLamp: ptrTo(RVCLampStatusOn), YellowLamp: ptrTo(RVCLampStatusOn)}
	if red.Severity() != "red" || !red.IsActiveFault() {
		t.Errorf("expected red active fault, got %q active=%v", red.Severity(), red.IsActiveFault())
	}
	yellow := RVCISODiagnostics{YellowLamp: ptrTo(RVCLampStatusOn)}
	if yellow.Severity() != "yellow" || !yellow.IsActiveFault() {
		t.Errorf("expected yellow active fault, got %q", yellow.Severity())
	}
	clear := RVCISODiagnostics{RedLamp: ptrTo(RVCLampStatusOff), YellowLamp: ptrTo(RVCLampStatusOff)}
	if clear.Severity() != "none" || clear.IsActiveFault() {
		t.Errorf("expected no fault, got %q active=%v", clear.Severity(), clear.IsActiveFault())
	}
	// FMI present with no lamp still counts as active.
	fmiOnly := RVCISODiagnostics{Fmi: ptrTo[uint8](2)}
	if !fmiOnly.IsActiveFault() {
		t.Error("expected active fault when FMI present")
	}
}

func TestVictronChargerErrorText(t *testing.T) {
	if got := VictronChargerErrorText(67); got != "BMS connection lost" {
		t.Errorf("code 67 = %q, want BMS connection lost", got)
	}
	if got := VictronChargerErrorText(0); got != "no error" {
		t.Errorf("code 0 = %q, want no error", got)
	}
	if got := VictronChargerErrorText(9999); got != "" {
		t.Errorf("unknown code = %q, want empty", got)
	}
}

func ptrTo[T any](v T) *T { return &v }
