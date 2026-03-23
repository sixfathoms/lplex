//go:build linux

package lplex

import (
	"context"
	"log/slog"
	"os/exec"
	"testing"
	"time"

	"go.einride.tech/can"
	"go.einride.tech/can/pkg/socketcan"
)

const vcanIface = "vcan0"

// setupVCAN creates a vcan0 virtual CAN interface for testing.
// Skips the test if vcan is not available (e.g. missing kernel module).
func setupVCAN(t *testing.T) {
	t.Helper()

	// Load vcan kernel module (may already be loaded).
	if err := exec.Command("sudo", "modprobe", "vcan").Run(); err != nil {
		t.Skipf("cannot load vcan module: %v (need root or kernel support)", err)
	}

	// Create vcan0 interface (ignore error if it already exists).
	_ = exec.Command("sudo", "ip", "link", "add", "dev", vcanIface, "type", "vcan").Run()

	if err := exec.Command("sudo", "ip", "link", "set", "up", vcanIface).Run(); err != nil {
		t.Skipf("cannot bring up %s: %v", vcanIface, err)
	}

	t.Cleanup(func() {
		_ = exec.Command("sudo", "ip", "link", "del", vcanIface).Run()
	})
}

// sendRawCANFrame writes a single CAN frame directly to vcan using the
// einride socketcan library, bypassing lplex entirely.
func sendRawCANFrame(ctx context.Context, t *testing.T, iface string, id uint32, data []byte) {
	t.Helper()
	conn, err := socketcan.DialContext(ctx, "can", iface)
	if err != nil {
		t.Fatalf("dial %s: %v", iface, err)
	}
	defer func() { _ = conn.Close() }()

	tx := socketcan.NewTransmitter(conn)
	f := can.Frame{
		ID:         id,
		Length:     uint8(len(data)),
		IsExtended: true,
	}
	copy(f.Data[:], data)

	if err := tx.TransmitFrame(ctx, f); err != nil {
		t.Fatalf("transmit: %v", err)
	}
}

// TestVCANEndToEnd creates a vcan0 interface, sends raw CAN frames through
// SocketCAN, and verifies they flow end-to-end through CANReader → Broker →
// Consumer. This catches kernel/userspace mismatches in CAN frame handling.
func TestVCANEndToEnd(t *testing.T) {
	setupVCAN(t)

	logger := slog.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := NewBroker(BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: time.Minute,
		Logger:            logger,
	})
	go broker.Run(ctx)
	defer broker.CloseRx()

	// Start CANReader on vcan0.
	go func() {
		_ = CANReader(ctx, vcanIface, broker.RxFrames(), logger)
	}()

	// Create a consumer to read frames from the broker.
	consumer := broker.NewConsumer(ConsumerConfig{Cursor: broker.CurrentSeq() + 1})
	defer func() { _ = consumer.Close() }()

	// Give CANReader a moment to connect.
	time.Sleep(100 * time.Millisecond)

	// Send a single-frame PGN (129025 = Position Rapid Update, 8 bytes).
	// CAN ID: priority=2, PGN=129025, source=42
	positionPGN := BuildCANID(CANHeader{Priority: 2, PGN: 129025, Source: 42, Destination: 0xFF})
	positionData := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}
	sendRawCANFrame(ctx, t, vcanIface, positionPGN, positionData)

	// Read frame from consumer.
	frame, err := consumer.Next(ctx)
	if err != nil {
		t.Fatalf("consumer.Next: %v", err)
	}

	if frame.Header.PGN != 129025 {
		t.Errorf("PGN = %d, want 129025", frame.Header.PGN)
	}
	if frame.Header.Source != 42 {
		t.Errorf("Source = %d, want 42", frame.Header.Source)
	}
	if frame.Header.Priority != 2 {
		t.Errorf("Priority = %d, want 2", frame.Header.Priority)
	}
	if frame.Bus != vcanIface {
		t.Errorf("Bus = %q, want %q", frame.Bus, vcanIface)
	}
	if len(frame.Data) != 8 {
		t.Fatalf("Data length = %d, want 8", len(frame.Data))
	}
	for i, b := range positionData {
		if frame.Data[i] != b {
			t.Errorf("Data[%d] = 0x%02x, want 0x%02x", i, frame.Data[i], b)
		}
	}
	t.Logf("single frame OK: PGN=%d src=%d data=%x", frame.Header.PGN, frame.Header.Source, frame.Data)
}

// TestVCANFastPacket sends a multi-frame fast-packet PGN through vcan and
// verifies reassembly works end-to-end through the real SocketCAN stack.
func TestVCANFastPacket(t *testing.T) {
	setupVCAN(t)

	logger := slog.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := NewBroker(BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: time.Minute,
		Logger:            logger,
	})
	go broker.Run(ctx)
	defer broker.CloseRx()

	go func() {
		_ = CANReader(ctx, vcanIface, broker.RxFrames(), logger)
	}()

	consumer := broker.NewConsumer(ConsumerConfig{Cursor: broker.CurrentSeq() + 1})
	defer func() { _ = consumer.Close() }()

	time.Sleep(100 * time.Millisecond)

	// Send a fast-packet PGN 126996 (Product Information, 134 bytes).
	// We'll send a smaller payload for testing: 20 bytes total.
	// Frame 0: [seq<<5|0] [totalLen] [6 data bytes]
	// Frame 1: [seq<<5|1] [7 data bytes]
	// Frame 2: [seq<<5|2] [7 data bytes]
	canID := BuildCANID(CANHeader{Priority: 6, PGN: 126996, Source: 10, Destination: 0xFF})

	payload := make([]byte, 20)
	for i := range payload {
		payload[i] = byte(i + 1)
	}

	// Fragment into fast-packet frames.
	fragments := FragmentFastPacket(payload, 0)

	conn, err := socketcan.DialContext(ctx, "can", vcanIface)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	tx := socketcan.NewTransmitter(conn)

	for _, frag := range fragments {
		f := can.Frame{
			ID:         canID,
			Length:     8,
			IsExtended: true,
		}
		copy(f.Data[:], frag)
		if err := tx.TransmitFrame(ctx, f); err != nil {
			t.Fatalf("transmit fragment: %v", err)
		}
		// Small delay between fragments to ensure ordering.
		time.Sleep(2 * time.Millisecond)
	}

	// Read the reassembled frame from consumer.
	frame, err := consumer.Next(ctx)
	if err != nil {
		t.Fatalf("consumer.Next: %v", err)
	}

	if frame.Header.PGN != 126996 {
		t.Errorf("PGN = %d, want 126996", frame.Header.PGN)
	}
	if frame.Header.Source != 10 {
		t.Errorf("Source = %d, want 10", frame.Header.Source)
	}
	if len(frame.Data) != len(payload) {
		t.Fatalf("reassembled length = %d, want %d", len(frame.Data), len(payload))
	}
	for i, b := range payload {
		if frame.Data[i] != b {
			t.Errorf("Data[%d] = 0x%02x, want 0x%02x", i, frame.Data[i], b)
		}
	}
	t.Logf("fast-packet OK: PGN=%d src=%d len=%d", frame.Header.PGN, frame.Header.Source, len(frame.Data))
}

// TestVCANTxEcho verifies the CANWriter → SocketCAN → CANReader echo path.
// On vcan, transmitted frames are echoed back by the kernel, so they should
// appear in the broker's ring buffer.
func TestVCANTxEcho(t *testing.T) {
	setupVCAN(t)

	logger := slog.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := NewBroker(BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: time.Minute,
		Logger:            logger,
	})
	go broker.Run(ctx)
	defer broker.CloseRx()

	// Start both reader and writer on vcan0.
	go func() {
		_ = CANReader(ctx, vcanIface, broker.RxFrames(), logger)
	}()
	go func() {
		_ = CANWriter(ctx, vcanIface, broker.TxFrames(), logger)
	}()

	consumer := broker.NewConsumer(ConsumerConfig{Cursor: broker.CurrentSeq() + 1})
	defer func() { _ = consumer.Close() }()

	time.Sleep(100 * time.Millisecond)

	// Queue a TX frame. The SocketCAN kernel will echo it back through the reader.
	ok := broker.QueueTx(TxRequest{
		Header: CANHeader{Priority: 3, PGN: 59904, Source: 254, Destination: 0xFF},
		Data:   []byte{0x01, 0xF0, 0x01}, // ISO Request for PGN 126721
	})
	if !ok {
		t.Fatal("QueueTx returned false (TX queue full)")
	}

	// The echoed frame should appear through the consumer.
	frame, err := consumer.Next(ctx)
	if err != nil {
		t.Fatalf("consumer.Next: %v", err)
	}

	if frame.Header.PGN != 59904 {
		t.Errorf("PGN = %d, want 59904", frame.Header.PGN)
	}
	if frame.Header.Source != 254 {
		t.Errorf("Source = %d, want 254", frame.Header.Source)
	}
	t.Logf("TX echo OK: PGN=%d src=%d data=%x", frame.Header.PGN, frame.Header.Source, frame.Data)
}
