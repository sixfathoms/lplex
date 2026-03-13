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
	"github.com/sixfathoms/lplex/pgn"
	"github.com/spf13/cobra"
)

var (
	requestPGN     uint32
	requestDst     uint8
	requestTimeout time.Duration
	requestDecode  bool
)

var requestCmd = &cobra.Command{
	Use:   "request",
	Short: "Send an ISO request and wait for the response",
	Long:  "Send an ISO Request (PGN 59904) and display the response frame.",
	RunE:  runRequest,
}

func init() {
	f := requestCmd.Flags()
	f.Uint32Var(&requestPGN, "pgn", 0, "PGN to request (required)")
	f.Uint8Var(&requestDst, "dst", 255, "destination address (255 = broadcast)")
	f.DurationVar(&requestTimeout, "timeout", 2*time.Second, "response timeout")
	f.BoolVar(&requestDecode, "decode", false, "decode the response PGN")
	_ = requestCmd.MarkFlagRequired("pgn")
}

func runRequest(_ *cobra.Command, _ []string) error {
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

	// Apply timeout.
	ctx, timeoutCancel := context.WithTimeout(ctx, requestTimeout)
	defer timeoutCancel()

	client := lplexc.NewClient(serverURL)

	frame, err := client.RequestPGN(ctx, requestPGN, requestDst)
	if err != nil {
		return fmt.Errorf("request PGN %d: %w", requestPGN, err)
	}

	if jsonMode {
		type jsonResponse struct {
			Seq         uint64 `json:"seq"`
			Ts          string `json:"ts"`
			Prio        uint8  `json:"prio"`
			PGN         uint32 `json:"pgn"`
			Src         uint8  `json:"src"`
			Dst         uint8  `json:"dst"`
			Data        string `json:"data"`
			Decoded     any    `json:"decoded,omitempty"`
			DecodeError string `json:"decode_error,omitempty"`
		}
		jr := jsonResponse{
			Seq: frame.Seq, Ts: frame.Ts, Prio: frame.Prio,
			PGN: frame.PGN, Src: frame.Src, Dst: frame.Dst, Data: frame.Data,
		}
		if requestDecode {
			if v, err := decodeFrame(frame); err != nil {
				jr.DecodeError = err.Error()
			} else if v != nil {
				jr.Decoded = v
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jr)
	}

	// Terminal output.
	var pgnName string
	if info, ok := pgn.Registry[frame.PGN]; ok {
		pgnName = info.Description
	}
	sc := colorForSrc(frame.Src)

	fmt.Printf("%s%s[src=%d]%s  %s%s%-6d%s %s\n",
		sc, ansiBold, frame.Src, ansiReset,
		ansiCyan, ansiBold, frame.PGN, ansiReset,
		pgnName,
	)
	fmt.Printf("  data: %s\n", frame.Data)

	if requestDecode {
		if decoded, err := decodeFrame(frame); err != nil {
			fmt.Printf("  %sdecode error: %s%s\n", ansiRed, err.Error(), ansiReset)
		} else if decoded != nil {
			b, _ := json.MarshalIndent(decoded, "  ", "  ")
			fmt.Printf("  %s%s%s\n", ansiDim, string(b), ansiReset)
		}
	}

	return nil
}
