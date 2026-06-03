package main

import (
	"testing"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

func p[T any](v T) *T { return &v }

func TestExtractAlarms_WS500RedFault(t *testing.T) {
	d := pgn.RVCISODiagnostics{
		RedLamp:    p(pgn.RVCLampStatusOn),
		YellowLamp: p(pgn.RVCLampStatusOn),
		SpnMsb:     p[uint8](254), SpnIsb: p[uint8](5), SpnLsb: p[uint8](2),
		Fmi: p[uint8](2),
	}
	recs := extractAlarms(d, 128, time.Unix(0, 0).UTC())
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.Severity != "red" || r.Code != "FMI 2" || r.Detail != "data erratic or incorrect" {
		t.Errorf("unexpected record: %+v", r)
	}
	if r.SPN == nil || *r.SPN != 520234 {
		t.Errorf("SPN = %v, want 520234", r.SPN)
	}
}

func TestExtractAlarms_NoFault(t *testing.T) {
	d := pgn.RVCISODiagnostics{
		RedLamp:    p(pgn.RVCLampStatusOff),
		YellowLamp: p(pgn.RVCLampStatusOff),
	}
	if recs := extractAlarms(d, 128, time.Now()); len(recs) != 0 {
		t.Errorf("expected no records for clear diagnostics, got %d", len(recs))
	}
}

func TestExtractAlarms_VictronChargerError(t *testing.T) {
	d := pgn.VictronBatteryRegister{Register: pgn.VictronChargerErrorRegister, Payload: p[uint32](67)}
	recs := extractAlarms(d, 36, time.Unix(0, 0).UTC())
	if len(recs) != 1 || recs[0].Detail != "BMS connection lost" || recs[0].Severity != "error" {
		t.Fatalf("unexpected records: %+v", recs)
	}

	// Zero payload = no error = no alarm.
	ok := pgn.VictronBatteryRegister{Register: pgn.VictronChargerErrorRegister, Payload: p[uint32](0)}
	if recs := extractAlarms(ok, 36, time.Now()); len(recs) != 0 {
		t.Errorf("expected no records for error code 0, got %d", len(recs))
	}

	// A different register is not an alarm.
	other := pgn.VictronBatteryRegister{Register: 0x0FFF, Payload: p[uint32](8432)}
	if recs := extractAlarms(other, 41, time.Now()); len(recs) != 0 {
		t.Errorf("expected no records for non-error register, got %d", len(recs))
	}
}

func TestGroupEpisodes(t *testing.T) {
	base := time.Date(2026, 6, 1, 22, 0, 0, 0, time.UTC)
	mk := func(sec int) alarmRecord {
		return alarmRecord{
			Time: base.Add(time.Duration(sec) * time.Second), Src: 128,
			Severity: "yellow", Source: "WS500 DM1", Code: "FMI 20", Detail: "data drifted high",
		}
	}
	// Three within gap -> one episode; a fourth far away -> second episode.
	recs := []alarmRecord{mk(0), mk(5), mk(10), mk(600)}
	eps := groupEpisodes(recs, 2*time.Minute)
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}
	if eps[0].Count != 3 || !eps[0].First.Equal(base) || !eps[0].Last.Equal(base.Add(10*time.Second)) {
		t.Errorf("episode 0 wrong: %+v", eps[0])
	}
	if eps[1].Count != 1 {
		t.Errorf("episode 1 count = %d, want 1", eps[1].Count)
	}
}

func TestGroupEpisodes_DistinctKeysDoNotMerge(t *testing.T) {
	base := time.Date(2026, 6, 1, 22, 0, 0, 0, time.UTC)
	recs := []alarmRecord{
		{Time: base, Src: 128, Severity: "red", Source: "WS500 DM1", Code: "FMI 2", Detail: "x"},
		{Time: base.Add(time.Second), Src: 128, Severity: "yellow", Source: "WS500 DM1", Code: "FMI 20", Detail: "y"},
	}
	eps := groupEpisodes(recs, time.Minute)
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2 (distinct codes)", len(eps))
	}
}
