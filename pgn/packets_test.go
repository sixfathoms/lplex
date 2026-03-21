package pgn

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// packetTest defines a single PGN packet test vector.
//
// To add a new test: append an entry to the packetTests slice below.
// Fields:
//   - desc:    human-readable description (shown on failure)
//   - pgn:     the PGN number
//   - hex:     the raw packet data as a hex string (as output by lplex)
//   - want:    the expected decoded struct value
//   - epsilon: tolerance for floating-point comparisons (0 means use default 1e-6)
//   - noRoundTrip: set true to skip encode-back-to-hex verification
//                   (useful for PGNs where encode is lossy or not implemented)
type packetTest struct {
	desc        string
	pgn         uint32
	hex         string
	want        any
	epsilon     float64
	noRoundTrip bool
}

// packetTests contains all reference test vectors for PGN decode/encode verification.
// Organized by PGN number for easy navigation. Each entry represents a real or
// hand-crafted packet with known expected values.
//
// Adding new entries:
//  1. Capture hex data from lplex (the "data" field in JSON output)
//  2. Create the expected struct with the decoded values
//  3. Add a descriptive string explaining what the packet represents
var packetTests = []packetTest{
	// ---- PGN 59904: ISO Request ----
	{
		desc: "ISO Request for Address Claim (PGN 60928)",
		pgn:  59904,
		hex:  "00ee00",
		want: ISORequest{RequestedPgn: 60928},
	},

	// ---- PGN 126992: System Time ----
	{
		desc: "system time epoch day 20000, midnight",
		pgn:  126992,
		hex:  "ff00204e00000000",
		want: SystemTime{
			Sid:           0xff,
			TimeSource:    0,
			DaysSince1970: 20000,
			SecondsToday:  ptr(0.0),
		},
	},

	// ---- PGN 127250: Vessel Heading ----
	{
		desc: "vessel heading ~180° true",
		pgn:  127250,
		hex:  "ff107b0000000000",
		// heading = 0x7B10 = 31504 * 0.0001 = 3.1504 rad
		want: VesselHeading{
			Sid:              0xff,
			Heading:          ptr(3.1504),
			Deviation:        ptr(0.0),
			Variation:        ptr(0.0),
			HeadingReference: HeadingReferenceTrue,
		},
	},
	{
		desc: "vessel heading ~45° magnetic with deviation and variation",
		pgn:  127250,
		hex:  "00ae1ef6ff0a0001",
		// heading = 0x1eae = 7854 -> 0.7854 rad ≈ 45°
		// deviation = 0xfff6 = -10 -> -0.001 rad
		// variation = 0x000a = 10 -> 0.001 rad
		// heading_reference = 1 (magnetic)
		want: VesselHeading{
			Sid:              0,
			Heading:          ptr(0.7854),
			Deviation:        ptr(-0.001),
			Variation:        ptr(0.001),
			HeadingReference: HeadingReferenceMagnetic,
		},
		epsilon: 1e-4,
	},

	// ---- PGN 127251: Rate of Turn ----
	{
		desc: "rate of turn zero",
		pgn:  127251,
		hex:  "ff00000000ffffff",
		want: RateOfTurn{
			Sid:  0xff,
			Rate: ptr(0.0),
		},
	},

	// ---- PGN 127257: Attitude ----
	{
		desc: "attitude level (all zeros)",
		pgn:  127257,
		hex:  "0000000000000000",
		want: Attitude{
			Sid:   0,
			Yaw:   ptr(0.0),
			Pitch: ptr(0.0),
			Roll:  ptr(0.0),
		},
	},
	{
		desc: "attitude with pitch 0.1 rad and roll 0.001 rad",
		pgn:  127257,
		hex:  "ff0000e8030a00ff",
		// yaw = 0x0000 = 0
		// pitch = 0x03e8 = 1000 -> 0.1 rad
		// roll = 0x000a = 10 -> 0.001 rad
		want: Attitude{
			Sid:   0xff,
			Yaw:   ptr(0.0),
			Pitch: ptr(0.1),
			Roll:  ptr(0.001),
		},
		epsilon: 1e-4,
	},

	// ---- PGN 127258: Magnetic Variation ----
	{
		desc: "magnetic variation -0.01 rad, day 20000",
		pgn:  127258,
		hex:  "fff1204e9cffffff",
		// source = 1 (magnetic), days = 20000, variation = -100 -> -0.01 rad
		want: MagneticVariation{
			Sid:           0xff,
			Source:        HeadingReferenceMagnetic,
			DaysSince1970: 20000,
			Variation:     ptr(-0.01),
		},
		epsilon: 1e-4,
	},

	// ---- PGN 128259: Speed Water Referenced ----
	{
		desc: "speed 3.5 m/s water, 4.0 m/s ground, paddle wheel",
		pgn:  128259,
		hex:  "005e019001f0ff",
		// speed_water = 0x015e = 350 -> 3.50 m/s
		// speed_ground = 0x0190 = 400 -> 4.00 m/s
		// speed_type = 0 (paddle_wheel)
		want: SpeedWaterReferenced{
			Sid:         0,
			SpeedWater:  ptr(3.5),
			SpeedGround: ptr(4.0),
			SpeedType:   SpeedTypePaddleWheel,
		},
	},

	// ---- PGN 128267: Water Depth ----
	{
		desc: "Airmar water depth 5.73m, offset -1.371m, range 140m",
		pgn:  128267,
		hex:  "ff3d020000a5fa0e",
		want: WaterDepth{
			Sid:    0xff,
			Depth:  ptr(5.73),
			Offset: ptr(-1.371),
			Range:  ptr(140.0),
		},
	},
	{
		desc: "shallow water 1.5m, no offset",
		pgn:  128267,
		hex:  "00960000000000ff",
		// depth = 150 -> 1.50 m, offset = 0, range = 0xff = not available
		want: WaterDepth{
			Sid:    0,
			Depth:  ptr(1.50),
			Offset: ptr(0.0),
			Range:  nil,
		},
	},

	// ---- PGN 127505: Fluid Level ----
	{
		desc: "fuel tank 75% of 200L",
		pgn:  127505,
		hex:  "053e49d0070000ff",
		// instance = 5 (bits 3:0), fluid_type = 0 (bits 7:4)
		// level = 18750 * 0.004 = 75.0%
		// capacity = 2000 * 0.1 = 200.0 L
		want: FluidLevel{
			Instance:  5,
			FluidType: 0,
			Level:     ptr(75.0),
			Capacity:  ptr(200.0),
		},
		epsilon: 0.01,
	},

	// ---- PGN 127508: Battery Status ----
	{
		desc: "battery 202.12V, -45.6A, 15.30K",
		pgn:  127508,
		hex:  "00f44e38fefa0500",
		// voltage = 0x4ef4 = 20212 * 0.01 = 202.12V
		// current = 0xfe38 = -456 as int16 * 0.1 = -45.6A
		// temperature = 0x05fa = 1530 * 0.01 = 15.30K
		want: BatteryStatus{
			Instance:    0,
			Voltage:     ptr(202.12),
			Current:     ptr(-45.6),
			Temperature: ptr(15.30),
			Sid:         0,
		},
		epsilon: 0.1,
	},

	// ---- PGN 129025: Position Rapid Update ----
	{
		desc: "Seattle 47.6062°N, -122.3321°W",
		pgn:  129025,
		hex:  "3021601c589a15b7",
		// lat = 476062000 * 1e-7 = 47.6062
		// lon = -1223321000 * 1e-7 = -122.3321
		want: PositionRapidUpdate{
			Latitude:  ptr(47.6062),
			Longitude: ptr(-122.3321),
		},
		epsilon: 1e-4,
	},
	{
		desc: "null island (0°, 0°)",
		pgn:  129025,
		hex:  "0000000000000000",
		want: PositionRapidUpdate{
			Latitude:  ptr(0.0),
			Longitude: ptr(0.0),
		},
	},
	{
		desc: "Sydney -33.8688°S, 151.2093°E",
		pgn:  129025,
		hex:  "0008d0eb48b5205a",
		// lat = -338688000 * 1e-7 = -33.8688
		// lon = 1512093000 * 1e-7 = 151.2093
		want: PositionRapidUpdate{
			Latitude:  ptr(-33.8688),
			Longitude: ptr(151.2093),
		},
		epsilon: 1e-4,
	},

	// ---- PGN 129026: COG & SOG Rapid Update ----
	{
		desc: "COG ~90° true, SOG 5.0 m/s",
		pgn:  129026,
		hex:  "fffc5c3df401ffff",
		// cog_reference = 0 (true)
		// cog = 0x3d5c = 15708 -> 1.5708 rad ≈ 90°
		// sog = 0x01f4 = 500 -> 5.00 m/s
		want: COGSOGRapidUpdate{
			Sid:          0xff,
			CogReference: HeadingReferenceTrue,
			Cog:          ptr(1.5708),
			Sog:          ptr(5.0),
		},
		epsilon: 0.001,
	},

	// ---- PGN 130306: Wind Data ----
	{
		desc: "apparent wind 5.5 m/s at 1.2345 rad",
		pgn:  130306,
		hex:  "012602393002ffff",
		// speed = 0x0226 = 550 -> 5.50 m/s
		// angle = 0x3039 = 12345 -> 1.2345 rad
		// wind_reference = 2 (apparent)
		want: WindData{
			Sid:           1,
			WindSpeed:     ptr(5.50),
			WindAngle:     ptr(1.2345),
			WindReference: WindReferenceApparent,
		},
	},
	{
		desc: "true wind 10 m/s from north",
		pgn:  130306,
		hex:  "00e803000000ffff",
		// speed = 0x03e8 = 1000 -> 10.00 m/s
		// angle = 0 -> 0 rad
		// reference = 0 (true_north)
		want: WindData{
			Sid:           0,
			WindSpeed:     ptr(10.0),
			WindAngle:     ptr(0.0),
			WindReference: WindReferenceTrueNorth,
		},
	},

	// ---- PGN 129794: AIS Class A Static and Voyage Related Data ----
	{
		desc: "AIS Class A static: CONTINUUM heading to EVERETT",
		pgn:  129794,
		hex:  "05823df315ffffffff57444e32343738434f4e54494e55554d202020202020202020202025c800320014007800ffffffffffff8c00455645524554542020202020202020202020202001e1",
		want: AISClassAStaticAndVoyageRelatedData{
			MessageId:            5,
			RepeatIndicator:      0,
			UserId:               368262530,
			ImoNumber:            0xFFFFFFFF,
			Callsign:             "WDN2478",
			Name:                 "CONTINUUM",
			ShipType:             37,
			ShipLength:           ptr(20.0),
			ShipBeam:             ptr(5.0),
			PositionRefStarboard: ptr(2.0),
			PositionRefBow:       ptr(12.0),
			EtaDate:              0xFFFF,
			EtaTime:              nil, // 0xFFFFFFFF = not available
			Draught:              ptr(1.40),
			Destination:          "EVERETT",
			AisVersionIndicator:  1,
			GnssType:             PositionFixTypeUndefined,
			Dte:                  0,
			AisTransceiverInfo:   AISTransceiverChannelBVdl,
		},
		epsilon: 0.01,
	},

	// ---- PGN 130312: Temperature ----
	{
		desc: "sea water temperature 293.15K (20°C)",
		pgn:  130312,
		hex:  "ff000283720000ff",
		// instance = 0, source = 2 (sea_water)
		// actual = 0x7283 = 29315 * 0.01 = 293.15K
		// set = 0
		want: Temperature{
			Sid:               0xff,
			Instance:          0,
			TemperatureSource: 2,
			ActualTemperature: ptr(293.15),
			SetTemperature:    ptr(0.0),
		},
		epsilon: 0.01,
	},

	// ---- PGN 127500: Load Controller Connection State/Control ----
	{
		desc: "Mastervolt connection 0, PWM 100%",
		pgn:  127500,
		hex:  "ff00000000640000",
		want: LoadControllerConnectionStateControl{
			Sid:                      0xff,
			ConnectionId:             0,
			State:                    0,
			Status:                   0,
			OperationalStatusControl: 0,
			PwmDutyCycle:             100,
			TimeOn:                   0,
			TimeOff:                  0,
		},
	},
	{
		desc: "Mastervolt connection 5, all zeros",
		pgn:  127500,
		hex:  "ff05000000000000",
		want: LoadControllerConnectionStateControl{
			Sid:          0xff,
			ConnectionId: 5,
		},
	},

	// ---- PGN 127751: DC Voltage/Current ----
	{
		desc: "Mastervolt connection 0, no readings",
		pgn:  127751,
		hex:  "ff000000000000ff",
		want: DCVoltageCurrent{
			Sid:              0xff,
			ConnectionNumber: 0,
			DcVoltage:        ptr(0.0),
			DcCurrent:        ptr(0.0),
		},
	},

	// ---- PGN 126208: NMEA Group Function ----
	{
		desc:        "Command: switch bank 127501, 2 pairs",
		pgn:         126208,
		hex:         "010df201f80201200a01",
		noRoundTrip: true, // variable-width PGN, no Encode method
		want: NMEAGroupFunctionCommand{
			FunctionCode:    1,
			CommandedPgn:    127501,
			PrioritySetting: 8, // 0x8 = don't change priority
			NumberOfPairs:   2,
			Params: []ParamPair{
				{Field: 1, Name: "instance", Value: 32},
				{Field: 10, Name: "indicator_9", Value: 1},
			},
		},
	},
	{
		desc:        "Acknowledge: reject 127501 (error 4)",
		pgn:         126208,
		hex:         "020df20144ff",
		noRoundTrip: true,
		want: NMEAGroupFunctionAcknowledge{
			FunctionCode:     2,
			AcknowledgedPgn:  127501,
			PgnErrorCode:     4,
			ControlErrorCode: 4,
			NumberOfPairs:    0xFF,
		},
	},
	{
		desc:        "Acknowledge: accept 127501 (error 0, raw params)",
		pgn:         126208,
		hex:         "020df201000200",
		noRoundTrip: true,
		want: NMEAGroupFunctionAcknowledge{
			FunctionCode:     2,
			AcknowledgedPgn:  127501,
			PgnErrorCode:     0,
			ControlErrorCode: 0,
			NumberOfPairs:    2,
			Params:           HexBytes{0x00},
		},
	},

}

func TestBytesParamPairJSON(t *testing.T) {
	// Verify the JSON output format for Command pairs matches the spec.
	data, _ := hex.DecodeString("010df201f80201200a01")
	result, err := Registry[126208].Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok := result.(NMEAGroupFunctionCommand)
	if !ok {
		t.Fatalf("expected NMEAGroupFunctionCommand, got %T", result)
	}
	if len(cmd.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(cmd.Params))
	}
	if cmd.Params[0].Field != 1 || cmd.Params[0].Name != "instance" || cmd.Params[0].Value != 32 {
		t.Errorf("param[0] = %+v, want {1 instance 32}", cmd.Params[0])
	}
	if cmd.Params[1].Field != 10 || cmd.Params[1].Name != "indicator_9" || cmd.Params[1].Value != 1 {
		t.Errorf("param[1] = %+v, want {10 indicator_9 1}", cmd.Params[1])
	}

	// Verify JSON matches expected format.
	j, _ := json.Marshal(cmd)
	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	params, ok := m["params"].([]any)
	if !ok {
		t.Fatalf("params is not an array: %T", m["params"])
	}
	pair := params[0].(map[string]any)
	if pair["field"].(float64) != 1 {
		t.Errorf("pair[0].field = %v, want 1", pair["field"])
	}
	if pair["name"].(string) != "instance" {
		t.Errorf("pair[0].name = %v, want instance", pair["name"])
	}
}

func TestBytesHexBytesJSON(t *testing.T) {
	// Verify HexBytes marshals as hex string.
	data, _ := hex.DecodeString("020df201000200")
	result, err := Registry[126208].Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := result.(NMEAGroupFunctionAcknowledge)
	if !ok {
		t.Fatalf("expected NMEAGroupFunctionAcknowledge, got %T", result)
	}
	if len(ack.Params) != 1 || ack.Params[0] != 0x00 {
		t.Errorf("Params = %x, want [00]", ack.Params)
	}

	// JSON should be a hex string.
	j, _ := json.Marshal(ack)
	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if m["params"].(string) != "00" {
		t.Errorf("params JSON = %q, want \"00\"", m["params"])
	}
}

func TestPacketDecode(t *testing.T) {
	for _, tc := range packetTests {
		t.Run(fmt.Sprintf("PGN%d/%s", tc.pgn, tc.desc), func(t *testing.T) {
			info, ok := Registry[tc.pgn]
			if !ok {
				t.Fatalf("PGN %d not in registry", tc.pgn)
			}
			if info.Decode == nil {
				t.Fatalf("PGN %d has no decoder", tc.pgn)
			}

			data, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatalf("bad hex %q: %v", tc.hex, err)
			}

			got, err := info.Decode(data)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}

			epsilon := tc.epsilon
			if epsilon == 0 {
				epsilon = 1e-6
			}

			compareStructs(t, got, tc.want, epsilon)
		})
	}
}

func TestPacketRoundTrip(t *testing.T) {
	for _, tc := range packetTests {
		if tc.noRoundTrip {
			continue
		}
		t.Run(fmt.Sprintf("PGN%d/%s", tc.pgn, tc.desc), func(t *testing.T) {
			data, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatalf("bad hex %q: %v", tc.hex, err)
			}

			info, ok := Registry[tc.pgn]
			if !ok {
				t.Fatalf("PGN %d not in registry", tc.pgn)
			}

			// Decode
			got, err := info.Decode(data)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}

			// Get pointer to call Encode (pointer receiver)
			reencoded := callEncode(t, got)
			if reencoded == nil {
				return
			}

			// Decode the re-encoded bytes and compare to original decode
			got2, err := info.Decode(reencoded)
			if err != nil {
				t.Fatalf("re-decode error: %v", err)
			}

			epsilon := tc.epsilon
			if epsilon == 0 {
				epsilon = 1e-6
			}
			compareStructs(t, got2, got, epsilon)

			// Check if hex is identical (best case: exact round-trip)
			reHex := hex.EncodeToString(reencoded)
			if len(reencoded) >= len(data) {
				trimmed := hex.EncodeToString(reencoded[:len(data)])
				if trimmed != tc.hex {
					t.Logf("hex differs: got %s, want %s (decoded values match)", reHex, tc.hex)
				}
			}
		})
	}
}

// callEncode calls the Encode() method on v via reflection, handling the
// pointer receiver case (Decode returns value types, Encode uses *T receiver).
func callEncode(t *testing.T, v any) []byte {
	t.Helper()
	rv := reflect.ValueOf(v)
	// Try value receiver first
	m := rv.MethodByName("Encode")
	if !m.IsValid() {
		// Try pointer receiver
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		m = ptr.MethodByName("Encode")
	}
	if !m.IsValid() {
		t.Skipf("type %T has no Encode method", v)
		return nil
	}
	results := m.Call(nil)
	return results[0].Bytes()
}

// compareStructs compares two decoded PGN structs via JSON with float tolerance.
func compareStructs(t *testing.T, got, want any, epsilon float64) {
	t.Helper()

	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)

	var gotMap, wantMap map[string]any
	if err := json.Unmarshal(gotJSON, &gotMap); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(wantJSON, &wantMap); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}

	for key, wv := range wantMap {
		gv, exists := gotMap[key]
		if !exists {
			t.Errorf("missing field %q in decoded output", key)
			continue
		}
		if wf, ok := wv.(float64); ok {
			gf, ok := gv.(float64)
			if !ok {
				t.Errorf("field %q: want float64, got %T", key, gv)
				continue
			}
			if math.Abs(wf-gf) > epsilon {
				t.Errorf("field %q = %v, want %v (epsilon=%v)", key, gf, wf, epsilon)
			}
		} else {
			// For non-float types (including slices/maps which aren't comparable
			// with !=), compare via JSON re-serialization.
			wj, _ := json.Marshal(wv)
			gj, _ := json.Marshal(gv)
			if string(wj) != string(gj) {
				t.Errorf("field %q = %s, want %s", key, gj, wj)
			}
		}
	}
}
