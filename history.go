package lplex

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/sixfathoms/lplex/journal"
)

// handleHistory serves GET /history queries against journal files.
// Query params:
//   - from: RFC3339 start time (required)
//   - to: RFC3339 end time (default: now)
//   - pgn: filter by PGN (repeatable)
//   - src: filter by source address (repeatable)
//   - limit: max frames to return (default: 10000)
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.broker.journalDir == "" {
		http.Error(w, "journaling is not enabled", http.StatusServiceUnavailable)
		return
	}

	q := r.URL.Query()

	// Parse required "from" param
	fromStr := q.Get("from")
	if fromStr == "" {
		http.Error(w, "from parameter is required (RFC3339)", http.StatusBadRequest)
		return
	}
	fromTime, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid from time: %v", err), http.StatusBadRequest)
		return
	}

	// Parse optional "to" param (default: now)
	toTime := time.Now()
	if toStr := q.Get("to"); toStr != "" {
		toTime, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid to time: %v", err), http.StatusBadRequest)
			return
		}
	}

	if toTime.Before(fromTime) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}

	// Parse optional PGN filter
	var pgns map[uint32]bool
	for _, p := range q["pgn"] {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid pgn: %s", p), http.StatusBadRequest)
			return
		}
		if pgns == nil {
			pgns = make(map[uint32]bool)
		}
		pgns[uint32(v)] = true
	}

	// Parse optional source filter
	var srcs map[uint8]bool
	for _, s := range q["src"] {
		v, err := strconv.ParseUint(s, 10, 8)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid src: %s", s), http.StatusBadRequest)
			return
		}
		if srcs == nil {
			srcs = make(map[uint8]bool)
		}
		srcs[uint8(v)] = true
	}

	// Parse optional limit
	limit := 10000
	if limitStr := q.Get("limit"); limitStr != "" {
		v, err := strconv.Atoi(limitStr)
		if err != nil || v <= 0 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		limit = v
	}

	// Discover journal files
	files, err := filepath.Glob(filepath.Join(s.broker.journalDir, "*.lpj"))
	if err != nil {
		http.Error(w, "failed to list journal files", http.StatusInternalServerError)
		return
	}
	sort.Strings(files)

	if len(files) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
		return
	}

	frames := queryJournalFiles(files, fromTime, toTime, pgns, srcs, limit)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(frames); err != nil {
		s.logger.Error("history: encode response", "error", err)
	}
}

type historyFrame struct {
	Seq  uint64 `json:"seq,omitempty"`
	Ts   string `json:"ts"`
	Prio uint8  `json:"prio"`
	PGN  uint32 `json:"pgn"`
	Src  uint8  `json:"src"`
	Dst  uint8  `json:"dst"`
	Data string `json:"data"`
}

func queryJournalFiles(files []string, from, to time.Time, pgns map[uint32]bool, srcs map[uint8]bool, limit int) []historyFrame {
	var result []historyFrame

	for _, path := range files {
		if len(result) >= limit {
			break
		}

		frames := queryOneFile(path, from, to, pgns, srcs, limit-len(result))
		result = append(result, frames...)
	}

	return result
}

func queryOneFile(path string, from, to time.Time, pgns map[uint32]bool, srcs map[uint8]bool, maxFrames int) []historyFrame {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	reader, err := journal.NewReader(f)
	if err != nil {
		return nil
	}
	defer reader.Close()

	// Seek to the starting time
	if err := reader.SeekToTime(from); err != nil {
		return nil
	}

	var frames []historyFrame

	for reader.Next() {
		if len(frames) >= maxFrames {
			break
		}

		entry := reader.Frame()

		// Stop if past the end time
		if entry.Timestamp.After(to) {
			break
		}

		// Skip frames before start time (SeekToTime positions at block level)
		if entry.Timestamp.Before(from) {
			continue
		}

		header := CANHeader(entry.Header)

		// Apply PGN filter
		if pgns != nil && !pgns[header.PGN] {
			continue
		}

		// Apply source filter
		if srcs != nil && !srcs[header.Source] {
			continue
		}

		frame := historyFrame{
			Ts:   entry.Timestamp.UTC().Format(time.RFC3339Nano),
			Prio: header.Priority,
			PGN:  header.PGN,
			Src:  header.Source,
			Dst:  header.Destination,
			Data: hex.EncodeToString(entry.Data),
		}

		if reader.Version() == journal.Version2 {
			frame.Seq = reader.FrameSeq()
		}

		frames = append(frames, frame)
	}

	return frames
}
