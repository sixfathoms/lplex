package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gurkankaymak/hocon"
	"github.com/sixfathoms/lplex"
)

// configToFlag maps HOCON config paths to CLI flag names.
var configToFlag = map[string]string{
	"interface":               "interface",
	"interfaces":              "interfaces",
	"port":                    "port",
	"max-buffer-duration":     "max-buffer-duration",
	"journal.dir":             "journal-dir",
	"journal.prefix":          "journal-prefix",
	"journal.block-size":      "journal-block-size",
	"journal.compression":     "journal-compression",
	"journal.rotate.duration": "journal-rotate-duration",
	"journal.rotate.size":     "journal-rotate-size",
	"journal.retention.max-age":         "journal-retention-max-age",
	"journal.retention.min-keep":        "journal-retention-min-keep",
	"journal.retention.max-size":        "journal-retention-max-size",
	"journal.retention.soft-pct":        "journal-retention-soft-pct",
	"journal.retention.overflow-policy": "journal-retention-overflow-policy",
	"journal.archive.command":           "journal-archive-command",
	"journal.archive.trigger":           "journal-archive-trigger",
	"ring-size":                      "ring-size",
	"loopback":                       "loopback",
	"device.idle-timeout":            "device-idle-timeout",
	"send.enabled":                   "send-enabled",
	"virtual-device.enabled":                "virtual-device",
	"virtual-device.name":                   "virtual-device-name",
	"virtual-device.model-id":               "virtual-device-model-id",
	"virtual-device.claim-heartbeat":         "virtual-device-claim-heartbeat",
	"virtual-device.product-info-heartbeat":  "virtual-device-product-info-heartbeat",
	"bus-silence-timeout":        "bus-silence-timeout",
	"replication.target":         "replication-target",
	"replication.instance-id":    "replication-instance-id",
	"replication.tls.cert":       "replication-tls-cert",
	"replication.tls.key":        "replication-tls-key",
	"replication.tls.ca":                    "replication-tls-ca",
	"replication.max-live-lag":               "replication-max-live-lag",
	"replication.lag-check-interval":         "replication-lag-check-interval",
	"replication.min-lag-reconnect-interval": "replication-min-lag-reconnect-interval",
	"read-only":                             "read-only",
	"api-key":                               "api-key",
	"send.rate-limit":                       "send-rate-limit",
	"send.rate-burst":                       "send-rate-burst",
	"health.bus-silence-threshold":           "bus-silence-threshold",
}

// findConfigFile resolves which config file to use.
// If configFlag is non-empty, that exact path is required (error if missing).
// Otherwise, searches ./lplex-server.conf then /etc/lplex/lplex-server.conf,
// falling back to ./lplex.conf then /etc/lplex/lplex.conf for backward compat.
// Returns "" with no error if no config file is found.
func findConfigFile(configFlag string) (string, error) {
	if configFlag != "" {
		info, err := os.Stat(configFlag)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("config file not found: %s", configFlag)
			}

			return "", fmt.Errorf("checking config file %s: %w", configFlag, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("config path is a directory: %s", configFlag)
		}

		return configFlag, nil
	}

	for _, path := range []string{
		"./lplex-server.conf", "/etc/lplex/lplex-server.conf",
		"./lplex.conf", "/etc/lplex/lplex.conf",
	} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", nil
}

// configResult holds values parsed from HOCON that can't be represented as
// simple flag strings (arrays of structured objects, etc.).
type configResult struct {
	Slots []lplex.ClientSlot
}

// applyConfig parses a HOCON config file and sets any flag values that
// weren't explicitly provided on the command line. CLI flags always win.
// Returns structured config values that can't be mapped to flags.
func applyConfig(path string) (*configResult, error) {
	cfg, err := hocon.ParseResource(path)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Collect flags the user explicitly set on the command line.
	explicit := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})

	for configKey, flagName := range configToFlag {
		if explicit[flagName] {
			continue
		}
		val := cfg.GetString(configKey)
		if val == "" {
			continue
		}
		if err := flag.Set(flagName, val); err != nil {
			return nil, fmt.Errorf("config key %q (flag -%s): %w", configKey, flagName, err)
		}
	}

	// Handle send.rules: supports both string elements (DSL syntax) and
	// object elements ({ deny, pgn, name }) in the same array.
	if !explicit["send-rules"] {
		if arr := cfg.GetArray("send.rules"); len(arr) > 0 {
			parts := make([]string, 0, len(arr))
			for i, elem := range arr {
				switch elem.Type() {
				case hocon.StringType:
					parts = append(parts, string(elem.(hocon.String)))
				case hocon.ObjectType:
					dsl, err := hoconRuleToDSL(elem.(hocon.Object))
					if err != nil {
						return nil, fmt.Errorf("config key send.rules[%d]: %w", i, err)
					}
					parts = append(parts, dsl)
				default:
					return nil, fmt.Errorf("config key send.rules[%d]: expected string or object, got %v", i, elem.Type())
				}
			}
			if err := flag.Set("send-rules", strings.Join(parts, ";")); err != nil {
				return nil, fmt.Errorf("config key send.rules: %w", err)
			}
		}
	}

	result := &configResult{}

	// Parse clients.slots array.
	if arr := cfg.GetArray("clients.slots"); len(arr) > 0 {
		for i, elem := range arr {
			if elem.Type() != hocon.ObjectType {
				return nil, fmt.Errorf("config key clients.slots[%d]: expected object, got %v", i, elem.Type())
			}
			slot, err := parseHOCNSlot(elem.(hocon.Object), i)
			if err != nil {
				return nil, err
			}
			result.Slots = append(result.Slots, slot)
		}
	}

	return result, nil
}

// parseHOCNSlot converts a HOCON object into a ClientSlot.
func parseHOCNSlot(obj hocon.Object, index int) (lplex.ClientSlot, error) {
	prefix := fmt.Sprintf("clients.slots[%d]", index)

	cfg := lplex.ClientSlotConfig{}

	if v, ok := obj["id"]; ok {
		cfg.ID = string(v.(hocon.String))
	} else {
		return lplex.ClientSlot{}, fmt.Errorf("%s: id is required", prefix)
	}

	if v, ok := obj["buffer-timeout"]; ok {
		cfg.BufferTimeout = string(v.(hocon.String))
	}

	if v, ok := obj["filter"]; ok {
		if v.Type() != hocon.ObjectType {
			return lplex.ClientSlot{}, fmt.Errorf("%s.filter: expected object", prefix)
		}
		filterObj := v.(hocon.Object)
		fc := &lplex.SlotFilterConfig{}

		if pgn, ok := filterObj["pgn"]; ok {
			pgns, err := hoconUint32Array(pgn, prefix+".filter.pgn")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.PGN = pgns
		}
		if pgn, ok := filterObj["exclude-pgn"]; ok {
			pgns, err := hoconUint32Array(pgn, prefix+".filter.exclude-pgn")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.ExcludePGN = pgns
		}
		if v, ok := filterObj["manufacturer"]; ok {
			strs, err := hoconStringArray(v, prefix+".filter.manufacturer")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.Manufacturer = strs
		}
		if v, ok := filterObj["instance"]; ok {
			vals, err := hoconUint32Array(v, prefix+".filter.instance")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			for _, val := range vals {
				fc.Instance = append(fc.Instance, uint8(val))
			}
		}
		if v, ok := filterObj["name"]; ok {
			strs, err := hoconStringArray(v, prefix+".filter.name")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.Name = strs
		}
		if v, ok := filterObj["exclude-name"]; ok {
			strs, err := hoconStringArray(v, prefix+".filter.exclude-name")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.ExcludeName = strs
		}
		if v, ok := filterObj["bus"]; ok {
			strs, err := hoconStringArray(v, prefix+".filter.bus")
			if err != nil {
				return lplex.ClientSlot{}, err
			}
			fc.Bus = strs
		}

		cfg.Filter = fc
	}

	return lplex.ParseClientSlot(cfg)
}

// hoconStringArray extracts a string array from a HOCON value (string or array of strings).
func hoconStringArray(v hocon.Value, path string) ([]string, error) {
	switch v.Type() {
	case hocon.StringType:
		return []string{string(v.(hocon.String))}, nil
	case hocon.ArrayType:
		arr := v.(hocon.Array)
		result := make([]string, len(arr))
		for i, elem := range arr {
			if elem.Type() != hocon.StringType {
				return nil, fmt.Errorf("%s[%d]: expected string", path, i)
			}
			result[i] = string(elem.(hocon.String))
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s: expected string or array", path)
	}
}

// hoconUint32Array extracts a uint32 array from a HOCON value (number/string or array).
func hoconUint32Array(v hocon.Value, path string) ([]uint32, error) {
	parseOne := func(elem hocon.Value, elemPath string) (uint32, error) {
		// Use String() which works for Int, Float, and String types.
		n, err := strconv.ParseUint(elem.String(), 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", elemPath, err)
		}
		return uint32(n), nil
	}

	if v.Type() == hocon.ArrayType {
		arr := v.(hocon.Array)
		result := make([]uint32, len(arr))
		for i, elem := range arr {
			var err error
			result[i], err = parseOne(elem, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	}

	n, err := parseOne(v, path)
	if err != nil {
		return nil, err
	}
	return []uint32{n}, nil
}

// hoconRuleToDSL converts a HOCON object rule to a DSL string.
// Supported fields: deny (bool), pgn (string), name (string or string array).
func hoconRuleToDSL(obj hocon.Object) (string, error) {
	var parts []string

	if v, ok := obj["deny"]; ok {
		if bool(v.(hocon.Boolean)) {
			parts = append(parts, "!")
		}
	}

	if v, ok := obj["pgn"]; ok {
		parts = append(parts, "pgn:"+string(v.(hocon.String)))
	}

	if v, ok := obj["name"]; ok {
		switch v.Type() {
		case hocon.StringType:
			parts = append(parts, "name:"+string(v.(hocon.String)))
		case hocon.ArrayType:
			arr := v.(hocon.Array)
			names := make([]string, len(arr))
			for i, n := range arr {
				names[i] = string(n.(hocon.String))
			}
			parts = append(parts, "name:"+strings.Join(names, ","))
		default:
			return "", fmt.Errorf("name field must be a string or array")
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("rule object must have at least one of: deny, pgn, name")
	}

	return strings.Join(parts, " "), nil
}
