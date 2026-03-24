package lplex

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sixfathoms/lplex/pgn"
)

// InfluxDBSinkConfig configures the InfluxDB time-series writer.
type InfluxDBSinkConfig struct {
	// URL is the InfluxDB server address (e.g. "http://localhost:8086").
	URL string

	// Token is the InfluxDB authentication token (v2 API).
	Token string

	// Org is the InfluxDB organization name.
	Org string

	// Bucket is the InfluxDB bucket name.
	Bucket string

	// Measurement is the InfluxDB measurement name (default "nmea2k").
	Measurement string

	// FlushInterval is how often buffered points are flushed (default 10s).
	FlushInterval time.Duration

	// FlushSize is how many points trigger an immediate flush (default 1000).
	FlushSize int

	// Filter restricts which CAN frames are written.
	Filter *EventFilter

	// Logger for diagnostic output.
	Logger *slog.Logger
}

// InfluxDBSink subscribes to the broker's frame stream, decodes PGN data,
// and writes decoded field values to InfluxDB as time-series points using
// the v2 write API with line protocol.
type InfluxDBSink struct {
	cfg    InfluxDBSinkConfig
	broker *Broker
	client *http.Client
	logger *slog.Logger
	buf    []string
}

// NewInfluxDBSink creates a new InfluxDB sink. Call Run to start writing.
func NewInfluxDBSink(cfg InfluxDBSinkConfig, broker *Broker) *InfluxDBSink {
	if cfg.Measurement == "" {
		cfg.Measurement = "nmea2k"
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 10 * time.Second
	}
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &InfluxDBSink{
		cfg:    cfg,
		broker: broker,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: cfg.Logger.With("component", "influxdb"),
	}
}

// Run subscribes to the broker's frame stream and writes decoded PGN values
// to InfluxDB until ctx is cancelled.
func (s *InfluxDBSink) Run(ctx context.Context) error {
	s.logger.Info("InfluxDB sink started",
		"url", s.cfg.URL,
		"org", s.cfg.Org,
		"bucket", s.cfg.Bucket,
		"measurement", s.cfg.Measurement,
		"flush_interval", s.cfg.FlushInterval,
		"flush_size", s.cfg.FlushSize,
	)

	sub, cleanup := s.broker.Subscribe(s.cfg.Filter)
	defer cleanup()

	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush remaining points before exit.
			if len(s.buf) > 0 {
				s.flush(context.Background())
			}
			return ctx.Err()
		case data, ok := <-sub.ch:
			if !ok {
				return nil
			}
			s.handleFrame(data)
			if len(s.buf) >= s.cfg.FlushSize {
				s.flush(ctx)
			}
		case <-ticker.C:
			if len(s.buf) > 0 {
				s.flush(ctx)
			}
		}
	}
}

// handleFrame decodes a pre-serialized JSON frame and converts its decoded
// PGN fields into InfluxDB line protocol points.
func (s *InfluxDBSink) handleFrame(data []byte) {
	var frame struct {
		Ts  string `json:"ts"`
		Bus string `json:"bus"`
		PGN uint32 `json:"pgn"`
		Src uint8  `json:"src"`
		Dst uint8  `json:"dst"`
		Dat string `json:"data"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}

	info, ok := pgn.Registry[frame.PGN]
	if !ok || info.Decode == nil {
		return
	}

	raw, err := hex.DecodeString(frame.Dat)
	if err != nil {
		return
	}

	decoded, err := info.Decode(raw)
	if err != nil {
		return
	}

	ts, err := time.Parse(time.RFC3339Nano, frame.Ts)
	if err != nil {
		return
	}

	// Build tags.
	tags := fmt.Sprintf("pgn=%d,src=%d,bus=%s", frame.PGN, frame.Src, escapeTag(frame.Bus))
	if frame.Dst != 255 {
		tags += fmt.Sprintf(",dst=%d", frame.Dst)
	}

	// Extract fields from the decoded struct via reflection.
	fields := extractFields(decoded)
	if len(fields) == 0 {
		return
	}

	// Format: measurement,tag=val,... field=val,... timestamp
	line := fmt.Sprintf("%s,%s %s %d",
		escapeTag(s.cfg.Measurement), tags, fields, ts.UnixNano())
	s.buf = append(s.buf, line)
}

// flush sends buffered line protocol points to InfluxDB.
func (s *InfluxDBSink) flush(ctx context.Context) {
	if len(s.buf) == 0 {
		return
	}

	body := strings.Join(s.buf, "\n")
	count := len(s.buf)
	s.buf = s.buf[:0]

	url := fmt.Sprintf("%s/api/v2/write?org=%s&bucket=%s&precision=ns",
		strings.TrimRight(s.cfg.URL, "/"), s.cfg.Org, s.cfg.Bucket)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		s.logger.Warn("InfluxDB request build failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if s.cfg.Token != "" {
		req.Header.Set("Authorization", "Token "+s.cfg.Token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Warn("InfluxDB write failed", "error", err, "points", count)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		s.logger.Warn("InfluxDB write error",
			"status", resp.StatusCode,
			"body", string(respBody),
			"points", count,
		)
		return
	}

	s.logger.Debug("InfluxDB flush", "points", count)
}

// extractFields converts a decoded PGN struct into InfluxDB line protocol
// field-set string. Uses reflection to iterate struct fields, using the json
// tag as the field name. Numeric types become InfluxDB floats/ints, strings
// become InfluxDB strings, booleans become InfluxDB booleans.
func extractFields(decoded any) string {
	v := reflect.ValueOf(decoded)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}

	t := v.Type()
	var parts []string

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		name := field.Tag.Get("json")
		if name == "" || name == "-" {
			continue
		}
		// Strip json options like ",omitempty"
		if idx := strings.IndexByte(name, ','); idx >= 0 {
			name = name[:idx]
		}
		if name == "" {
			continue
		}

		fv := v.Field(i)
		formatted, ok := formatFieldValue(fv)
		if !ok {
			continue
		}

		parts = append(parts, escapeFieldKey(name)+"="+formatted)
	}

	// Sort for deterministic output (useful for testing).
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// formatFieldValue converts a reflect.Value to an InfluxDB line protocol
// field value string. Returns the formatted string and true if the value
// should be included, or ("", false) if it should be skipped.
func formatFieldValue(v reflect.Value) (string, bool) {
	// Handle pointer types.
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}

	// Check for Stringer interface (for enums like TemperatureSource).
	if v.CanInterface() {
		if stringer, ok := v.Interface().(fmt.Stringer); ok {
			// Only use Stringer for non-numeric named types (enums).
			if v.Kind() >= reflect.Int && v.Kind() <= reflect.Uint64 {
				s := stringer.String()
				// If the Stringer returns a formatted fallback like "Type(42)", skip it.
				if !strings.Contains(s, "(") {
					return fmt.Sprintf("%q", s), true
				}
			}
		}
	}

	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", false
		}
		return fmt.Sprintf("%g", f), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%di", v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%di", v.Uint()), true
	case reflect.Bool:
		if v.Bool() {
			return "true", true
		}
		return "false", true
	case reflect.String:
		s := v.String()
		if s == "" {
			return "", false
		}
		return fmt.Sprintf("%q", s), true
	default:
		return "", false
	}
}

// escapeTag escapes a tag key or value for InfluxDB line protocol.
func escapeTag(s string) string {
	s = strings.ReplaceAll(s, " ", "\\ ")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "=", "\\=")
	return s
}

// escapeFieldKey escapes a field key for InfluxDB line protocol.
func escapeFieldKey(s string) string {
	return escapeTag(s)
}
