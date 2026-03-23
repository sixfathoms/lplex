package lplex

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestLoopbackBusName(t *testing.T) {
	bus := NewLoopbackBus("test0", 16, slog.Default())
	if got := bus.Name(); got != "test0" {
		t.Errorf("Name() = %q, want %q", got, "test0")
	}
}

func TestLoopbackBusInjectAndRead(t *testing.T) {
	bus := NewLoopbackBus("loop0", 16, slog.Default())
	rxFrames := make(chan RxFrame, 16)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = bus.ReadFrames(ctx, rxFrames) }()

	frame := RxFrame{
		Timestamp: time.Now(),
		Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
		Data:      []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
	}
	if !bus.Inject(frame) {
		t.Fatal("Inject returned false")
	}

	select {
	case got := <-rxFrames:
		if got.Bus != "loop0" {
			t.Errorf("Bus = %q, want %q", got.Bus, "loop0")
		}
		if got.Header.PGN != 129025 {
			t.Errorf("PGN = %d, want 129025", got.Header.PGN)
		}
		if got.Header.Source != 1 {
			t.Errorf("Source = %d, want 1", got.Header.Source)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
	}
}

func TestLoopbackBusWriteEchoes(t *testing.T) {
	bus := NewLoopbackBus("echo0", 16, slog.Default())
	rxFrames := make(chan RxFrame, 16)
	txFrames := make(chan TxRequest, 16)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = bus.ReadFrames(ctx, rxFrames) }()
	go func() { _ = bus.WriteFrames(ctx, txFrames) }()

	// Give goroutines a moment to start.
	time.Sleep(10 * time.Millisecond)

	txFrames <- TxRequest{
		Header: CANHeader{Priority: 3, PGN: 59904, Source: 254, Destination: 0xFF},
		Data:   []byte{0x01, 0xF0, 0x01},
	}

	select {
	case got := <-rxFrames:
		if got.Bus != "echo0" {
			t.Errorf("echoed Bus = %q, want %q", got.Bus, "echo0")
		}
		if got.Header.PGN != 59904 {
			t.Errorf("echoed PGN = %d, want 59904", got.Header.PGN)
		}
		if got.Header.Source != 254 {
			t.Errorf("echoed Source = %d, want 254", got.Header.Source)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for echoed frame")
	}
}

func TestLoopbackBusInjectDropsWhenFull(t *testing.T) {
	bus := NewLoopbackBus("full0", 1, slog.Default())

	frame := RxFrame{
		Timestamp: time.Now(),
		Header:    CANHeader{PGN: 129025},
	}

	// First inject should succeed (buffer size = 1).
	if !bus.Inject(frame) {
		t.Fatal("first Inject should succeed")
	}
	// Second inject should drop (buffer full, non-blocking).
	if bus.Inject(frame) {
		t.Fatal("second Inject should return false when buffer is full")
	}
}

func TestLoopbackBusWithBroker(t *testing.T) {
	b := newTestBroker()
	go b.Run()
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	bus := NewLoopbackBus("sim0", 256, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = bus.ReadFrames(ctx, b.RxFrames()) }()

	c := b.NewConsumer(ConsumerConfig{Cursor: b.CurrentSeq() + 1})
	defer func() { _ = c.Close() }()

	// Inject a frame via the loopback bus and verify it flows through the broker.
	bus.Inject(RxFrame{
		Timestamp: time.Now(),
		Header:    CANHeader{Priority: 2, PGN: 129025, Source: 42, Destination: 0xFF},
		Data:      []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x00, 0x00, 0x00},
	})

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	frame, err := c.Next(readCtx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if frame.Header.PGN != 129025 {
		t.Errorf("PGN = %d, want 129025", frame.Header.PGN)
	}
	if frame.Header.Source != 42 {
		t.Errorf("Source = %d, want 42", frame.Header.Source)
	}
	if frame.Bus != "sim0" {
		t.Errorf("Bus = %q, want %q", frame.Bus, "sim0")
	}
}

func TestSocketCANBusName(t *testing.T) {
	bus := NewSocketCANBus("can0", slog.Default())
	if got := bus.Name(); got != "can0" {
		t.Errorf("Name() = %q, want %q", got, "can0")
	}
}
