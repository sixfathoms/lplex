package lplex

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// benchFrame returns a typical 8-byte CAN frame for benchmarking.
func benchFrame(seq uint64) RxFrame {
	return RxFrame{
		Timestamp: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Millisecond),
		Header: CANHeader{
			Priority:    2,
			PGN:         127250,
			Source:      35,
			Destination: 0xFF,
		},
		Data: []byte{0xFF, 0x10, 0x7B, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
}

func BenchmarkFrameJSONSerialization(b *testing.B) {
	frame := benchFrame(1)
	msg := frameJSON{
		Seq:  1,
		Ts:   frame.Timestamp.UTC().Format(time.RFC3339Nano),
		Prio: frame.Header.Priority,
		PGN:  frame.Header.PGN,
		Src:  frame.Header.Source,
		Dst:  frame.Header.Destination,
		Data: hex.EncodeToString(frame.Data),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		json.Marshal(msg) //nolint:errcheck
	}
}

func BenchmarkFrameJSONSerializationFull(b *testing.B) {
	frame := benchFrame(1)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		msg := frameJSON{
			Seq:  1,
			Ts:   frame.Timestamp.UTC().Format(time.RFC3339Nano),
			Prio: frame.Header.Priority,
			PGN:  frame.Header.PGN,
			Src:  frame.Header.Source,
			Dst:  frame.Header.Destination,
			Data: hex.EncodeToString(frame.Data),
		}
		json.Marshal(msg) //nolint:errcheck
	}
}

func BenchmarkHexEncodeData(b *testing.B) {
	data := []byte{0xFF, 0x10, 0x7B, 0x00, 0x00, 0x00, 0x00, 0x00}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		hex.EncodeToString(data)
	}
}

func BenchmarkTimeFormat(b *testing.B) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 123456789, time.UTC)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ts.UTC().Format(time.RFC3339Nano)
	}
}

func BenchmarkRingBufferWrite(b *testing.B) {
	broker := NewBroker(BrokerConfig{
		RingSize: 65536,
	})

	frame := benchFrame(1)
	jsonBytes := []byte(`{"seq":1,"ts":"2025-06-15T12:00:00Z","prio":2,"pgn":127250,"src":35,"dst":255,"data":"ff107b0000000000"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		idx := broker.head & uint64(broker.ringMask)
		broker.mu.Lock()
		broker.ring[idx] = ringEntry{
			Seq:       broker.head,
			Timestamp: frame.Timestamp,
			Header:    frame.Header,
			RawData:   frame.Data,
			JSON:      jsonBytes,
		}
		broker.head++
		if broker.head-broker.tail > uint64(broker.ringMask+1) {
			broker.tail = broker.head - uint64(broker.ringMask+1)
		}
		broker.mu.Unlock()
	}
}

func BenchmarkEventFilterMatches(b *testing.B) {
	devices := NewDeviceRegistry()

	header := CANHeader{
		Priority:    2,
		PGN:         127250,
		Source:      35,
		Destination: 0xFF,
	}

	b.Run("nil_filter", func(b *testing.B) {
		var filter *EventFilter
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})

	b.Run("single_pgn_match", func(b *testing.B) {
		filter := &EventFilter{PGNs: []uint32{127250}}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})

	b.Run("single_pgn_miss", func(b *testing.B) {
		filter := &EventFilter{PGNs: []uint32{129025}}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})

	b.Run("multiple_pgns", func(b *testing.B) {
		filter := &EventFilter{PGNs: []uint32{127250, 129025, 129026, 130306, 130310}}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})

	b.Run("exclude_pgn_miss", func(b *testing.B) {
		filter := &EventFilter{ExcludePGNs: []uint32{129025, 129026}}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})

	b.Run("exclude_pgn_match", func(b *testing.B) {
		filter := &EventFilter{ExcludePGNs: []uint32{127250}}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header, devices)
		}
	})
}

func BenchmarkResolvedFilterMatches(b *testing.B) {
	header := CANHeader{
		Priority:    2,
		PGN:         127250,
		Source:      35,
		Destination: 0xFF,
	}

	b.Run("nil_filter", func(b *testing.B) {
		var filter *resolvedFilter
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header)
		}
	})

	b.Run("pgn_match", func(b *testing.B) {
		filter := &resolvedFilter{
			pgns: map[uint32]struct{}{127250: {}, 129025: {}, 129026: {}},
		}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header)
		}
	})

	b.Run("pgn_miss", func(b *testing.B) {
		filter := &resolvedFilter{
			pgns: map[uint32]struct{}{129025: {}, 129026: {}},
		}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header)
		}
	})

	b.Run("source_match", func(b *testing.B) {
		filter := &resolvedFilter{
			sources: map[uint8]struct{}{35: {}, 42: {}, 100: {}},
		}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header)
		}
	})

	b.Run("pgn_and_source", func(b *testing.B) {
		filter := &resolvedFilter{
			pgns:    map[uint32]struct{}{127250: {}},
			sources: map[uint8]struct{}{35: {}},
		}
		b.ReportAllocs()
		for b.Loop() {
			filter.matches("", header)
		}
	})
}

func BenchmarkFanOut(b *testing.B) {
	header := CANHeader{
		Priority:    2,
		PGN:         127250,
		Source:      35,
		Destination: 0xFF,
	}
	jsonBytes := []byte(`{"seq":1,"ts":"2025-06-15T12:00:00Z","prio":2,"pgn":127250,"src":35,"dst":255,"data":"ff107b0000000000"}`)

	for _, numSubs := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("subscribers_%d", numSubs), func(b *testing.B) {
			broker := NewBroker(BrokerConfig{RingSize: 1024})

			// Create subscribers with large buffers so they don't block.
			for range numSubs {
				broker.Subscribe(nil)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				broker.fanOut("", header, jsonBytes)
			}
		})
	}

	b.Run("subscribers_10_with_pgn_filter", func(b *testing.B) {
		broker := NewBroker(BrokerConfig{RingSize: 1024})

		for range 10 {
			broker.Subscribe(&EventFilter{PGNs: []uint32{127250}})
		}

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			broker.fanOut("", header, jsonBytes)
		}
	})

	b.Run("subscribers_10_with_exclude_filter", func(b *testing.B) {
		broker := NewBroker(BrokerConfig{RingSize: 1024})

		for range 10 {
			broker.Subscribe(&EventFilter{ExcludePGNs: []uint32{129025}})
		}

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			broker.fanOut("", header, jsonBytes)
		}
	})
}
