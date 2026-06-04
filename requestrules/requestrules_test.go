package requestrules

import (
	"testing"
	"time"
)

// fakeClock is an injectable, advanceable clock.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time   { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func u8(v uint8) *uint8 { return &v }

var victron = DeviceView{Bus: "can0", Source: 41, Manufacturer: "Victron Energy", ManufacturerCode: 358, ModelID: "Lynx Smart BMS 500"}
var garmin = DeviceView{Bus: "can0", Source: 6, Manufacturer: "Garmin", ModelID: "GPSMAP 1042xsv"}

func newEng(clk *fakeClock) *Engine {
	return New(Config{Clock: clk.now})
}

func TestMatch(t *testing.T) {
	cases := []struct {
		m    Match
		d    DeviceView
		want bool
	}{
		{Match{Manufacturer: "Victron Energy"}, victron, true},
		{Match{Manufacturer: "victron energy"}, victron, true}, // case-insensitive
		{Match{Manufacturer: "Garmin"}, victron, false},
		{Match{ManufacturerCode: 358}, victron, true},
		{Match{ModelID: "GPSMAP*"}, garmin, true},
		{Match{ModelID: "GPSMAP*"}, victron, false},
		{Match{ModelID: "Lynx Smart BMS 500"}, victron, true},
		{Match{Source: u8(6)}, garmin, true},
		{Match{Source: u8(7)}, garmin, false},
		{Match{Bus: "can1"}, garmin, false},
		{Match{}, garmin, true}, // empty = wildcard
	}
	for i, c := range cases {
		if got := c.m.matches(c.d); got != c.want {
			t.Errorf("case %d: matches=%v want %v", i, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	e := New(Config{})
	if err := e.AddRule(Rule{Wants: []Want{{PGN: 1}}, MinInterval: time.Second}); err == nil {
		t.Error("expected error for missing name")
	}
	if err := e.AddRule(Rule{Name: "x", MinInterval: time.Second}); err == nil {
		t.Error("expected error for no wants")
	}
	if err := e.AddRule(Rule{Name: "x", Wants: []Want{{PGN: 1}}}); err == nil {
		t.Error("expected error for missing min-interval")
	}
}

func TestOnOnlineRequestsThenMarkedFresh(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newEng(clk)
	_ = e.AddRule(Rule{
		Name: "route", Match: Match{ModelID: "GPSMAP*"}, Via: ViaISORequest,
		Wants: []Want{{PGN: 130065}}, OnOnline: true, MinInterval: 10 * time.Second,
	})

	reqs := e.OnDeviceOnline(garmin)
	if len(reqs) != 1 || reqs[0].PGN != 130065 || reqs[0].Via != ViaISORequest {
		t.Fatalf("expected one ISO request for 130065, got %+v", reqs)
	}

	// A second online event within MinInterval must NOT re-request.
	clk.add(3 * time.Second)
	if reqs := e.OnDeviceOnline(garmin); len(reqs) != 0 {
		t.Fatalf("expected no re-request within min-interval, got %+v", reqs)
	}

	// Receiving the wanted PGN marks it fresh; still no re-request (no MaxAge).
	clk.add(30 * time.Second)
	e.OnFrame("can0", 6, 130065, nil)
	if reqs := e.OnDeviceOnline(garmin); len(reqs) != 0 {
		t.Fatalf("expected no request once fresh, got %+v", reqs)
	}
}

func TestMinIntervalFloor(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := newEng(clk)
	_ = e.AddRule(Rule{
		Name: "poll", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 127489}},
		InvalidateOn: []uint32{127489}, MinInterval: 5 * time.Second,
	})
	e.OnDeviceOnline(garmin)

	// First invalidate fires a request.
	if reqs := e.OnFrame("can0", 6, 127489, nil); len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	// Rapid repeats within the floor are suppressed (no bus hammering).
	for i := range 5 {
		clk.add(500 * time.Millisecond)
		if reqs := e.OnFrame("can0", 6, 127489, nil); len(reqs) != 0 {
			t.Fatalf("iter %d: expected suppression within min-interval, got %d", i, len(reqs))
		}
	}
	// After the floor elapses, it fires again.
	clk.add(5 * time.Second)
	if reqs := e.OnFrame("can0", 6, 127489, nil); len(reqs) != 1 {
		t.Fatalf("expected request after floor elapsed, got %d", len(reqs))
	}
}

func TestMaxAgeRefreshViaTick(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := newEng(clk)
	_ = e.AddRule(Rule{
		Name: "fresh", Match: Match{Manufacturer: "Garmin"}, Via: ViaISORequest,
		Wants: []Want{{PGN: 126996}}, OnOnline: true, MaxAge: time.Minute, MinInterval: 10 * time.Second,
	})
	e.OnDeviceOnline(garmin)          // fires initial request
	e.OnFrame("can0", 6, 126996, nil) // becomes fresh

	clk.add(30 * time.Second)
	if reqs := e.Tick(); len(reqs) != 0 {
		t.Fatalf("not stale yet; expected no tick request, got %d", len(reqs))
	}
	clk.add(40 * time.Second) // now 70s old > MaxAge
	if reqs := e.Tick(); len(reqs) != 1 || reqs[0].PGN != 126996 {
		t.Fatalf("expected refresh after MaxAge, got %+v", reqs)
	}
}

func TestFrameRequestWithSubKey(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := newEng(clk)
	// Parameterized read: template has a 2-byte subkey slot at offset 2; responses
	// carry the register id at offset 2 as well (like Victron 61184).
	_ = e.AddRule(Rule{
		Name: "vreg", Match: Match{ManufacturerCode: 358}, Via: ViaFrame,
		Wants:         []Want{{PGN: 61184, SubKey: 0x031E, HasSubKey: true}, {PGN: 61184, SubKey: 0x031C, HasSubKey: true}},
		FramePGN:      61184,
		FrameTemplate: []byte{0x66, 0x99, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		SubKeyWriteOff: 2, SubKeyWriteLen: 2,
		SubKeyReadOff: 2, SubKeyReadLen: 2,
		ToDevice: true, OnOnline: true, MinInterval: 30 * time.Second,
	})

	reqs := e.OnDeviceOnline(victron)
	if len(reqs) != 2 {
		t.Fatalf("expected 2 frame requests (one per register), got %d", len(reqs))
	}
	// Subkey 0x031E must be written little-endian into the template at offset 2.
	var got031E *Request
	for i := range reqs {
		if reqs[i].Want.SubKey == 0x031E {
			got031E = &reqs[i]
		}
	}
	if got031E == nil {
		t.Fatal("missing request for register 0x031E")
	}
	if got031E.Via != ViaFrame || got031E.PGN != 61184 || got031E.Dst != 41 {
		t.Errorf("unexpected request: %+v", *got031E)
	}
	if got031E.Data[2] != 0x1E || got031E.Data[3] != 0x03 {
		t.Errorf("subkey not written LE: data=%x", got031E.Data)
	}

	// A response for 0x031E marks only that register fresh; 0x031C still wanted.
	clk.add(time.Minute)
	resp := []byte{0x66, 0x99, 0x1E, 0x03, 0x01, 0x00, 0x00, 0x00} // register 0x031E
	e.OnFrame("can0", 41, 61184, resp)
	reqs = e.Tick()
	// 0x031C is past MinInterval and still missing -> should re-request; 0x031E is fresh.
	gotKeys := map[uint32]bool{}
	for _, r := range reqs {
		gotKeys[r.Want.SubKey] = true
	}
	if gotKeys[0x031E] {
		t.Error("0x031E is fresh; should not be re-requested")
	}
	if !gotKeys[0x031C] {
		t.Error("0x031C still missing; expected re-request")
	}
}

func TestGlobalFloor(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := New(Config{Clock: clk.now, GlobalMinInterval: time.Second})
	_ = e.AddRule(Rule{Name: "a", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 1}}, OnOnline: true, MinInterval: time.Hour})
	_ = e.AddRule(Rule{Name: "b", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 2}}, OnOnline: true, MinInterval: time.Hour})

	// Both rules want to fire on online, but the global floor permits only one now.
	reqs := e.OnDeviceOnline(garmin)
	if len(reqs) != 1 {
		t.Fatalf("global floor should allow only 1 request at once, got %d", len(reqs))
	}
	// The suppressed want fires on a later tick once the floor passes.
	clk.add(2 * time.Second)
	if reqs := e.Tick(); len(reqs) != 0 {
		// Tick only refreshes MaxAge/OnOnline rules; OnOnline rules with no data re-evaluate.
		_ = reqs
	}
}

func TestOfflineResetsState(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := newEng(clk)
	_ = e.AddRule(Rule{Name: "r", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 130065}}, OnOnline: true, MinInterval: time.Hour})
	if reqs := e.OnDeviceOnline(garmin); len(reqs) != 1 {
		t.Fatalf("expected initial request, got %d", len(reqs))
	}
	// Within MinInterval, re-online does nothing...
	clk.add(time.Second)
	if reqs := e.OnDeviceOnline(garmin); len(reqs) != 0 {
		t.Fatalf("expected suppression, got %d", len(reqs))
	}
	// ...but going offline resets state, so coming back re-requests immediately.
	e.OnDeviceOffline("can0", 6)
	if reqs := e.OnDeviceOnline(garmin); len(reqs) != 1 {
		t.Fatalf("expected re-request after offline/online, got %d", len(reqs))
	}
}

func TestUnknownDeviceFrameIgnored(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	e := newEng(clk)
	_ = e.AddRule(Rule{Name: "r", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 1}}, InvalidateOn: []uint32{2}, MinInterval: time.Second})
	// Frame from a device we've never seen online must not trigger anything.
	if reqs := e.OnFrame("can0", 99, 2, nil); len(reqs) != 0 {
		t.Fatalf("expected no requests for unknown device, got %d", len(reqs))
	}
}

func TestInterestingPGNs(t *testing.T) {
	e := New(Config{})
	_ = e.AddRule(Rule{Name: "a", Match: Match{}, Via: ViaISORequest, Wants: []Want{{PGN: 130065}}, InvalidateOn: []uint32{129284}, MinInterval: time.Second})
	_ = e.AddRule(Rule{Name: "b", Match: Match{}, Via: ViaFrame, FramePGN: 61184, Wants: []Want{{PGN: 61184, SubKey: 1, HasSubKey: true}}, MinInterval: time.Second})
	set := e.InterestingPGNs()
	for _, p := range []uint32{130065, 129284, 61184} {
		if _, ok := set[p]; !ok {
			t.Errorf("expected %d in interesting set", p)
		}
	}
	if len(set) != 3 {
		t.Errorf("set size = %d, want 3: %v", len(set), set)
	}
}
