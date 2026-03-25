package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/filter"
	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Follow live NMEA 2000 frames (like tail -f)",
	Long: `Stream live frames from an lplex server with minimal configuration.
Simpler alternative to "lplex dump" for quick debugging: auto-reconnect
is always on, and --last N replays recent frames before following.

Examples:
  lplex tail
  lplex tail --last 100
  lplex tail --decode
  lplex tail --pgn 129025 --decode
  lplex tail --server http://inuc1.local:8089`,
	RunE: runTail,
}

var (
	tailDecode  bool
	tailChanges bool
	tailLast    int
	tailWhere   string

	tailFilterPGNs    uintSlice
	tailExcludePGNs   uintSlice
	tailManufacturers stringSlice
	tailInstances     uintSlice
	tailFilterNames   stringSlice
	tailExcludeNames  stringSlice
)

func init() {
	f := tailCmd.Flags()
	f.BoolVar(&tailDecode, "decode", false, "decode known PGNs and display field values")
	f.BoolVar(&tailChanges, "changes", false, "only show frames with changed data")
	f.IntVar(&tailLast, "last", 0, "replay last N frames before following live stream")
	f.StringVar(&tailWhere, "where", "", `display filter expression (e.g. "water_temperature < 280")`)

	f.VarP(&tailFilterPGNs, "pgn", "", "filter by PGN (repeatable)")
	f.VarP(&tailExcludePGNs, "exclude-pgn", "", "exclude PGN (repeatable)")
	f.VarP(&tailManufacturers, "manufacturer", "", "filter by manufacturer (repeatable)")
	f.VarP(&tailInstances, "instance", "", "filter by device instance (repeatable)")
	f.VarP(&tailFilterNames, "name", "", "filter by CAN NAME hex (repeatable)")
	f.VarP(&tailExcludeNames, "exclude-name", "", "exclude by CAN NAME hex (repeatable)")
}

func runTail(cmd *cobra.Command, _ []string) error {
	jsonMode := flagJSON || !isTerminal(os.Stdout)

	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.SetFlags(log.Ltime)

	var displayFilter *filter.Filter
	if tailWhere != "" {
		var err error
		displayFilter, err = filter.Compile(tailWhere)
		if err != nil {
			return fmt.Errorf("invalid --where expression: %w", err)
		}
		if displayFilter.NeedsDecode() {
			tailDecode = true
		}
	}

	// Load config and resolve boat.
	boatSet := cmd.Flags().Changed("boat") || rootCmd.PersistentFlags().Changed("boat")
	if boatSet && flagServer != "" {
		return fmt.Errorf("--boat and --server are mutually exclusive")
	}

	var boat *BoatConfig
	var mdnsTimeout time.Duration
	{
		var cfgExPGNs []uint32
		var cfgExNames []string
		var err error
		boat, mdnsTimeout, cfgExPGNs, cfgExNames, err = loadBoatConfig(flagBoat, flagConfig, boatSet)
		if err != nil {
			return err
		}
		for _, p := range cfgExPGNs {
			tailExcludePGNs = append(tailExcludePGNs, uint(p))
		}
		tailExcludeNames = append(tailExcludeNames, cfgExNames...)
	}

	serverURL := resolveServerURL(flagServer, boat, mdnsTimeout)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := lplexc.NewClient(serverURL)
	devices := newDeviceMap()
	var lastSeq atomic.Uint64

	sseFilter := buildFilter(tailFilterPGNs, tailExcludePGNs, tailManufacturers, tailInstances, tailFilterNames, tailExcludeNames)

	// If --last N is set, use a buffered session to replay recent history
	// then continue following live.
	if tailLast > 0 {
		return tailWithHistory(ctx, client, sseFilter, displayFilter, devices, &lastSeq, jsonMode)
	}

	// Default: ephemeral follow with auto-reconnect.
	return tailFollow(ctx, client, sseFilter, displayFilter, devices, &lastSeq, jsonMode)
}

// tailFollow streams live frames with auto-reconnect. No session, no replay.
func tailFollow(ctx context.Context, client *lplexc.Client, sseFilter *lplexc.Filter, displayFilter *filter.Filter, devices *deviceMap, lastSeq *atomic.Uint64, jsonMode bool) error {
	for {
		err := runEphemeral(ctx, client, jsonMode, tailDecode, tailChanges, sseFilter, displayFilter, devices, lastSeq)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("disconnected: %v", err)
		}
		log.Printf("reconnecting in 2s...")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// tailWithHistory creates a buffered session, sets the cursor to replay
// the last N frames, then follows live with auto-reconnect.
func tailWithHistory(ctx context.Context, client *lplexc.Client, sseFilter *lplexc.Filter, displayFilter *filter.Filter, devices *deviceMap, lastSeq *atomic.Uint64, jsonMode bool) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "lplex"
	}
	clientID := fmt.Sprintf("%s-tail-%d", hostname, time.Now().UnixMilli()%10000)

	// Create session and set cursor to head - N for initial replay.
	session, err := client.CreateSession(ctx, lplexc.SessionConfig{
		ClientID:      clientID,
		BufferTimeout: "PT1M",
		Filter:        sseFilter,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	info := session.Info()
	startSeq := uint64(0)
	if info.Seq > uint64(tailLast) {
		startSeq = info.Seq - uint64(tailLast)
	}
	if startSeq > 0 {
		if err := session.Ack(ctx, startSeq); err != nil {
			return fmt.Errorf("set replay cursor: %w", err)
		}
	}

	log.Printf("replaying last %d frames (seq %d → %d), then following live", tailLast, startSeq+1, info.Seq)

	if len(info.Devices) > 0 {
		devices.loadAll(info.Devices)
		if !jsonMode {
			printDeviceTable(os.Stderr, devices)
		}
	}

	// Stream with auto-reconnect using the existing runBuffered logic.
	for {
		err := runBuffered(ctx, client, clientID, "PT1M", 5*time.Second, jsonMode, tailDecode, tailChanges, sseFilter, displayFilter, devices, lastSeq)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("disconnected: %v", err)
		}

		if seq := lastSeq.Load(); seq > 0 {
			ackFinal(client, clientID, seq)
		}

		log.Printf("reconnecting in 2s...")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
