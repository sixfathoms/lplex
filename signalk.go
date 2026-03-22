package lplex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

// SignalKDelta is the top-level SignalK delta message format.
// See https://signalk.org/specification/1.7.0/doc/data_model.html
type SignalKDelta struct {
	Updates []SignalKUpdate `json:"updates"`
}

// SignalKUpdate is a single update within a delta message.
type SignalKUpdate struct {
	Source    SignalKSource  `json:"source"`
	Timestamp string        `json:"timestamp"`
	Values   []SignalKValue `json:"values"`
}

// SignalKSource identifies the data source in NMEA 2000 context.
type SignalKSource struct {
	Label string `json:"label"`
	Type  string `json:"type"`
	PGN   uint32 `json:"pgn"`
	Src   string `json:"src"`
}

// SignalKValue is a path-value pair in the SignalK data model.
type SignalKValue struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// pgnMapping defines how a decoded PGN struct maps to SignalK paths.
type pgnMapping struct {
	convert func(decoded any) []SignalKValue
}

// signalKMappings maps PGN numbers to their SignalK conversion functions.
var signalKMappings = map[uint32]pgnMapping{
	129025: {convert: convertPositionRapidUpdate},
	129026: {convert: convertCOGSOGRapidUpdate},
	127250: {convert: convertVesselHeading},
	128259: {convert: convertSpeed},
	128267: {convert: convertWaterDepth},
	130306: {convert: convertWindData},
	127488: {convert: convertEngineParametersRapidUpdate},
	127508: {convert: convertBatteryStatus},
	130310: {convert: convertEnvironmentalParameters},
	130312: {convert: convertTemperature},
}

// ConvertToSignalK converts a decoded PGN value to a SignalK delta message.
// Returns nil if the PGN has no SignalK mapping.
func ConvertToSignalK(pgnNum uint32, src uint8, ts time.Time, data []byte) *SignalKDelta {
	mapping, ok := signalKMappings[pgnNum]
	if !ok {
		return nil
	}

	info, ok := pgn.Registry[pgnNum]
	if !ok || info.Decode == nil {
		return nil
	}

	decoded, err := info.Decode(data)
	if err != nil {
		return nil
	}

	values := mapping.convert(decoded)
	if len(values) == 0 {
		return nil
	}

	return &SignalKDelta{
		Updates: []SignalKUpdate{{
			Source: SignalKSource{
				Label: "lplex",
				Type:  "NMEA2000",
				PGN:   pgnNum,
				Src:   strconv.Itoa(int(src)),
			},
			Timestamp: ts.UTC().Format(time.RFC3339Nano),
			Values:    values,
		}},
	}
}

// ConvertToSignalKJSON is a convenience that returns the JSON bytes.
func ConvertToSignalKJSON(pgnNum uint32, src uint8, ts time.Time, data []byte) ([]byte, error) {
	delta := ConvertToSignalK(pgnNum, src, ts, data)
	if delta == nil {
		return nil, nil
	}
	return json.Marshal(delta)
}

// HasSignalKMapping reports whether a PGN has a SignalK conversion.
func HasSignalKMapping(pgnNum uint32) bool {
	_, ok := signalKMappings[pgnNum]
	return ok
}

// --- PGN conversion functions ---

func convertPositionRapidUpdate(decoded any) []SignalKValue {
	type pos struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	b, _ := json.Marshal(decoded)
	var p pos
	if json.Unmarshal(b, &p) != nil || p.Latitude == nil || p.Longitude == nil {
		return nil
	}
	return []SignalKValue{{
		Path: "navigation.position",
		Value: map[string]float64{
			"latitude":  *p.Latitude,
			"longitude": *p.Longitude,
		},
	}}
}

func convertCOGSOGRapidUpdate(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "cog"); ok {
		values = append(values, SignalKValue{Path: "navigation.courseOverGroundTrue", Value: degToRad(v)})
	}
	if v, ok := getFloat(m, "sog"); ok {
		values = append(values, SignalKValue{Path: "navigation.speedOverGround", Value: v})
	}
	return values
}

func convertVesselHeading(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "heading"); ok {
		values = append(values, SignalKValue{Path: "navigation.headingTrue", Value: degToRad(v)})
	}
	return values
}

func convertSpeed(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "speed_water_referenced"); ok {
		values = append(values, SignalKValue{Path: "navigation.speedThroughWater", Value: v})
	}
	return values
}

func convertWaterDepth(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "depth"); ok {
		values = append(values, SignalKValue{Path: "environment.depth.belowTransducer", Value: v})
	}
	return values
}

func convertWindData(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "wind_speed"); ok {
		values = append(values, SignalKValue{Path: "environment.wind.speedApparent", Value: v})
	}
	if v, ok := getFloat(m, "wind_angle"); ok {
		values = append(values, SignalKValue{Path: "environment.wind.angleApparent", Value: degToRad(v)})
	}
	return values
}

func convertEngineParametersRapidUpdate(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	instance := "0"
	if v, ok := m["engine_instance"]; ok {
		instance = fmt.Sprintf("%v", v)
	}
	if v, ok := getFloat(m, "engine_speed"); ok {
		values = append(values, SignalKValue{
			Path:  fmt.Sprintf("propulsion.%s.revolutions", instance),
			Value: v / 60.0, // RPM to Hz
		})
	}
	return values
}

func convertBatteryStatus(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	instance := "0"
	if v, ok := m["battery_instance"]; ok {
		instance = fmt.Sprintf("%v", v)
	}
	if v, ok := getFloat(m, "voltage"); ok {
		values = append(values, SignalKValue{
			Path:  fmt.Sprintf("electrical.batteries.%s.voltage", instance),
			Value: v,
		})
	}
	if v, ok := getFloat(m, "current"); ok {
		values = append(values, SignalKValue{
			Path:  fmt.Sprintf("electrical.batteries.%s.current", instance),
			Value: v,
		})
	}
	return values
}

func convertEnvironmentalParameters(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "water_temperature"); ok {
		values = append(values, SignalKValue{Path: "environment.water.temperature", Value: v})
	}
	if v, ok := getFloat(m, "atmospheric_pressure"); ok {
		values = append(values, SignalKValue{Path: "environment.outside.pressure", Value: v})
	}
	return values
}

func convertTemperature(decoded any) []SignalKValue {
	m := structToMap(decoded)
	var values []SignalKValue
	if v, ok := getFloat(m, "actual_temperature"); ok {
		values = append(values, SignalKValue{Path: "environment.inside.temperature", Value: v})
	}
	return values
}

// --- helpers ---

func structToMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case json.Number:
		n, err := f.Float64()
		return n, err == nil
	}
	return 0, false
}

func degToRad(deg float64) float64 {
	return deg * 3.14159265358979323846 / 180.0
}
