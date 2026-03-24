package lplex

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// ClientSlot defines a pre-configured client session that is created at
// startup. This allows test environments and Docker containers to have
// named sessions ready before any HTTP client connects.
type ClientSlot struct {
	ID            string        `json:"id"`
	BufferTimeout time.Duration `json:"buffer_timeout"`
	Filter        *EventFilter  `json:"filter,omitempty"`
}

// ClientSlotConfig is the JSON/HOCON-friendly representation of a client slot
// before duration parsing.
type ClientSlotConfig struct {
	ID            string            `json:"id"`
	BufferTimeout string            `json:"buffer_timeout"`
	Filter        *SlotFilterConfig `json:"filter,omitempty"`
}

// SlotFilterConfig is the JSON/HOCON-friendly representation of an EventFilter.
type SlotFilterConfig struct {
	PGN          []uint32 `json:"pgn,omitempty"`
	ExcludePGN   []uint32 `json:"exclude_pgn,omitempty"`
	Manufacturer []string `json:"manufacturer,omitempty"`
	Instance     []uint8  `json:"instance,omitempty"`
	Name         []string `json:"name,omitempty"`
	ExcludeName  []string `json:"exclude_name,omitempty"`
	Bus          []string `json:"bus,omitempty"`
}

// ParseClientSlot converts a ClientSlotConfig into a ClientSlot, parsing
// durations and hex NAME values.
func ParseClientSlot(cfg ClientSlotConfig) (ClientSlot, error) {
	if cfg.ID == "" {
		return ClientSlot{}, fmt.Errorf("slot id is required")
	}
	if !clientIDPattern.MatchString(cfg.ID) {
		return ClientSlot{}, fmt.Errorf("invalid slot id %q: must be 1-64 alphanumeric, hyphens, or underscores", cfg.ID)
	}

	var bufTimeout time.Duration
	if cfg.BufferTimeout != "" {
		parsed, err := ParseISO8601Duration(cfg.BufferTimeout)
		if err != nil {
			return ClientSlot{}, fmt.Errorf("slot %q: invalid buffer_timeout %q: %w", cfg.ID, cfg.BufferTimeout, err)
		}
		bufTimeout = parsed
	}

	slot := ClientSlot{
		ID:            cfg.ID,
		BufferTimeout: bufTimeout,
	}

	if cfg.Filter != nil {
		filter := &EventFilter{
			PGNs:          cfg.Filter.PGN,
			ExcludePGNs:   cfg.Filter.ExcludePGN,
			Manufacturers: cfg.Filter.Manufacturer,
			Instances:     cfg.Filter.Instance,
			Buses:         cfg.Filter.Bus,
		}
		for _, nameHex := range cfg.Filter.Name {
			name, err := strconv.ParseUint(nameHex, 16, 64)
			if err != nil {
				return ClientSlot{}, fmt.Errorf("slot %q: invalid CAN name %q: must be hex", cfg.ID, nameHex)
			}
			filter.Names = append(filter.Names, name)
		}
		for _, nameHex := range cfg.Filter.ExcludeName {
			name, err := strconv.ParseUint(nameHex, 16, 64)
			if err != nil {
				return ClientSlot{}, fmt.Errorf("slot %q: invalid exclude CAN name %q: must be hex", cfg.ID, nameHex)
			}
			filter.ExcludeNames = append(filter.ExcludeNames, name)
		}
		slot.Filter = filter
	}

	return slot, nil
}

// ApplySlots creates pre-configured client sessions on the broker.
func ApplySlots(broker *Broker, slots []ClientSlot, logger *slog.Logger) {
	for _, slot := range slots {
		broker.CreateSession(slot.ID, slot.BufferTimeout, slot.Filter)
		logger.Info("pre-configured client slot created",
			"id", slot.ID,
			"buffer_timeout", slot.BufferTimeout,
		)
	}
}
