package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/sendpolicy"
	"github.com/spf13/cobra"
)

var simulateCmd = &cobra.Command{
	Use:   "simulate",
	Short: "Replay a journal file through a live HTTP server",
	Long: `Replay a journal file at real-time speed (or faster/slower) through an
in-memory broker with a full HTTP API. This simulates a live boat for
development and testing without needing a CAN bus or lplex-server.

All standard lplex HTTP endpoints are available: /events (SSE),
/ws (WebSocket), /devices, /values, /values/decoded, and /history.

Examples:
  lplex simulate --file recording.lpj
  lplex simulate --file recording.lpj --speed 10 --port 8090
  lplex simulate --file recording.lpj --speed 0 --port 8090
  lplex simulate --file recording.lpj --start 2024-03-15T10:00:00Z --loop`,
	RunE: runSimulate,
}

var (
	simFile      string
	simPort      int
	simSpeed     float64
	simStartTime string
	simLoop      bool
	simRingSize  int
)

func init() {
	f := simulateCmd.Flags()
	f.StringVar(&simFile, "file", "", "journal file to replay (required)")
	f.IntVar(&simPort, "port", 8090, "HTTP listen port")
	f.Float64Var(&simSpeed, "speed", 1.0, "playback speed (1.0 = real-time, 0 = as fast as possible, 10 = 10x)")
	f.StringVar(&simStartTime, "start", "", "seek to start time (RFC3339)")
	f.BoolVar(&simLoop, "loop", false, "loop replay when the journal ends")
	f.IntVar(&simRingSize, "ring-size", 65536, "ring buffer size (must be power of 2)")
	_ = simulateCmd.MarkFlagRequired("file")
}

func runSimulate(cmd *cobra.Command, _ []string) error {
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
	go broker.Run()

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

	// Run the replay in the main goroutine.
	go func() {
		if err := replayInto(ctx, broker, logger); err != nil {
			if ctx.Err() == nil {
				logger.Error("replay failed", "error", err)
			}
		}
		// Replay done (or cancelled). If not looping, give clients a moment
		// then shut down.
		if ctx.Err() == nil {
			logger.Info("simulate: replay finished, server still running (Ctrl+C to stop)")
			// Keep the server running after replay ends so clients can
			// still query /devices, /values, etc. Wait for signal.
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

// replayInto reads a journal file and feeds frames into the broker at the
// configured playback speed. With --loop, it restarts from the beginning
// when the file ends.
func replayInto(ctx context.Context, broker *lplex.Broker, logger *slog.Logger) error {
	for {
		if err := replayOnce(ctx, broker, logger); err != nil {
			return err
		}
		if !simLoop || ctx.Err() != nil {
			return nil
		}
		logger.Info("simulate: looping replay")
	}
}

func replayOnce(ctx context.Context, broker *lplex.Broker, logger *slog.Logger) error {
	f, err := os.Open(simFile)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader, err := journal.NewReader(f)
	if err != nil {
		return fmt.Errorf("read journal: %w", err)
	}
	defer reader.Close()

	log.Printf("simulate: %s (%d blocks, version %d, compression %d)",
		simFile, reader.BlockCount(), reader.Version(), reader.Compression())

	if simStartTime != "" {
		t, err := time.Parse(time.RFC3339, simStartTime)
		if err != nil {
			return fmt.Errorf("invalid --start time: %w", err)
		}
		if err := reader.SeekToTime(t); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
		log.Printf("simulate: seeked to %s", t.Format(time.RFC3339))
	}

	var (
		frameCount uint64
		prevTs     time.Time
		firstTs    time.Time
		lastTs     time.Time
	)

	for reader.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		entry := reader.Frame()

		if firstTs.IsZero() {
			firstTs = entry.Timestamp
		}
		lastTs = entry.Timestamp

		// Throttle playback based on inter-frame timing.
		if simSpeed > 0 && !prevTs.IsZero() {
			delta := entry.Timestamp.Sub(prevTs)
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
		prevTs = entry.Timestamp

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

		frameCount++
		if frameCount%10000 == 0 {
			logger.Info("simulate: progress",
				"frames", frameCount,
				"ts", entry.Timestamp.UTC().Format(time.RFC3339),
			)
		}
	}

	if err := reader.Err(); err != nil {
		return fmt.Errorf("journal read: %w", err)
	}

	duration := lastTs.Sub(firstTs)
	log.Printf("simulate: %d frames replayed, %s span (%s to %s)",
		frameCount,
		duration.Truncate(time.Second),
		firstTs.UTC().Format("15:04:05"),
		lastTs.UTC().Format("15:04:05"),
	)

	return nil
}
