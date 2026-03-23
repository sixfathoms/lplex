package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/sendpolicy"
	"github.com/spf13/cobra"
)

var loadtestCmd = &cobra.Command{
	Use:   "loadtest",
	Short: "Generate synthetic CAN traffic to stress-test broker throughput",
	Long: `Generate synthetic CAN frames at a configurable rate and feed them
through an in-memory broker to measure throughput, consumer lag, and
journal write performance.

Useful for stress-testing the broker hot path, finding bottlenecks,
and validating that ring buffer, journal, and consumer subsystems
handle high frame rates without dropping data.

Examples:
  lplex loadtest --rate 1000 --duration 30s
  lplex loadtest --rate 5000 --duration 1m --consumers 3
  lplex loadtest --rate 10000 --duration 10s --journal-dir /tmp/loadtest
  lplex loadtest --rate 0 --duration 5s  # unlimited rate (as fast as possible)`,
	RunE: runLoadtest,
}

var (
	loadtestRate       int
	loadtestDuration   time.Duration
	loadtestConsumers  int
	loadtestJournalDir string
	loadtestRingSize   int
	loadtestSources    int
	loadtestPort       int
)

func init() {
	f := loadtestCmd.Flags()
	f.IntVar(&loadtestRate, "rate", 1000, "frames per second (0 = unlimited)")
	f.DurationVar(&loadtestDuration, "duration", 30*time.Second, "test duration")
	f.IntVar(&loadtestConsumers, "consumers", 1, "number of concurrent consumers")
	f.StringVar(&loadtestJournalDir, "journal-dir", "", "journal directory (empty = disabled)")
	f.IntVar(&loadtestRingSize, "ring-size", 65536, "ring buffer size (power of 2)")
	f.IntVar(&loadtestSources, "sources", 10, "number of simulated CAN source addresses")
	f.IntVar(&loadtestPort, "port", 0, "HTTP port for live monitoring (0 = disabled)")
}

func runLoadtest(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:          loadtestRingSize,
		MaxBufferDuration: 10 * time.Minute,
		Logger:            logger,
		DeviceIdleTimeout: -1, // disable
	})
	go broker.Run(ctx)
	defer broker.CloseRx()

	// Optional journal writer.
	if loadtestJournalDir != "" {
		journalCh := make(chan lplex.RxFrame, 16384)
		broker.SetJournal(journalCh)

		jw, err := lplex.NewJournalWriter(lplex.JournalConfig{
			Dir:         loadtestJournalDir,
			Prefix:      "loadtest",
			BlockSize:   262144,
			Compression: journal.CompressionZstd,
			Logger:      logger,
		}, broker.Devices(), journalCh)
		if err != nil {
			return fmt.Errorf("journal writer: %w", err)
		}
		go func() { _ = jw.Run(ctx) }()
	}

	// Optional HTTP server for live monitoring.
	if loadtestPort > 0 {
		srv := lplex.NewServer(broker, logger, sendpolicy.SendPolicy{})
		addr := fmt.Sprintf(":%d", loadtestPort)
		hs := &http.Server{Addr: addr, Handler: srv}
		go func() {
			fmt.Fprintf(os.Stderr, "monitoring at http://localhost%s\n", addr)
			_ = hs.ListenAndServe()
		}()
		defer func() { _ = hs.Close() }()
	}

	// Start consumers.
	consumerFrames := make([]atomic.Uint64, loadtestConsumers)
	for i := range loadtestConsumers {
		c := broker.NewConsumer(lplex.ConsumerConfig{Cursor: broker.CurrentSeq() + 1})
		i := i
		go func() {
			defer func() { _ = c.Close() }()
			for {
				_, err := c.Next(ctx)
				if err != nil {
					return
				}
				consumerFrames[i].Add(1)
			}
		}()
	}

	// Common PGNs for realistic traffic mix.
	pgns := []uint32{129025, 129026, 130310, 130306, 127250, 127488, 128259, 128267, 127257, 130312}

	fmt.Printf("loadtest: rate=%d fps, duration=%s, consumers=%d, ring=%d, sources=%d\n",
		loadtestRate, loadtestDuration, loadtestConsumers, loadtestRingSize, loadtestSources)
	if loadtestJournalDir != "" {
		fmt.Printf("loadtest: journal=%s\n", loadtestJournalDir)
	}
	fmt.Println()

	// Generate frames.
	var produced atomic.Uint64
	startTime := time.Now()
	deadline := time.After(loadtestDuration)

	// Stats ticker.
	statsTicker := time.NewTicker(2 * time.Second)
	defer statsTicker.Stop()

	var lastProduced, lastConsumed uint64
	lastStatTime := startTime

	r := rand.New(rand.NewPCG(42, 137))
	data := make([]byte, 8)

	running := true
	for running {
		select {
		case <-ctx.Done():
			running = false
		case <-deadline:
			running = false
		case <-statsTicker.C:
			now := time.Now()
			elapsed := now.Sub(lastStatTime).Seconds()
			p := produced.Load()
			var totalConsumed uint64
			for i := range consumerFrames {
				totalConsumed += consumerFrames[i].Load()
			}
			stats := broker.Stats()

			prodRate := float64(p-lastProduced) / elapsed
			consRate := float64(totalConsumed-lastConsumed) / elapsed

			fmt.Printf("  produced: %8d (%7.0f/s)  consumed: %8d (%7.0f/s)  lag: %6d  journal_drops: %d\n",
				p, prodRate, totalConsumed/uint64(max(loadtestConsumers, 1)), consRate/float64(max(loadtestConsumers, 1)),
				stats.ConsumerMaxLag, stats.JournalDrops)

			lastProduced = p
			lastConsumed = totalConsumed
			lastStatTime = now
		default:
			// Generate a frame.
			for i := range data {
				data[i] = byte(r.UintN(256))
			}
			frame := lplex.RxFrame{
				Timestamp: time.Now(),
				Header: lplex.CANHeader{
					Priority:    uint8(r.UintN(8)),
					PGN:         pgns[r.IntN(len(pgns))],
					Source:      uint8(r.IntN(loadtestSources) + 1),
					Destination: 0xFF,
				},
				Data: append([]byte(nil), data...),
				Bus:  "load0",
			}

			select {
			case broker.RxFrames() <- frame:
				produced.Add(1)
			case <-ctx.Done():
				running = false
				continue
			}

			// Rate limiting.
			if loadtestRate > 0 {
				p := produced.Load()
				expected := time.Duration(float64(p) / float64(loadtestRate) * float64(time.Second))
				actual := time.Since(startTime)
				if expected > actual {
					time.Sleep(expected - actual)
				}
			}
		}
	}

	// Final stats.
	elapsed := time.Since(startTime)
	p := produced.Load()
	var totalConsumed uint64
	for i := range consumerFrames {
		totalConsumed += consumerFrames[i].Load()
	}
	stats := broker.Stats()

	fmt.Println()
	fmt.Printf("=== Results ===\n")
	fmt.Printf("  duration:       %s\n", elapsed.Truncate(time.Millisecond))
	fmt.Printf("  produced:       %d frames (%.0f/s)\n", p, float64(p)/elapsed.Seconds())
	fmt.Printf("  consumed:       %d frames/consumer (%.0f/s)\n",
		totalConsumed/uint64(max(loadtestConsumers, 1)),
		float64(totalConsumed)/float64(max(loadtestConsumers, 1))/elapsed.Seconds())
	fmt.Printf("  broker head:    %d\n", stats.HeadSeq)
	fmt.Printf("  consumer lag:   %d\n", stats.ConsumerMaxLag)
	fmt.Printf("  journal drops:  %d\n", stats.JournalDrops)
	fmt.Printf("  ring usage:     %d/%d (%.0f%%)\n",
		stats.RingEntries, stats.RingCapacity,
		float64(stats.RingEntries)/float64(stats.RingCapacity)*100)

	return nil
}

