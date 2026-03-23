package main

import (
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
		if !contains(result, "latitude") || !contains(result, "longitude") {
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

func TestDashModelView(t *testing.T) {
	// Verify the model renders without panicking.
	m := dashModel{
		width:  80,
		height: 40,
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

	// Check key sections are present.
	checks := []string{"Devices", "Live Values", "PGN Activity", "Garmin", "GNX 120", "Position Rapid Update", "latitude"}
	for _, check := range checks {
		if !contains(output, check) {
			t.Errorf("expected %q in output", check)
		}
	}
}

func TestDashModelViewEmpty(t *testing.T) {
	// Verify empty state renders without panicking.
	m := dashModel{width: 80, height: 40}
	output := m.View()
	if !contains(output, "no devices") {
		t.Error("expected 'no devices' message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
