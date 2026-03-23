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
//   - interval: downsample interval (e.g., "1s", "5s", "PT1M"); keeps one
//     frame per (source, PGN) per interval, reducing bandwidth for
//     high-frequency PGNs
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

	// Parse optional downsample interval
	var interval time.Duration
	if intervalStr := q.Get("interval"); intervalStr != "" {
		// Try Go duration first (e.g., "1s", "5s", "1m")
		interval, err = time.ParseDuration(intervalStr)
		if err != nil {
			// Try ISO 8601 duration
			interval, err = ParseISO8601Duration(intervalStr)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid interval: %s", intervalStr), http.StatusBadRequest)
				return
			}
		}
		if interval <= 0 {
			http.Error(w, "interval must be positive", http.StatusBadRequest)
			return
		}
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

	frames := queryJournalFiles(files, fromTime, toTime, pgns, srcs, limit, interval)

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

// downsampleKey identifies a unique (source, PGN) pair for downsampling.
type downsampleKey struct {
	Src uint8
	PGN uint32
}

func queryJournalFiles(files []string, from, to time.Time, pgns map[uint32]bool, srcs map[uint8]bool, limit int, interval time.Duration) []historyFrame {
	var result []historyFrame
	// Track the last emitted bucket per (src, pgn) for downsampling
	lastBucket := make(map[downsampleKey]int64)

	for _, path := range files {
		if len(result) >= limit {
			break
		}

		frames := queryOneFile(path, from, to, pgns, srcs, limit-len(result), interval, lastBucket)
		result = append(result, frames...)
	}

	return result
}

func queryOneFile(path string, from, to time.Time, pgns map[uint32]bool, srcs map[uint8]bool, maxFrames int, interval time.Duration, lastBucket map[downsampleKey]int64) []historyFrame {
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

		// Apply downsampling: keep one frame per (src, pgn) per time bucket
		if interval > 0 {
			bucket := entry.Timestamp.UnixNano() / int64(interval)
			key := downsampleKey{Src: header.Source, PGN: header.PGN}
			if last, ok := lastBucket[key]; ok && last == bucket {
				continue // already emitted a frame for this key in this bucket
			}
			lastBucket[key] = bucket
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
