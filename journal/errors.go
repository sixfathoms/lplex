package journal

import "fmt"

// CorruptError indicates that a journal file has invalid or corrupted data.
// It carries diagnostic context about where the corruption was detected.
type CorruptError struct {
	Path     string // file path, if known
	BlockIdx int    // block index, -1 if header-level
	Offset   int64  // byte offset in file, -1 if unknown
	Reason   string // short description (e.g. "bad_magic", "checksum_mismatch")
	Detail   string // human-readable detail
	Err      error  // underlying error, if any
}

func (e *CorruptError) Error() string {
	msg := "journal corrupt"
	if e.Path != "" {
		msg += ": " + e.Path
	}
	if e.BlockIdx >= 0 {
		msg += fmt.Sprintf(" block %d", e.BlockIdx)
	}
	msg += ": " + e.Detail
	return msg
}

func (e *CorruptError) Unwrap() error { return e.Err }
