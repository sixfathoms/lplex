package main

import "testing"

func TestValidateConfigDefaults(t *testing.T) {
	code := runValidateConfig("",
		"PT5M", "5m", 262144, 65536,
		"PT1H", "zstd",
		"", "", 0, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		false, "",
		"30s",
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestValidateConfigInvalidDuration(t *testing.T) {
	code := runValidateConfig("",
		"INVALID", "5m", 262144, 65536,
		"PT1H", "zstd",
		"", "", 0, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		false, "",
		"30s",
	)
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid duration, got %d", code)
	}
}

func TestValidateConfigInvalidRingSize(t *testing.T) {
	code := runValidateConfig("",
		"PT5M", "5m", 262144, 100,
		"PT1H", "zstd",
		"", "", 0, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		false, "",
		"30s",
	)
	if code != 1 {
		t.Fatalf("expected exit 1 for non-power-of-2 ring size, got %d", code)
	}
}

func TestValidateConfigInvalidCompression(t *testing.T) {
	code := runValidateConfig("",
		"PT5M", "5m", 262144, 65536,
		"PT1H", "lz4",
		"", "", 0, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		false, "",
		"30s",
	)
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid compression, got %d", code)
	}
}

func TestValidateConfigVirtualDeviceMissingName(t *testing.T) {
	code := runValidateConfig("",
		"PT5M", "5m", 262144, 65536,
		"PT1H", "zstd",
		"", "", 0, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		true, "",
		"30s",
	)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing virtual device name, got %d", code)
	}
}

func TestValidateConfigWarnings(t *testing.T) {
	// max-size without max-age should warn but pass.
	code := runValidateConfig("",
		"PT5M", "5m", 262144, 65536,
		"PT1H", "zstd",
		"", "", 1073741824, 80, "delete-unarchived",
		"", "",
		"", "",
		"5m",
		"60s", "5m",
		false, "",
		false, "",
		"30s",
	)
	if code != 0 {
		t.Fatalf("warnings should not cause failure, got exit %d", code)
	}
}
