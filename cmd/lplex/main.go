package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Global persistent flags.
var (
	flagServer string
	flagBoat   string
	flagConfig string
	flagQuiet  bool
	flagJSON   bool
)

// stringSlice implements pflag.Value for repeatable string flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}
func (s *stringSlice) Type() string { return "string" }

// uintSlice implements pflag.Value for repeatable uint flags.
type uintSlice []uint

func (s *uintSlice) String() string {
	parts := make([]string, len(*s))
	for i, v := range *s {
		parts[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(parts, ",")
}
func (s *uintSlice) Set(v string) error {
	n, err := strconv.ParseUint(v, 10, strconv.IntSize)
	if err != nil {
		return err
	}
	*s = append(*s, uint(n))
	return nil
}
func (s *uintSlice) Type() string { return "uint" }

var rootCmd = &cobra.Command{
	Use:   "lplex",
	Short: "NMEA 2000 CAN bus CLI",
	Long:  "Multi-tool CLI for interacting with NMEA 2000 CAN bus data via lplex.",
	Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagServer, "server", "", "lplex server URL (auto-discovered via mDNS if empty)")
	pf.StringVar(&flagBoat, "boat", "", "connect to a named boat from config")
	pf.StringVar(&flagConfig, "config", "", "config file path (default: ~/.config/lplex/lplex.conf)")
	pf.BoolVar(&flagQuiet, "quiet", false, "suppress stderr status messages")
	pf.BoolVar(&flagJSON, "json", false, "force JSON output")

	rootCmd.AddCommand(dumpCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(devicesCmd)
	rootCmd.AddCommand(valuesCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(requestCmd)
	rootCmd.AddCommand(switchesCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(simulateCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(tailCmd)
	rootCmd.AddCommand(dashboardCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
