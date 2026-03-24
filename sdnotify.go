package lplex

import (
	"net"
	"os"
)

// SDNotify sends a notification to systemd via the NOTIFY_SOCKET.
// Returns false if NOTIFY_SOCKET is not set (not running under systemd).
// This is a minimal implementation that avoids external dependencies.
func SDNotify(state string) bool {
	socketAddr := os.Getenv("NOTIFY_SOCKET")
	if socketAddr == "" {
		return false
	}

	conn, err := net.Dial("unixgram", socketAddr)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte(state))
	return err == nil
}

// SDReady sends READY=1 to systemd, indicating the service is ready.
func SDReady() bool {
	return SDNotify("READY=1")
}

// SDWatchdog sends WATCHDOG=1 to systemd, resetting the watchdog timer.
func SDWatchdog() bool {
	return SDNotify("WATCHDOG=1")
}
