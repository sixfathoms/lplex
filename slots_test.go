package lplex

import (
	"log/slog"
	"testing"
	"time"
)

func TestParseClientSlot(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		slot, err := ParseClientSlot(ClientSlotConfig{
			ID:            "test-client",
			BufferTimeout: "PT5M",
		})
		if err != nil {
			t.Fatal(err)
		}
		if slot.ID != "test-client" {
			t.Fatalf("expected id test-client, got %s", slot.ID)
		}
		if slot.BufferTimeout != 5*time.Minute {
			t.Fatalf("expected 5m buffer timeout, got %v", slot.BufferTimeout)
		}
		if slot.Filter != nil {
			t.Fatal("expected nil filter")
		}
	})

	t.Run("with_filter", func(t *testing.T) {
		slot, err := ParseClientSlot(ClientSlotConfig{
			ID:            "filtered",
			BufferTimeout: "PT2M",
			Filter: &SlotFilterConfig{
				PGN:          []uint32{129025, 129026},
				Manufacturer: []string{"Garmin"},
				Bus:          []string{"can0"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if slot.Filter == nil {
			t.Fatal("expected filter")
		}
		if len(slot.Filter.PGNs) != 2 {
			t.Fatalf("expected 2 PGNs, got %d", len(slot.Filter.PGNs))
		}
		if len(slot.Filter.Manufacturers) != 1 {
			t.Fatalf("expected 1 manufacturer, got %d", len(slot.Filter.Manufacturers))
		}
		if len(slot.Filter.Buses) != 1 {
			t.Fatalf("expected 1 bus, got %d", len(slot.Filter.Buses))
		}
	})

	t.Run("missing_id", func(t *testing.T) {
		_, err := ParseClientSlot(ClientSlotConfig{BufferTimeout: "PT1M"})
		if err == nil {
			t.Fatal("expected error for missing id")
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		_, err := ParseClientSlot(ClientSlotConfig{ID: "has spaces"})
		if err == nil {
			t.Fatal("expected error for invalid id")
		}
	})

	t.Run("invalid_duration", func(t *testing.T) {
		_, err := ParseClientSlot(ClientSlotConfig{ID: "test", BufferTimeout: "garbage"})
		if err == nil {
			t.Fatal("expected error for invalid duration")
		}
	})

	t.Run("no_timeout", func(t *testing.T) {
		slot, err := ParseClientSlot(ClientSlotConfig{ID: "no-timeout"})
		if err != nil {
			t.Fatal(err)
		}
		if slot.BufferTimeout != 0 {
			t.Fatalf("expected 0 timeout, got %v", slot.BufferTimeout)
		}
	})
}

func TestApplySlots(t *testing.T) {
	broker := NewBroker(BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: 10 * time.Minute,
	})
	go broker.Run(t.Context())

	slots := []ClientSlot{
		{ID: "slot-a", BufferTimeout: 5 * time.Minute},
		{ID: "slot-b", BufferTimeout: 2 * time.Minute, Filter: &EventFilter{PGNs: []uint32{129025}}},
	}

	ApplySlots(broker, slots, slog.Default())

	// Verify sessions were created.
	if s := broker.GetSession("slot-a"); s == nil {
		t.Fatal("slot-a session not found")
	} else if s.BufferTimeout != 5*time.Minute {
		t.Fatalf("slot-a timeout: expected 5m, got %v", s.BufferTimeout)
	}

	if s := broker.GetSession("slot-b"); s == nil {
		t.Fatal("slot-b session not found")
	} else if s.Filter == nil || len(s.Filter.PGNs) != 1 {
		t.Fatal("slot-b filter not applied")
	}

	// Verify non-existent slot returns nil.
	if s := broker.GetSession("nonexistent"); s != nil {
		t.Fatal("expected nil for nonexistent session")
	}
}
