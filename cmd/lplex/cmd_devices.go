package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var devicesWatch bool

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List NMEA 2000 devices on the bus",
	Long:  "Show a table of all discovered devices, with optional live watch mode.",
	RunE:  runDevices,
}

func init() {
	devicesCmd.Flags().BoolVar(&devicesWatch, "watch", false, "live-updating device table")
}

func runDevices(cmd *cobra.Command, _ []string) error {
	jsonMode := flagJSON || !isTerminal(os.Stdout)

	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.SetFlags(log.Ltime)

	serverURL := resolveServerURL(flagServer, nil, 0)
	if flagBoat != "" || flagConfig != "" {
		boat, mdnsTimeout, _, _, err := loadBoatConfig(flagBoat, flagConfig, flagBoat != "")
		if err != nil {
			return err
		}
		serverURL = resolveServerURL(flagServer, boat, mdnsTimeout)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := lplexc.NewClient(serverURL)

	devs, err := client.Devices(ctx)
	if err != nil {
		return fmt.Errorf("fetching devices: %w", err)
	}

	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(devs)
	}

	dm := newDeviceMap()
	dm.loadAll(devs)
	printDeviceTable(os.Stdout, dm)

	if !devicesWatch {
		return nil
	}

	// Watch mode: subscribe to ephemeral stream for device updates.
	sub, err := client.Subscribe(ctx, nil)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = sub.Close() }()

	// Periodically refresh the full device list to catch traffic stats.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if fresh, err := client.Devices(ctx); err == nil {
					dm.loadAll(fresh)
					// Clear screen and reprint.
					fmt.Fprint(os.Stdout, "\033[2J\033[H")
					printDeviceTable(os.Stdout, dm)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		ev, err := sub.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("stream: %w", err)
		}
		if ev.Device != nil {
			dm.update(*ev.Device)
			fmt.Fprint(os.Stdout, "\033[2J\033[H")
			printDeviceTable(os.Stdout, dm)
		}
	}
}
