package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/sendpolicy"
	"github.com/spf13/cobra"
)

var simulateCmd = &cobra.Command{
	Use:   "simulate",
	Short: "Replay journal files through a live HTTP server",
	Long: `Replay journal files at real-time speed (or faster/slower) through an
in-memory broker with a full HTTP API. This simulates a live boat for
development and testing without needing a CAN bus or lplex-server.

Use --file for a single journal, or --dir to replay all .lpj files in a
directory (sorted by name). With --exit-when-done, the process exits after
replay completes instead of keeping the server running.

All standard lplex HTTP endpoints are available: /events (SSE),
/ws (WebSocket), /devices, /values, /values/decoded, and /history.

Examples:
  lplex simulate --file recording.lpj
  lplex simulate --file recording.lpj --speed 10 --port 8090
  lplex simulate --dir /data/journals --exit-when-done
  lplex simulate --dir /data/journals --speed 0 --exit-when-done
  lplex simulate --file recording.lpj --start 2024-03-15T10:00:00Z --loop`,
	RunE: runSimulate,
}

var (
	simFile         string
	simDir          string
	simPort         int
	simSpeed        float64
	simStartTime    string
	simLoop         bool
	simRingSize     int
	simExitWhenDone bool
	simSlots        string
)

func init() {
	f := simulateCmd.Flags()
	f.StringVar(&simFile, "file", "", "journal file to replay")
	f.StringVar(&simDir, "dir", "", "directory of .lpj files to replay (all files, sorted by name)")
	f.IntVar(&simPort, "port", 8090, "HTTP listen port")
	f.Float64Var(&simSpeed, "speed", 1.0, "playback speed (1.0 = real-time, 0 = as fast as possible, 10 = 10x)")
	f.StringVar(&simStartTime, "start", "", "seek to start time (RFC3339)")
	f.BoolVar(&simLoop, "loop", false, "loop replay when the journal ends")
	f.IntVar(&simRingSize, "ring-size", 65536, "ring buffer size (must be power of 2)")
	f.BoolVar(&simExitWhenDone, "exit-when-done", false, "exit after replay completes instead of keeping the server running")
	f.StringVar(&simSlots, "slots", "", `pre-configured client slots as JSON array (e.g. '[{"id":"test","buffer_timeout":"PT5M"}]')`)
	simulateCmd.MarkFlagsMutuallyExclusive("file", "dir")
}

func runSimulate(cmd *cobra.Command, _ []string) error {
	if simFile == "" && simDir == "" {
		return fmt.Errorf("either --file or --dir is required")
	}

	// Resolve the list of journal files to replay.
	files, err := resolveJournalFiles()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:          simRingSize,
		MaxBufferDuration: 10 * time.Minute,
		Logger:            logger,
		DeviceIdleTimeout: -1, // disable idle expiry during simulation
	})
	go broker.Run(ctx)

	// Apply pre-configured client slots.
	if simSlots != "" {
		slots, err := parseSimSlots(simSlots)
		if err != nil {
			return err
		}
		lplex.ApplySlots(broker, slots, logger)
	}

	// Disable send (no real CAN bus to transmit on).
	srv := lplex.NewServer(broker, logger, sendpolicy.SendPolicy{Enabled: false})

	addr := fmt.Sprintf(":%d", simPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	go func() {
		logger.Info("simulate: HTTP server starting", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "error", err)
			cancel()
		}
	}()

	replayDone := make(chan struct{})
	go func() {
		defer close(replayDone)
		if err := replayInto(ctx, broker, files, logger); err != nil {
			if ctx.Err() == nil {
				logger.Error("replay failed", "error", err)
			}
		}
		if ctx.Err() == nil {
			if simExitWhenDone {
				logger.Info("simulate: replay finished, exiting")
				cancel()
			} else {
				logger.Info("simulate: replay finished, server still running (Ctrl+C to stop)")
			}
		}
	}()

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown error", "error", err)
	}
	broker.CloseRx()

	return nil
}

// resolveJournalFiles returns the list of .lpj files to replay.
func resolveJournalFiles() ([]string, error) {
	if simFile != "" {
		return []string{simFile}, nil
	}

	entries, err := os.ReadDir(simDir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", simDir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".lpj") {
			files = append(files, filepath.Join(simDir, e.Name()))
		}
	}
	slices.Sort(files)

	if len(files) == 0 {
		return nil, fmt.Errorf("no .lpj files found in %s", simDir)
	}

	return files, nil
}

// replayInto reads journal files and feeds frames into the broker at the
// configured playback speed. With --loop, it restarts from the beginning
// when all files have been replayed.
func replayInto(ctx context.Context, broker *lplex.Broker, files []string, logger *slog.Logger) error {
	for {
		if err := replayAll(ctx, broker, files, logger); err != nil {
			return err
		}
		if !simLoop || ctx.Err() != nil {
			return nil
		}
		logger.Info("simulate: looping replay")
	}
}

// replayState carries timing state across files so inter-file gaps are
// replayed with proper delays.
type replayState struct {
	frameCount uint64
	prevTs     time.Time
	firstTs    time.Time
	lastTs     time.Time
}

func replayAll(ctx context.Context, broker *lplex.Broker, files []string, logger *slog.Logger) error {
	var state replayState
	for i, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Info("simulate: replaying file", "file", file, "index", i+1, "total", len(files))
		if err := replayOneFile(ctx, broker, file, &state, i == 0, logger); err != nil {
			return err
		}
	}

	duration := state.lastTs.Sub(state.firstTs)
	log.Printf("simulate: %d frames replayed across %d file(s), %s span (%s to %s)",
		state.frameCount,
		len(files),
		duration.Truncate(time.Second),
		state.firstTs.UTC().Format("15:04:05"),
		state.lastTs.UTC().Format("15:04:05"),
	)
	return nil
}

func replayOneFile(ctx context.Context, broker *lplex.Broker, path string, state *replayState, isFirst bool, logger *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader, err := journal.NewReader(f)
	if err != nil {
		return fmt.Errorf("read journal %s: %w", path, err)
	}
	defer reader.Close()

	log.Printf("simulate: %s (%d blocks, version %d, compression %d)",
		path, reader.BlockCount(), reader.Version(), reader.Compression())

	if isFirst && simStartTime != "" {
		t, err := time.Parse(time.RFC3339, simStartTime)
		if err != nil {
			return fmt.Errorf("invalid --start time: %w", err)
		}
		if err := reader.SeekToTime(t); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
		log.Printf("simulate: seeked to %s", t.Format(time.RFC3339))
	}

	for reader.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		entry := reader.Frame()

		if state.firstTs.IsZero() {
			state.firstTs = entry.Timestamp
		}
		state.lastTs = entry.Timestamp

		// Throttle playback based on inter-frame timing.
		if simSpeed > 0 && !state.prevTs.IsZero() {
			delta := entry.Timestamp.Sub(state.prevTs)
			if delta > 0 {
				sleepDur := time.Duration(float64(delta) / simSpeed)
				// Cap individual sleeps to 5s to avoid long pauses on gaps.
				if sleepDur > 5*time.Second {
					sleepDur = 5 * time.Second
				}
				select {
				case <-time.After(sleepDur):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		state.prevTs = entry.Timestamp

		header := lplex.CANHeader(entry.Header)

		select {
		case broker.RxFrames() <- lplex.RxFrame{
			Timestamp: entry.Timestamp,
			Header:    header,
			Data:      entry.Data,
		}:
		case <-ctx.Done():
			return ctx.Err()
		}

		state.frameCount++
		if state.frameCount%10000 == 0 {
			logger.Info("simulate: progress",
				"frames", state.frameCount,
				"ts", entry.Timestamp.UTC().Format(time.RFC3339),
			)
		}
	}

	if err := reader.Err(); err != nil {
		return fmt.Errorf("journal read %s: %w", path, err)
	}

	return nil
}

// parseSimSlots parses the --slots JSON flag into ClientSlots.
func parseSimSlots(raw string) ([]lplex.ClientSlot, error) {
	var configs []lplex.ClientSlotConfig
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return nil, fmt.Errorf("invalid --slots JSON: %w", err)
	}
	slots := make([]lplex.ClientSlot, 0, len(configs))
	for _, cfg := range configs {
		slot, err := lplex.ParseClientSlot(cfg)
		if err != nil {
			return nil, fmt.Errorf("invalid slot: %w", err)
		}
		slots = append(slots, slot)
	}
	return slots, nil
}
