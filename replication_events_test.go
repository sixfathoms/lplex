package lplex

import (
	"testing"
)

func TestEventLogRecordAndRecent(t *testing.T) {
	l := NewEventLog()

	l.Record(EventLiveStart, map[string]any{"seq": 1})
	l.Record(EventCheckpoint, map[string]any{"seq": 2})
	l.Record(EventLiveStop, map[string]any{"seq": 3})

	got := l.Recent(10) // ask for more than exist
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}

	// Newest first
	if got[0].Type != EventLiveStop {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, EventLiveStop)
	}
	if got[1].Type != EventCheckpoint {
		t.Errorf("got[1].Type = %q, want %q", got[1].Type, EventCheckpoint)
	}
	if got[2].Type != EventLiveStart {
		t.Errorf("got[2].Type = %q, want %q", got[2].Type, EventLiveStart)
	}

	// Detail preserved
	if got[2].Detail["seq"] != 1 {
		t.Errorf("got[2].Detail[seq] = %v, want 1", got[2].Detail["seq"])
	}
}

func TestEventLogRecentClamping(t *testing.T) {
	l := NewEventLog()

	l.Record(EventLiveStart, nil)
	l.Record(EventLiveStop, nil)

	got := l.Recent(1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != EventLiveStop {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, EventLiveStop)
	}
}

func TestEventLogEmpty(t *testing.T) {
	l := NewEventLog()
	got := l.Recent(100)
	if got != nil {
		t.Fatalf("expected nil for empty log, got %v", got)
	}
}

func TestEventLogWrapAround(t *testing.T) {
	l := NewEventLog()

	// Write more than the ring buffer capacity
	total := eventLogSize + 500
	for i := range total {
		l.Record(EventCheckpoint, map[string]any{"i": i})
	}

	got := l.Recent(eventLogSize)
	if len(got) != eventLogSize {
		t.Fatalf("expected %d events, got %d", eventLogSize, len(got))
	}

	// Newest should be the last written
	if got[0].Detail["i"] != total-1 {
		t.Errorf("newest event i = %v, want %d", got[0].Detail["i"], total-1)
	}

	// Oldest retained should be total - eventLogSize
	if got[eventLogSize-1].Detail["i"] != total-eventLogSize {
		t.Errorf("oldest event i = %v, want %d", got[eventLogSize-1].Detail["i"], total-eventLogSize)
	}
}
