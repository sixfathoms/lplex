package pgn

import (
	"encoding/hex"
	"testing"
)

func BenchmarkDecode(b *testing.B) {
	b.Run("VesselHeading", func(b *testing.B) {
		data, _ := hex.DecodeString("ff107b0000000000")
		b.ReportAllocs()
		for b.Loop() {
			DecodeVesselHeading(data) //nolint:errcheck
		}
	})

	b.Run("WindData", func(b *testing.B) {
		data, _ := hex.DecodeString("ff640500d21e0200")
		b.ReportAllocs()
		for b.Loop() {
			DecodeWindData(data) //nolint:errcheck
		}
	})

	b.Run("Temperature", func(b *testing.B) {
		data, _ := hex.DecodeString("ff0100a47300ffff")
		b.ReportAllocs()
		for b.Loop() {
			DecodeTemperature(data) //nolint:errcheck
		}
	})

	b.Run("EngineParametersRapidUpdate", func(b *testing.B) {
		data, _ := hex.DecodeString("00e80300000000ff")
		b.ReportAllocs()
		for b.Loop() {
			DecodeEngineParametersRapidUpdate(data) //nolint:errcheck
		}
	})

	b.Run("BatteryStatus", func(b *testing.B) {
		data, _ := hex.DecodeString("019c04e8ffffff7f")
		b.ReportAllocs()
		for b.Loop() {
			DecodeBatteryStatus(data) //nolint:errcheck
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	b.Run("VesselHeading", func(b *testing.B) {
		data, _ := hex.DecodeString("ff107b0000000000")
		m, _ := DecodeVesselHeading(data)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			m.Encode()
		}
	})

	b.Run("WindData", func(b *testing.B) {
		data, _ := hex.DecodeString("ff640500d21e0200")
		m, _ := DecodeWindData(data)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			m.Encode()
		}
	})

	b.Run("EngineParametersRapidUpdate", func(b *testing.B) {
		data, _ := hex.DecodeString("00e80300000000ff")
		m, _ := DecodeEngineParametersRapidUpdate(data)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			m.Encode()
		}
	})
}

func BenchmarkDecodeEncode(b *testing.B) {
	b.Run("VesselHeading", func(b *testing.B) {
		data, _ := hex.DecodeString("ff107b0000000000")
		b.ReportAllocs()
		for b.Loop() {
			m, _ := DecodeVesselHeading(data)
			m.Encode()
		}
	})

	b.Run("EngineParametersRapidUpdate", func(b *testing.B) {
		data, _ := hex.DecodeString("00e80300000000ff")
		b.ReportAllocs()
		for b.Loop() {
			m, _ := DecodeEngineParametersRapidUpdate(data)
			m.Encode()
		}
	})
}

func BenchmarkRegistryLookup(b *testing.B) {
	b.Run("known_pgn", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Registry[127250]
		}
	})

	b.Run("unknown_pgn", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Registry[99999]
		}
	})

	b.Run("known_with_decode", func(b *testing.B) {
		data, _ := hex.DecodeString("ff107b0000000000")
		b.ReportAllocs()
		for b.Loop() {
			if info, ok := Registry[127250]; ok && info.Decode != nil {
				info.Decode(data) //nolint:errcheck
			}
		}
	})
}
