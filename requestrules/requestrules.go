// Package requestrules implements a declarative engine for polling on-demand
// NMEA 2000 data: send a request when a matching device comes online (or at
// startup), keep the data fresh, and re-request when a trigger PGN is seen —
// without duplicating ad-hoc request code per device/PGN.
//
// The engine is transport-agnostic and has no dependency on the root lplex
// package (it operates on DeviceView, returns Request values). A driver (the
// broker) feeds it device/frame events and a clock tick, and transmits the
// Requests it returns.
//
// Timing safety: every rule has a MinInterval — the floor between successive
// requests for the same (device, datum). The engine will never emit a request
// for the same target more often than MinInterval, regardless of how many
// triggers fire. An optional global floor caps the rate across all rules.
package requestrules

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DeviceView is the minimal device identity the engine matches against. The
// driver builds it from its own device registry (avoids an import cycle).
type DeviceView struct {
	Bus              string
	Source           uint8
	NAMEHex          string
	Manufacturer     string
	ManufacturerCode uint16
	DeviceClass      uint8
	DeviceFunction   uint8
	DeviceInstance   uint8
	ModelID          string
	ProductCode      uint16
}

// Match selects which devices a rule applies to. All set fields must match
// (logical AND); zero/empty fields are wildcards. ModelID matches
// case-insensitively and supports a single trailing '*' for prefix matching.
type Match struct {
	Manufacturer     string
	ManufacturerCode uint16 // 0 = any
	ModelID          string // exact, or "Prefix*"
	DeviceClass      *uint8
	DeviceFunction   *uint8
	Name             string // CAN NAME hex, exact (case-insensitive)
	Source           *uint8
	Bus              string
}

func (m Match) matches(d DeviceView) bool {
	if m.Manufacturer != "" && !strings.EqualFold(m.Manufacturer, d.Manufacturer) {
		return false
	}
	if m.ManufacturerCode != 0 && m.ManufacturerCode != d.ManufacturerCode {
		return false
	}
	if m.ModelID != "" && !globMatch(m.ModelID, d.ModelID) {
		return false
	}
	if m.DeviceClass != nil && *m.DeviceClass != d.DeviceClass {
		return false
	}
	if m.DeviceFunction != nil && *m.DeviceFunction != d.DeviceFunction {
		return false
	}
	if m.Name != "" && !strings.EqualFold(m.Name, d.NAMEHex) {
		return false
	}
	if m.Source != nil && *m.Source != d.Source {
		return false
	}
	if m.Bus != "" && m.Bus != d.Bus {
		return false
	}
	return true
}

func globMatch(pattern, s string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
	}
	return strings.EqualFold(pattern, s)
}

// Via is how a request is transmitted.
type Via int

const (
	// ViaISORequest sends an ISO Request (PGN 59904) for the wanted PGN.
	ViaISORequest Via = iota
	// ViaFrame sends a templated frame, substituting the want's SubKey.
	ViaFrame
)

// Want is one datum a rule keeps available. For ViaISORequest only PGN is used.
// For ViaFrame, SubKey identifies the parameter (e.g. a Victron register id):
// it is written into the request frame template and matched against responses
// so distinct sub-keyed values are tracked independently.
type Want struct {
	PGN       uint32
	SubKey    uint32
	HasSubKey bool
}

func (w Want) key() uint64 { return uint64(w.PGN)<<32 | uint64(w.SubKey) }

// Rule is a declarative request rule. Construct directly (Go API) or via config.
type Rule struct {
	Name  string
	Match Match
	Wants []Want
	Via   Via

	// Destination. If ToDevice is true the request is sent to the matched
	// device's source address; otherwise to Dst (0 means 0xFF broadcast).
	ToDevice bool
	Dst      uint8

	// ViaFrame parameters.
	FramePGN       uint32
	FramePriority  uint8
	FrameTemplate  []byte // copied per request; SubKey written in if SubKeyWriteLen>0
	SubKeyWriteOff int    // byte offset in template to write the want's SubKey (LE)
	SubKeyWriteLen int    // 0 = don't write a subkey into the frame
	SubKeyReadOff  int    // byte offset in a response to read the subkey (LE)
	SubKeyReadLen  int    // 0 = responses are not sub-keyed

	// Timing.
	MinInterval  time.Duration // REQUIRED: floor between requests for the same (device, want)
	MaxAge       time.Duration // refresh when the datum is older than this; 0 = request once
	OnOnline     bool          // request when a matching device appears / at startup
	InvalidateOn []uint32      // PGNs that, when seen from the device, mark wants stale
}

func (r *Rule) validate() error {
	if r.Name == "" {
		return fmt.Errorf("rule has no name")
	}
	if len(r.Wants) == 0 {
		return fmt.Errorf("rule %q has no wants", r.Name)
	}
	if r.MinInterval <= 0 {
		return fmt.Errorf("rule %q: min-interval must be > 0", r.Name)
	}
	if r.Via == ViaFrame && r.FramePGN == 0 {
		return fmt.Errorf("rule %q: frame request needs a frame pgn", r.Name)
	}
	return nil
}

// Request is an action the driver should transmit.
type Request struct {
	Bus      string
	Dst      uint8
	Via      Via
	PGN      uint32 // ViaISORequest: PGN to request; ViaFrame: the frame's PGN
	Data     []byte // ViaFrame: the payload to send
	Priority uint8
	Rule     string // originating rule name (for logging)
	Want     Want
}

type devKey struct {
	bus string
	src uint8
}

type trackKey struct {
	rule int
	dev  devKey
	want uint64
}

type trackState struct {
	lastRequested time.Time
	lastReceived  time.Time
	haveData      bool
}

// Engine evaluates rules against device/frame events. All methods are safe for
// concurrent use, but in practice the driver calls them from one goroutine.
type Engine struct {
	mu           sync.Mutex
	clock        func() time.Time
	globalMin    time.Duration
	lastGlobal   time.Time
	rules        []*Rule
	byInvalidate map[uint32][]int // PGN -> rule indices that invalidate on it
	devices      map[devKey]DeviceView
	track        map[trackKey]*trackState
}

// Config configures a new Engine.
type Config struct {
	// GlobalMinInterval optionally caps the rate across ALL rules: no two
	// requests are emitted closer together than this. 0 = no global cap.
	GlobalMinInterval time.Duration
	// Clock is injectable for tests; nil uses time.Now.
	Clock func() time.Time
}

// New creates an Engine. Add rules with AddRule.
func New(cfg Config) *Engine {
	clk := cfg.Clock
	if clk == nil {
		clk = time.Now
	}
	return &Engine{
		clock:        clk,
		globalMin:    cfg.GlobalMinInterval,
		byInvalidate: map[uint32][]int{},
		devices:      map[devKey]DeviceView{},
		track:        map[trackKey]*trackState{},
	}
}

// AddRule registers a rule. Returns an error if the rule is invalid.
func (e *Engine) AddRule(r Rule) error {
	if err := r.validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	idx := len(e.rules)
	rc := r
	e.rules = append(e.rules, &rc)
	for _, p := range r.InvalidateOn {
		e.byInvalidate[p] = append(e.byInvalidate[p], idx)
	}
	return nil
}

// OnDeviceOnline records a device as present and returns any requests triggered
// by OnOnline rules. Call when a device is first seen or its identity updates.
func (e *Engine) OnDeviceOnline(d DeviceView) []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.devices[devKey{d.Bus, d.Source}] = d
	now := e.clock()
	var out []Request
	for idx, r := range e.rules {
		if !r.OnOnline || !r.Match.matches(d) {
			continue
		}
		for _, w := range r.Wants {
			if req, ok := e.consider(idx, r, d, w, now); ok {
				out = append(out, req)
			}
		}
	}
	return out
}

// InterestingPGNs returns the set of PGNs the engine cares about in OnFrame:
// the PGNs that mark a want fresh, plus the invalidation triggers. A driver
// (e.g. the broker) can check membership lock-free on its hot path and only
// call OnFrame for relevant frames, avoiding per-frame engine work.
func (e *Engine) InterestingPGNs() map[uint32]struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := map[uint32]struct{}{}
	for _, r := range e.rules {
		for _, w := range r.Wants {
			if r.Via == ViaFrame {
				set[r.FramePGN] = struct{}{}
			} else {
				set[w.PGN] = struct{}{}
			}
		}
		for _, p := range r.InvalidateOn {
			set[p] = struct{}{}
		}
	}
	return set
}

// OnDeviceOffline forgets a device so its freshness state is reset.
func (e *Engine) OnDeviceOffline(bus string, src uint8) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dk := devKey{bus, src}
	delete(e.devices, dk)
	for k := range e.track {
		if k.dev == dk {
			delete(e.track, k)
		}
	}
}

// OnFrame processes an incoming frame: it marks wanted data fresh (so the
// engine knows the datum is now available as state) and applies invalidation
// triggers, returning any re-requests. data may be nil if not needed.
func (e *Engine) OnFrame(bus string, src uint8, pgn uint32, data []byte) []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	dk := devKey{bus, src}
	d, known := e.devices[dk]
	if !known {
		return nil
	}
	now := e.clock()

	// 1) Mark any matching want fresh (the response we were after).
	for idx, r := range e.rules {
		if !r.Match.matches(d) {
			continue
		}
		for _, w := range r.Wants {
			if !wantMatchesFrame(r, w, pgn, data) {
				continue
			}
			ts := e.stateFor(idx, dk, w)
			ts.haveData = true
			ts.lastReceived = now
		}
	}

	// 2) Invalidation triggers -> mark stale and maybe re-request.
	var out []Request
	for _, idx := range e.byInvalidate[pgn] {
		r := e.rules[idx]
		if !r.Match.matches(d) {
			continue
		}
		for _, w := range r.Wants {
			ts := e.stateFor(idx, dk, w)
			ts.haveData = false
			if req, ok := e.consider(idx, r, d, w, now); ok {
				out = append(out, req)
			}
		}
	}
	return out
}

// Tick drives MaxAge-based refresh. The driver calls it periodically (e.g. 1 Hz).
func (e *Engine) Tick() []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock()
	var out []Request
	for idx, r := range e.rules {
		if r.MaxAge <= 0 && !r.OnOnline {
			continue
		}
		for dk, d := range e.devices {
			_ = dk
			if !r.Match.matches(d) {
				continue
			}
			for _, w := range r.Wants {
				if req, ok := e.consider(idx, r, d, w, now); ok {
					out = append(out, req)
				}
			}
		}
	}
	return out
}

// consider decides whether to request (rule, device, want) now, enforcing the
// per-target MinInterval, the global floor, and freshness need. Must hold mu.
func (e *Engine) consider(idx int, r *Rule, d DeviceView, w Want, now time.Time) (Request, bool) {
	ts := e.stateFor(idx, devKey{d.Bus, d.Source}, w)

	// Per-target minimum interval: never re-request the same datum too soon.
	if !ts.lastRequested.IsZero() && now.Sub(ts.lastRequested) < r.MinInterval {
		return Request{}, false
	}
	// Freshness: request only if we lack the datum, or it has aged out.
	if ts.haveData {
		if r.MaxAge <= 0 || now.Sub(ts.lastReceived) < r.MaxAge {
			return Request{}, false
		}
	}
	// Global floor across all rules.
	if e.globalMin > 0 && !e.lastGlobal.IsZero() && now.Sub(e.lastGlobal) < e.globalMin {
		return Request{}, false
	}

	ts.lastRequested = now
	e.lastGlobal = now
	return e.build(r, d, w), true
}

func (e *Engine) build(r *Rule, d DeviceView, w Want) Request {
	dst := r.Dst
	if r.ToDevice {
		dst = d.Source
	} else if dst == 0 {
		dst = 0xFF
	}
	req := Request{Bus: d.Bus, Dst: dst, Via: r.Via, Priority: r.FramePriority, Rule: r.Name, Want: w}
	switch r.Via {
	case ViaISORequest:
		req.PGN = w.PGN
	case ViaFrame:
		req.PGN = r.FramePGN
		payload := make([]byte, len(r.FrameTemplate))
		copy(payload, r.FrameTemplate)
		if w.HasSubKey && r.SubKeyWriteLen > 0 {
			writeLE(payload, r.SubKeyWriteOff, r.SubKeyWriteLen, w.SubKey)
		}
		req.Data = payload
	}
	return req
}

func (e *Engine) stateFor(idx int, dk devKey, w Want) *trackState {
	k := trackKey{idx, dk, w.key()}
	ts := e.track[k]
	if ts == nil {
		ts = &trackState{}
		e.track[k] = ts
	}
	return ts
}

// wantMatchesFrame reports whether an incoming (pgn, data) is the response for
// want w under rule r — i.e. the datum we wanted is now available.
func wantMatchesFrame(r *Rule, w Want, pgn uint32, data []byte) bool {
	switch r.Via {
	case ViaISORequest:
		return w.PGN == pgn
	case ViaFrame:
		if pgn != r.FramePGN {
			return false
		}
		if !w.HasSubKey || r.SubKeyReadLen == 0 {
			return true
		}
		got, ok := readLE(data, r.SubKeyReadOff, r.SubKeyReadLen)
		return ok && got == w.SubKey
	}
	return false
}

func writeLE(b []byte, off, length int, v uint32) {
	for i := 0; i < length && off+i < len(b); i++ {
		b[off+i] = byte(v >> (8 * i))
	}
}

func readLE(b []byte, off, length int) (uint32, bool) {
	if length <= 0 || length > 4 || off < 0 || off+length > len(b) {
		return 0, false
	}
	var v uint32
	switch length {
	case 1:
		v = uint32(b[off])
	case 2:
		v = uint32(binary.LittleEndian.Uint16(b[off : off+2]))
	default:
		var tmp [4]byte
		copy(tmp[:], b[off:off+length])
		v = binary.LittleEndian.Uint32(tmp[:])
	}
	return v, true
}
