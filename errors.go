package lplex

import "fmt"

// SequenceGapError indicates that journal data has a gap in sequence numbers.
// The consumer expected ExpectedSeq but the next available sequence was ActualSeq.
type SequenceGapError struct {
	ExpectedSeq uint64
	ActualSeq   uint64
}

func (e *SequenceGapError) Error() string {
	return fmt.Sprintf("sequence gap: expected seq %d, got %d (gap of %d frames)",
		e.ExpectedSeq, e.ActualSeq, e.ActualSeq-e.ExpectedSeq)
}

// SessionNotFoundError indicates that a client session ID was not found.
type SessionNotFoundError struct {
	SessionID string
}

func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("session not found: %s", e.SessionID)
}

// DeviceNotFoundError indicates that no device was found at the given
// bus and source address.
type DeviceNotFoundError struct {
	Bus    string
	Source uint8
}

func (e *DeviceNotFoundError) Error() string {
	if e.Bus != "" {
		return fmt.Sprintf("device not found: bus=%s source=%d", e.Bus, e.Source)
	}
	return fmt.Sprintf("device not found: source=%d", e.Source)
}
