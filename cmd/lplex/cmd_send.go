package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os/signal"
	"syscall"

	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var (
	sendPGN  uint32
	sendSrc  uint8
	sendDst  uint8
	sendPrio uint8
	sendData string
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a raw CAN frame",
	Long:  "Transmit a CAN frame through the lplex server.",
	RunE:  runSend,
}

func init() {
	f := sendCmd.Flags()
	f.Uint32Var(&sendPGN, "pgn", 0, "PGN to send (required)")
	f.Uint8Var(&sendSrc, "src", 0, "source address")
	f.Uint8Var(&sendDst, "dst", 255, "destination address")
	f.Uint8Var(&sendPrio, "prio", 6, "priority (0-7)")
	f.StringVar(&sendData, "data", "", "frame data as hex string (required)")
	_ = sendCmd.MarkFlagRequired("pgn")
	_ = sendCmd.MarkFlagRequired("data")
}

func runSend(_ *cobra.Command, _ []string) error {
	if flagQuiet {
		log.SetOutput(io.Discard)
	}

	serverURL := resolveServerURL(flagServer, nil, 0)
	if flagBoat != "" || flagConfig != "" {
		boat, mdnsTimeout, _, _, err := loadBoatConfig(flagBoat, flagConfig, flagBoat != "")
		if err != nil {
			return err
		}
		serverURL = resolveServerURL(flagServer, boat, mdnsTimeout)
	}

	data, err := hex.DecodeString(sendData)
	if err != nil {
		return fmt.Errorf("invalid --data hex: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := lplexc.NewClient(serverURL)
	if err := client.Send(ctx, sendPGN, sendSrc, sendDst, sendPrio, data); err != nil {
		return fmt.Errorf("send failed: %w", err)
	}

	log.Printf("sent PGN %d to dst %d (%d bytes)", sendPGN, sendDst, len(data))
	return nil
}
