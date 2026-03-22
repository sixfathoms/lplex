package lplex

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestBusSilenceMonitor_DetectsSilence(t *testing.T) {
	b := newTestBroker()
	go b.Run()
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	// Inject a frame so LastFrameTime is set.
	injectFrame(b, 129025, 1, []byte{0, 0, 0, 0, 0, 0, 0, 0})
	time.Sleep(20 * time.Millisecond)

	monitor := NewBusSilenceMonitor(50*time.Millisecond, b, slog.Default(), nil)

	if monitor.IsSilent() {
		t.Fatal("expected not silent initially")
	}

	// Wait for the timeout to elapse, then check.
	time.Sleep(80 * time.Millisecond)
	monitor.check()

	if !monitor.IsSilent() {
		t.Fatal("expected silent after timeout elapsed")
	}
}

func TestBusSilenceMonitor_ResumesAfterFrame(t *testing.T) {
	b := newTestBroker()
	go b.Run()
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	injectFrame(b, 129025, 1, []byte{0, 0, 0, 0, 0, 0, 0, 0})
	time.Sleep(20 * time.Millisecond)

	monitor := NewBusSilenceMonitor(50*time.Millisecond, b, slog.Default(), nil)

	// Let silence occur.
	time.Sleep(80 * time.Millisecond)
	monitor.check()
	if !monitor.IsSilent() {
		t.Fatal("expected silent")
	}

	// Inject another frame to resume.
	injectFrame(b, 129025, 1, []byte{1, 0, 0, 0, 0, 0, 0, 0})
	time.Sleep(20 * time.Millisecond)
	monitor.check()

	if monitor.IsSilent() {
		t.Fatal("expected not silent after new frame")
	}
}

func TestBusSilenceMonitor_NoAlertBeforeFirstFrame(t *testing.T) {
	b := newTestBroker()
	go b.Run()
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	monitor := NewBusSilenceMonitor(50*time.Millisecond, b, slog.Default(), nil)

	// No frames received yet — should not alert.
	time.Sleep(80 * time.Millisecond)
	monitor.check()

	if monitor.IsSilent() {
		t.Fatal("should not alert before any frames are received")
	}
}

func TestBusSilenceMonitor_RunExitsOnCancel(t *testing.T) {
	b := newTestBroker()
	go b.Run()
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	monitor := NewBusSilenceMonitor(time.Second, b, slog.Default(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}
