package lplex

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// BusSilenceMonitor watches for periods of CAN bus inactivity and logs
// alerts when no frames have been received for a configurable duration.
// This helps detect CAN bus disconnection or power issues on the boat.
type BusSilenceMonitor struct {
	timeout time.Duration
	broker  *Broker
	logger  *slog.Logger

	mu       sync.Mutex
	silentAt time.Time // when silence was first detected (zero = not silent)
}

// NewBusSilenceMonitor creates a monitor that alerts when no frames have
// been received for the given timeout duration. The timeout must be positive.
func NewBusSilenceMonitor(timeout time.Duration, broker *Broker, logger *slog.Logger) *BusSilenceMonitor {
	return &BusSilenceMonitor{
		timeout: timeout,
		broker:  broker,
		logger:  logger,
	}
}

// Run checks for bus silence periodically and logs warnings.
// It exits when ctx is cancelled.
func (m *BusSilenceMonitor) Run(ctx context.Context) {
	// Check at half the timeout interval for responsive detection,
	// but at least every 5 seconds and no more often than every 1 second.
	interval := m.timeout / 2
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check()
		}
	}
}

func (m *BusSilenceMonitor) check() {
	lastFrame := m.broker.LastFrameTime()
	if lastFrame.IsZero() {
		// No frames received yet — don't alert during startup.
		return
	}

	elapsed := time.Since(lastFrame)

	m.mu.Lock()
	defer m.mu.Unlock()

	if elapsed >= m.timeout {
		if m.silentAt.IsZero() {
			m.silentAt = time.Now()
			m.logger.Warn("bus silence detected: no frames received",
				"timeout", m.timeout,
				"last_frame", lastFrame,
				"elapsed", elapsed.Truncate(time.Second),
			)
		}
	} else {
		if !m.silentAt.IsZero() {
			silenceDur := time.Since(m.silentAt)
			m.logger.Info("bus activity resumed after silence",
				"silence_duration", silenceDur.Truncate(time.Second),
			)
			m.silentAt = time.Time{}
		}
	}
}

// IsSilent reports whether the bus is currently in a silence state.
func (m *BusSilenceMonitor) IsSilent() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.silentAt.IsZero()
}
