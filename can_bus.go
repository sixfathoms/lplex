package lplex

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// CANBus provides read and write access to a CAN bus. Implementations handle
// the transport (SocketCAN, loopback, etc.) while the broker remains
// transport-agnostic.
type CANBus interface {
	// ReadFrames reads CAN frames from the bus, reassembles fast-packets,
	// and sends completed frames to rxFrames. Blocks until ctx is cancelled
	// or an unrecoverable error occurs.
	ReadFrames(ctx context.Context, rxFrames chan<- RxFrame) error

	// WriteFrames reads TX requests from txFrames and transmits them on the
	// bus. Blocks until ctx is cancelled or txFrames is closed.
	WriteFrames(ctx context.Context, txFrames <-chan TxRequest) error

	// Name returns the bus name (e.g. "can0", "loopback0").
	Name() string
}

// SocketCANBus implements CANBus using Linux SocketCAN.
type SocketCANBus struct {
	iface  string
	logger *slog.Logger
}

// NewSocketCANBus creates a CANBus backed by a Linux SocketCAN interface.
func NewSocketCANBus(iface string, logger *slog.Logger) *SocketCANBus {
	return &SocketCANBus{iface: iface, logger: logger}
}

func (b *SocketCANBus) ReadFrames(ctx context.Context, rxFrames chan<- RxFrame) error {
	return CANReader(ctx, b.iface, rxFrames, b.logger)
}

func (b *SocketCANBus) WriteFrames(ctx context.Context, txFrames <-chan TxRequest) error {
	return CANWriter(ctx, b.iface, txFrames, b.logger)
}

func (b *SocketCANBus) Name() string { return b.iface }

// LoopbackBus implements CANBus as an in-memory loopback: transmitted frames
// are echoed back as received frames (matching SocketCAN's kernel echo
// behavior). Useful for testing and development on platforms without
// SocketCAN (e.g. macOS).
type LoopbackBus struct {
	name   string
	frames chan RxFrame
	logger *slog.Logger

	mu       sync.Mutex
	rxFrames chan<- RxFrame // set by ReadFrames for WriteFrames echo path
}

// NewLoopbackBus creates a CANBus that echoes transmitted frames back as
// received frames. The buffer size controls how many frames can be queued
// via Inject before blocking.
func NewLoopbackBus(name string, bufSize int, logger *slog.Logger) *LoopbackBus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &LoopbackBus{
		name:   name,
		frames: make(chan RxFrame, bufSize),
		logger: logger,
	}
}

// Inject adds a frame to the bus as if it was received from CAN hardware.
// The Bus field is set to the loopback bus name. Non-blocking: drops the
// frame if the internal buffer is full.
func (b *LoopbackBus) Inject(frame RxFrame) bool {
	frame.Bus = b.name
	select {
	case b.frames <- frame:
		return true
	default:
		return false
	}
}

func (b *LoopbackBus) ReadFrames(ctx context.Context, rxFrames chan<- RxFrame) error {
	b.mu.Lock()
	b.rxFrames = rxFrames
	b.mu.Unlock()

	b.logger.Info("loopback reader started", "bus", b.name)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-b.frames:
			select {
			case rxFrames <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (b *LoopbackBus) WriteFrames(ctx context.Context, txFrames <-chan TxRequest) error {
	b.logger.Info("loopback writer started", "bus", b.name)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-txFrames:
			if !ok {
				return nil
			}
			// Echo back as received frame (matches SocketCAN kernel echo).
			b.Inject(RxFrame{
				Timestamp: time.Now(),
				Header:    req.Header,
				Data:      req.Data,
			})
		}
	}
}

func (b *LoopbackBus) Name() string { return b.name }
