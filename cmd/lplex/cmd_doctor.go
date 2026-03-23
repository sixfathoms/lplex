package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system and server health",
	Long: `Run diagnostic checks against an lplex server.

Checks the remote lplex server for reachability, device discovery, and
health status. Can also optionally check local CAN interfaces (when run
on the boat) and journal directory health.

Checks include:
  - Platform and OS
  - lplex server reachability (mDNS or --server)
  - Server device count (warns if no devices discovered)
  - Server health (/healthz endpoint)
  - CAN interface status (only if --interfaces specified or auto-detected on Linux)
  - Journal directory writable and disk space (only if --journal-dir specified)

Examples:
  lplex doctor
  lplex doctor --server http://inuc1.local:8089
  lplex doctor --server http://inuc1.local:8089 --journal-dir /var/log/lplex`,
	RunE: runDoctor,
}

var (
	doctorJournalDir string
	doctorInterfaces string
)

func init() {
	f := doctorCmd.Flags()
	f.StringVar(&doctorJournalDir, "journal-dir", "", "journal directory to check (optional)")
	f.StringVar(&doctorInterfaces, "interfaces", "", "comma-separated CAN interfaces to check (default: auto-detect)")
}

type checkResult struct {
	name   string
	status string // "ok", "warn", "fail", "skip"
	detail string
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}

	fmt.Println("lplex doctor")
	fmt.Println()

	var results []checkResult

	// Platform check.
	results = append(results, checkPlatform())

	// CAN and journal checks only when explicitly requested via flags, or
	// when running on the boat itself (Linux with CAN interfaces present).
	// The lplex CLI typically runs on a remote laptop, not the boat.
	if doctorInterfaces != "" {
		for _, iface := range strings.Split(doctorInterfaces, ",") {
			results = append(results, checkCANInterface(strings.TrimSpace(iface)))
		}
	} else if runtime.GOOS == "linux" {
		if ifaces := detectCANInterfaces(); len(ifaces) > 0 {
			results = append(results, checkCANModule())
			for _, iface := range ifaces {
				results = append(results, checkCANInterface(iface))
			}
		}
	}

	// Server reachability.
	serverURL := flagServer
	if serverURL == "" {
		// Try mDNS discovery.
		discoverCtx, discoverCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if discovered, err := lplexc.Discover(discoverCtx); err == nil && discovered != "" {
			serverURL = discovered
		}
		discoverCancel()
	}
	if serverURL != "" {
		results = append(results, checkServerReachable(serverURL))
		results = append(results, checkServerDevices(serverURL))
		results = append(results, checkServerHealth(serverURL))
	} else {
		results = append(results, checkResult{
			name:   "Server reachable",
			status: "skip",
			detail: "no --server specified and mDNS discovery found nothing",
		})
	}

	// Journal directory.
	if doctorJournalDir != "" {
		results = append(results, checkJournalDir(doctorJournalDir))
		results = append(results, checkDiskSpace(doctorJournalDir))
	}

	// Print results.
	fmt.Println()
	var warnings, failures int
	for _, r := range results {
		icon := statusIcon(r.status)
		fmt.Printf("  %s %s: %s\n", icon, r.name, r.detail)
		switch r.status {
		case "warn":
			warnings++
		case "fail":
			failures++
		}
	}

	fmt.Println()
	if failures > 0 {
		fmt.Printf("%d check(s) failed, %d warning(s)\n", failures, warnings)
		return fmt.Errorf("%d check(s) failed", failures)
	}
	if warnings > 0 {
		fmt.Printf("All checks passed with %d warning(s)\n", warnings)
	} else {
		fmt.Println("All checks passed")
	}

	return nil
}

func statusIcon(status string) string {
	switch status {
	case "ok":
		return "[OK]  "
	case "warn":
		return "[WARN]"
	case "fail":
		return "[FAIL]"
	case "skip":
		return "[SKIP]"
	default:
		return "[????]"
	}
}

func checkPlatform() checkResult {
	return checkResult{
		name:   "Platform",
		status: "ok",
		detail: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

func checkCANModule() checkResult {
	// Check if the can kernel module is loaded.
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return checkResult{
			name:   "CAN kernel module",
			status: "warn",
			detail: "cannot read /proc/modules",
		}
	}

	modules := string(data)
	hasCAN := strings.Contains(modules, "can ")
	hasVCAN := strings.Contains(modules, "vcan ")
	hasCAN_RAW := strings.Contains(modules, "can_raw ")

	if hasCAN && hasCAN_RAW {
		detail := "can, can_raw loaded"
		if hasVCAN {
			detail += " (vcan available)"
		}
		return checkResult{
			name:   "CAN kernel module",
			status: "ok",
			detail: detail,
		}
	}

	var missing []string
	if !hasCAN {
		missing = append(missing, "can")
	}
	if !hasCAN_RAW {
		missing = append(missing, "can_raw")
	}
	return checkResult{
		name:   "CAN kernel module",
		status: "fail",
		detail: fmt.Sprintf("missing: %s (try: modprobe can can_raw)", strings.Join(missing, ", ")),
	}
}

func detectCANInterfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var ifaces []string
	for _, e := range entries {
		typePath := filepath.Join("/sys/class/net", e.Name(), "type")
		data, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}
		// ARPHRD_CAN = 280
		if strings.TrimSpace(string(data)) == "280" {
			ifaces = append(ifaces, e.Name())
		}
	}
	return ifaces
}

func checkCANInterface(iface string) checkResult {
	name := fmt.Sprintf("CAN interface %s", iface)

	netIface, err := net.InterfaceByName(iface)
	if err != nil {
		return checkResult{
			name:   name,
			status: "fail",
			detail: fmt.Sprintf("not found: %v", err),
		}
	}

	if netIface.Flags&net.FlagUp == 0 {
		return checkResult{
			name:   name,
			status: "fail",
			detail: "interface is DOWN (try: ip link set can0 up type can bitrate 250000)",
		}
	}

	return checkResult{
		name:   name,
		status: "ok",
		detail: "UP",
	}
}

func checkServerReachable(serverURL string) checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/devices", nil)
	if err != nil {
		return checkResult{
			name:   "Server reachable",
			status: "fail",
			detail: fmt.Sprintf("invalid URL: %v", err),
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return checkResult{
			name:   "Server reachable",
			status: "fail",
			detail: fmt.Sprintf("%s — %v", serverURL, err),
		}
	}
	_ = resp.Body.Close()

	if resp.StatusCode != 200 {
		return checkResult{
			name:   "Server reachable",
			status: "fail",
			detail: fmt.Sprintf("%s — HTTP %d", serverURL, resp.StatusCode),
		}
	}

	return checkResult{
		name:   "Server reachable",
		status: "ok",
		detail: serverURL,
	}
}

func checkServerDevices(serverURL string) checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := lplexc.NewClient(serverURL)
	devs, err := client.Devices(ctx)
	if err != nil {
		return checkResult{
			name:   "Server devices",
			status: "warn",
			detail: fmt.Sprintf("could not fetch: %v", err),
		}
	}

	if len(devs) == 0 {
		return checkResult{
			name:   "Server devices",
			status: "warn",
			detail: "no devices discovered (bus may be idle or disconnected)",
		}
	}

	return checkResult{
		name:   "Server devices",
		status: "ok",
		detail: fmt.Sprintf("%d device(s) discovered", len(devs)),
	}
}

func checkServerHealth(serverURL string) checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/healthz", nil)
	if err != nil {
		return checkResult{
			name:   "Server health",
			status: "warn",
			detail: fmt.Sprintf("invalid URL: %v", err),
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return checkResult{
			name:   "Server health",
			status: "warn",
			detail: fmt.Sprintf("healthz unreachable: %v", err),
		}
	}
	_ = resp.Body.Close()

	if resp.StatusCode == 200 {
		return checkResult{
			name:   "Server health",
			status: "ok",
			detail: "healthy",
		}
	}

	return checkResult{
		name:   "Server health",
		status: "warn",
		detail: fmt.Sprintf("healthz returned HTTP %d", resp.StatusCode),
	}
}

func checkJournalDir(dir string) checkResult {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return checkResult{
				name:   "Journal directory",
				status: "fail",
				detail: fmt.Sprintf("%s does not exist", dir),
			}
		}
		return checkResult{
			name:   "Journal directory",
			status: "fail",
			detail: fmt.Sprintf("%s: %v", dir, err),
		}
	}
	if !info.IsDir() {
		return checkResult{
			name:   "Journal directory",
			status: "fail",
			detail: fmt.Sprintf("%s is not a directory", dir),
		}
	}

	// Check writable by attempting to create a temp file.
	tmp := filepath.Join(dir, ".lplex-doctor-test")
	f, err := os.Create(tmp)
	if err != nil {
		return checkResult{
			name:   "Journal directory",
			status: "fail",
			detail: fmt.Sprintf("%s is not writable: %v", dir, err),
		}
	}
	_ = f.Close()
	_ = os.Remove(tmp)

	// Count existing journal files.
	entries, _ := filepath.Glob(filepath.Join(dir, "*.lpj"))

	return checkResult{
		name:   "Journal directory",
		status: "ok",
		detail: fmt.Sprintf("%s (writable, %d journal file(s))", dir, len(entries)),
	}
}

func checkDiskSpace(dir string) checkResult {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return checkResult{
			name:   "Disk space",
			status: "warn",
			detail: fmt.Sprintf("cannot stat %s: %v", dir, err),
		}
	}

	availGB := float64(stat.Bavail*uint64(stat.Bsize)) / (1024 * 1024 * 1024)
	totalGB := float64(stat.Blocks*uint64(stat.Bsize)) / (1024 * 1024 * 1024)
	usedPct := 100.0 * (1.0 - float64(stat.Bavail)/float64(stat.Blocks))

	if availGB < 0.5 {
		return checkResult{
			name:   "Disk space",
			status: "fail",
			detail: fmt.Sprintf("%.1f GB available of %.1f GB (%.0f%% used) — critically low", availGB, totalGB, usedPct),
		}
	}
	if availGB < 2.0 {
		return checkResult{
			name:   "Disk space",
			status: "warn",
			detail: fmt.Sprintf("%.1f GB available of %.1f GB (%.0f%% used) — low", availGB, totalGB, usedPct),
		}
	}

	return checkResult{
		name:   "Disk space",
		status: "ok",
		detail: fmt.Sprintf("%.1f GB available of %.1f GB (%.0f%% used)", availGB, totalGB, usedPct),
	}
}

// init suppresses the unused import warning for exec (used on Linux only via checkCANModule).
var _ = exec.Command
