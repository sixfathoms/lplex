package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
	"github.com/sixfathoms/lplex/pgn"
	"github.com/spf13/cobra"
)

var (
	valuesWatch        bool
	valuesRaw          bool
	valuesPGNs         uintSlice
	valuesManufacturer stringSlice
)

var valuesCmd = &cobra.Command{
	Use:   "values",
	Short: "Show last-known decoded values per device/PGN",
	Long:  "Display the most recent decoded value for each (device, PGN) pair, with optional filtering and live watch.",
	RunE:  runValues,
}

func init() {
	f := valuesCmd.Flags()
	f.BoolVar(&valuesWatch, "watch", false, "live-updating values")
	f.BoolVar(&valuesRaw, "raw", false, "show raw hex instead of decoded values")
	f.VarP(&valuesPGNs, "pgn", "", "filter by PGN (repeatable)")
	f.VarP(&valuesManufacturer, "manufacturer", "", "filter by manufacturer name (repeatable)")
}

func runValues(cmd *cobra.Command, _ []string) error {
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

	// Build filter for the values endpoint.
	var filter *lplexc.Filter
	if len(valuesPGNs) > 0 || len(valuesManufacturer) > 0 {
		filter = &lplexc.Filter{
			Manufacturers: []string(valuesManufacturer),
		}
		for _, p := range valuesPGNs {
			filter.PGNs = append(filter.PGNs, uint32(p))
		}
	}

	printValues := func() error {
		if valuesRaw {
			return printRawValues(ctx, client, filter, jsonMode)
		}
		return printDecodedValues(ctx, client, filter, jsonMode)
	}

	if err := printValues(); err != nil {
		return err
	}

	if !valuesWatch {
		return nil
	}

	// Watch mode: poll periodically.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fmt.Fprint(os.Stdout, "\033[2J\033[H")
			if err := printValues(); err != nil {
				log.Printf("refresh: %v", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func printRawValues(ctx context.Context, client *lplexc.Client, filter *lplexc.Filter, jsonMode bool) error {
	values, err := client.Values(ctx, filter)
	if err != nil {
		return fmt.Errorf("fetching values: %w", err)
	}

	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(values)
	}

	for _, dv := range values {
		fmt.Printf("%s%s[src=%d] %s%s\n", ansiBold, colorForSrc(dv.Source), dv.Source, dv.Manufacturer, ansiReset)
		for _, pv := range dv.Values {
			var pgnName string
			if info, ok := pgn.Registry[pv.PGN]; ok {
				pgnName = info.Description
			}
			ts := formatTime(pv.Ts)
			fmt.Printf("  %s%-6d%s %-22s %s%s%s  %s\n",
				ansiCyan, pv.PGN, ansiReset,
				pgnName,
				ansiDim, ts, ansiReset,
				pv.Data,
			)
		}
		fmt.Println()
	}
	return nil
}

func printDecodedValues(ctx context.Context, client *lplexc.Client, filter *lplexc.Filter, jsonMode bool) error {
	values, err := client.DecodedValues(ctx, filter)
	if err != nil {
		return fmt.Errorf("fetching decoded values: %w", err)
	}

	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(values)
	}

	for _, dv := range values {
		fmt.Printf("%s%s[src=%d] %s%s\n", ansiBold, colorForSrc(dv.Source), dv.Source, dv.Manufacturer, ansiReset)
		for _, pv := range dv.Values {
			var pgnName string
			if info, ok := pgn.Registry[pv.PGN]; ok {
				pgnName = info.Description
			}
			ts := formatTime(pv.Ts)
			fmt.Printf("  %s%-6d%s %-22s %s%s%s", ansiCyan, pv.PGN, ansiReset, pgnName, ansiDim, ts, ansiReset)
			if pv.Fields != nil {
				b, _ := json.Marshal(pv.Fields)
				// Compact: strip the outer braces for readability.
				s := strings.TrimPrefix(strings.TrimSuffix(string(b), "}"), "{")
				fmt.Printf("  %s%s%s", ansiDim, s, ansiReset)
			}
			fmt.Println()
		}
		fmt.Println()
	}
	return nil
}
