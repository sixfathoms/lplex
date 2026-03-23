package main

import (
	"strings"
	"testing"

	"github.com/sixfathoms/lplex/lplexc"
)

func TestExtractValue(t *testing.T) {
	values := []lplexc.DecodedDeviceValues{
		{
			Source: 1,
			Values: []lplexc.DecodedPGNValue{
				{
					PGN:         129025,
					Description: "Position Rapid Update",
					Fields: map[string]any{
						"latitude":  47.6062,
						"longitude": -122.3321,
					},
				},
				{
					PGN:         129026,
					Description: "COG/SOG Rapid Update",
					Fields: map[string]any{
						"sog": 5.2,
						"cog": 180.0,
					},
				},
			},
		},
	}

	t.Run("GPS position", func(t *testing.T) {
		result := extractValue(values, 129025, "latitude", "longitude")
		if result == "" {
			t.Fatal("expected GPS data")
		}
		if !strings.Contains(result, "latitude") || !strings.Contains(result, "longitude") {
			t.Fatalf("expected latitude and longitude, got: %s", result)
		}
	})

	t.Run("SOG/COG", func(t *testing.T) {
		result := extractValue(values, 129026, "sog", "cog")
		if result == "" {
			t.Fatal("expected SOG/COG data")
		}
	})

	t.Run("missing PGN", func(t *testing.T) {
		result := extractValue(values, 128267, "depth")
		if result != "" {
			t.Fatalf("expected empty for missing PGN, got: %s", result)
		}
	})

	t.Run("empty values", func(t *testing.T) {
		result := extractValue(nil, 129025, "latitude")
		if result != "" {
			t.Fatalf("expected empty for nil values, got: %s", result)
		}
	})
}

func TestDashModelViewOverview(t *testing.T) {
	m := dashModel{
		width:     80,
		height:    40,
		activeTab: tabOverview,
		devices: []lplexc.Device{
			{Src: 1, Manufacturer: "Garmin", ModelID: "GNX 120", PacketCount: 12345},
			{Src: 2, Manufacturer: "Victron", ModelID: "BMV-712", PacketCount: 6789},
		},
		values: []lplexc.DecodedDeviceValues{
			{
				Source: 1,
				Values: []lplexc.DecodedPGNValue{
					{PGN: 129025, Description: "Position Rapid Update", Fields: map[string]any{"latitude": 47.6, "longitude": -122.3}},
					{PGN: 129026, Description: "COG/SOG Rapid Update", Fields: map[string]any{"sog": 5.2, "cog": 180.0}},
				},
			},
		},
	}

	output := m.View()

	checks := []string{"Live Values", "PGN Activity", "Position Rapid Update", "latitude", "Overview", "Devices"}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected %q in overview tab output", check)
		}
	}
}

func TestDashModelViewDevicesTab(t *testing.T) {
	m := dashModel{
		width:     100,
		height:    40,
		activeTab: tabDevices,
		devices: []lplexc.Device{
			{Src: 1, Manufacturer: "Garmin", ModelID: "GNX 120", ModelSerial: "SN12345", PacketCount: 12345, ByteCount: 98765},
			{Src: 2, Manufacturer: "Victron", ModelID: "BMV-712", ModelSerial: "V001", PacketCount: 6789, ByteCount: 54321},
		},
	}

	output := m.View()

	checks := []string{"Devices", "Garmin", "GNX 120", "SN12345", "12345", "Victron", "BMV-712", "Manufacturer", "Packets", "Bytes"}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected %q in devices tab output", check)
		}
	}
}

func TestDashModelViewEmpty(t *testing.T) {
	m := dashModel{width: 80, height: 40, activeTab: tabOverview}
	output := m.View()
	if !strings.Contains(output, "no decoded values") {
		t.Error("expected 'no decoded values' message on empty overview")
	}

	m.activeTab = tabDevices
	output = m.View()
	if !strings.Contains(output, "no devices") {
		t.Error("expected 'no devices' message on empty devices tab")
	}
}

func TestDashTabSwitching(t *testing.T) {
	m := dashModel{width: 80, height: 40, activeTab: tabOverview}

	// Tab key cycles forward.
	if m.activeTab != tabOverview {
		t.Fatal("should start on overview")
	}
	m.activeTab = (m.activeTab + 1) % tabCount
	if m.activeTab != tabDevices {
		t.Fatal("tab should switch to devices")
	}
	m.activeTab = (m.activeTab + 1) % tabCount
	if m.activeTab != tabOverview {
		t.Fatal("tab should wrap to overview")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("short string should not be truncated")
	}
	result := truncate("a very long string", 10)
	if len(result) > 10 {
		t.Fatalf("truncated string too long: %d chars, got '%s'", len(result), result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Fatalf("expected '...' suffix, got '%s'", result)
	}
}
