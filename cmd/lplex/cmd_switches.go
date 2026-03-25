package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var (
	switchesInstance int
	switchesWatch    bool
)

var switchesCmd = &cobra.Command{
	Use:   "switches",
	Short: "Show binary switch bank status",
	Long:  "Display the state of binary switch banks (PGN 127501) with colored ON/OFF indicators.",
	RunE:  runSwitches,
}

var switchesSetCmd = &cobra.Command{
	Use:   "set SWITCH=STATE [SWITCH=STATE ...]",
	Short: "Control binary switches",
	Long: `Send NMEA Command (PGN 126208) targeting Load Controller Connection
State/Control (PGN 127500) to set switch states. Each switch is commanded
individually by connection ID. The command is addressed to the device that
owns the switch bank (auto-detected from PGN 127501, or use --dst).

Each argument is a SWITCH=STATE pair where SWITCH is the 1-based switch number
and STATE is "on" or "off".

Examples:
  lplex switches set --instance 0 1=on
  lplex switches set --instance 0 1=on 3=off 5=on
  lplex switches set --dst 144 1=on`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSwitchesSet,
}

var (
	switchSetInstance int
	switchSetDst     int
	switchSetSrc     uint8
	switchSetPrio    uint8
)

func init() {
	f := switchesCmd.Flags()
	f.IntVar(&switchesInstance, "instance", -1, "filter to specific switch bank instance (-1 = all)")
	f.BoolVar(&switchesWatch, "watch", false, "live-updating switch status")

	sf := switchesSetCmd.Flags()
	sf.IntVar(&switchSetInstance, "instance", -1, "switch bank instance (for auto-detecting destination device)")
	sf.IntVar(&switchSetDst, "dst", -1, "destination device source address (-1 = auto-detect from --instance)")
	sf.Uint8Var(&switchSetSrc, "src", 0, "source address")
	sf.Uint8Var(&switchSetPrio, "prio", 3, "priority (0-7, default 3)")

	switchesCmd.AddCommand(switchesSetCmd)
}

// switchState represents one switch's state from PGN 127501.
type switchState int

const (
	switchOff         switchState = 0
	switchOn          switchState = 1
	switchError       switchState = 2
	switchUnavailable switchState = 3
)

func (s switchState) String() string {
	switch s {
	case switchOff:
		return "OFF"
	case switchOn:
		return "ON"
	case switchError:
		return "ERR"
	case switchUnavailable:
		return "N/A"
	default:
		return "?"
	}
}

func (s switchState) color() string {
	switch s {
	case switchOff:
		return ansiDim
	case switchOn:
		return ansiGreen + ansiBold
	case switchError:
		return ansiRed + ansiBold
	default:
		return ansiDim
	}
}

func runSwitches(_ *cobra.Command, _ []string) error {
	jsonMode := flagJSON || !isTerminal(os.Stdout)

	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.SetFlags(log.Ltime)

	boat, mdnsTimeout, _, _, err := loadBoatConfig(flagBoat, flagConfig, flagBoat != "")
	if err != nil {
		return err
	}
	serverURL := resolveServerURL(flagServer, boat, mdnsTimeout)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := lplexc.NewClient(serverURL)

	// PGN 127501 = Binary Switch Bank Status
	const switchPGN uint32 = 127501

	printSwitches := func() error {
		filter := &lplexc.Filter{PGNs: []uint32{switchPGN}}
		values, err := client.Values(ctx, filter)
		if err != nil {
			return fmt.Errorf("fetching switch values: %w", err)
		}

		if jsonMode {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(values)
		}

		found := false
		for _, dv := range values {
			for _, pv := range dv.Values {
				if pv.PGN != switchPGN {
					continue
				}

				data, err := hex.DecodeString(pv.Data)
				if err != nil || len(data) < 1 {
					continue
				}

				instance := int(data[0])
				if switchesInstance >= 0 && instance != switchesInstance {
					continue
				}

				found = true
				states := decodeSwitchBank(data)

				sc := colorForSrc(dv.Source)
				fmt.Printf("%s%s[src=%d] %s%s  Bank %d\n",
					sc, ansiBold, dv.Source, dv.Manufacturer, ansiReset, instance)

				for i, st := range states {
					fmt.Printf("  Switch %2d: %s%-3s%s\n",
						i+1, st.color(), st.String(), ansiReset)
				}
				fmt.Println()
			}
		}

		if !found {
			fmt.Println("No switch banks found.")
		}
		return nil
	}

	if err := printSwitches(); err != nil {
		return err
	}

	if !switchesWatch {
		return nil
	}

	// Watch mode: subscribe to ephemeral stream filtered to PGN 127501.
	sub, err := client.Subscribe(ctx, &lplexc.Filter{PGNs: []uint32{switchPGN}})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = sub.Close() }()

	// Refresh on each matching frame (throttled to avoid flicker).
	lastPrint := time.Now()
	for {
		ev, err := sub.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("stream: %w", err)
		}
		if ev.Frame != nil && ev.Frame.PGN == switchPGN {
			if time.Since(lastPrint) < 500*time.Millisecond {
				continue
			}
			lastPrint = time.Now()
			fmt.Fprint(os.Stdout, "\033[2J\033[H")
			if err := printSwitches(); err != nil {
				log.Printf("refresh: %v", err)
			}
		}
	}
}

// parseSwitchArg parses a "SWITCH=STATE" argument.
// Returns the 1-based switch number and the desired state (0=off, 1=on).
func parseSwitchArg(arg string) (int, uint8, error) {
	parts := strings.SplitN(arg, "=", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid argument %q: expected SWITCH=STATE (e.g. 1=on)", arg)
	}

	num, err := strconv.Atoi(parts[0])
	if err != nil || num < 1 || num > 28 {
		return 0, 0, fmt.Errorf("invalid switch number %q: must be 1-28", parts[0])
	}

	var state uint8
	switch strings.ToLower(parts[1]) {
	case "on", "1":
		state = 1
	case "off", "0":
		state = 0
	default:
		return 0, 0, fmt.Errorf("invalid state %q: must be on/off", parts[1])
	}

	return num, state, nil
}

func runSwitchesSet(_ *cobra.Command, args []string) error {
	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.SetFlags(log.Ltime)

	boat, mdnsTimeout, _, _, err := loadBoatConfig(flagBoat, flagConfig, flagBoat != "")
	if err != nil {
		return err
	}
	serverURL := resolveServerURL(flagServer, boat, mdnsTimeout)

	// Parse switch=state pairs from args.
	type switchSet struct {
		num   int
		state uint8
	}
	var sets []switchSet
	for _, arg := range args {
		num, state, err := parseSwitchArg(arg)
		if err != nil {
			return err
		}
		sets = append(sets, switchSet{num: num, state: state})
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := lplexc.NewClient(serverURL)

	// Resolve destination device. PGN 126208 is PDU1, so we address
	// the command to the specific device that owns this switch bank.
	var dst uint8
	if switchSetDst >= 0 {
		dst = uint8(switchSetDst)
	} else {
		if switchSetInstance < 0 {
			return fmt.Errorf("--instance is required for auto-detection (or use --dst)")
		}
		resolved, err := resolveSwitchBankDevice(ctx, client, uint8(switchSetInstance))
		if err != nil {
			return err
		}
		dst = resolved
		log.Printf("auto-detected switch bank owner: src=%d", dst)
	}

	// Send one PGN 126208 Command per switch, targeting PGN 127500
	// (Load Controller Connection State/Control). This matches the
	// protocol used by Maretron N2KAnalyzer.
	//
	// Payload (22 bytes):
	//   Byte 0:    0x01 (function code = Command)
	//   Byte 1-3:  PGN 127500 LE
	//   Byte 4:    0xF8 (priority=8 "don't change" | reserved=0xF0)
	//   Byte 5:    0x08 (8 parameter pairs, all fields of PGN 127500)
	//   Pairs:     [field_number] [value] x 8
	//
	// PGN 127500 fields:
	//   1=sid, 2=connection_id, 3=state, 4=status,
	//   5=operational_status_control, 6=pwm_duty_cycle,
	//   7=time_on, 8=time_off
	//
	// State values: 0=off, 2=on.
	const sendPGN uint32 = 126208
	for _, s := range sets {
		// Map user state (0=off, 1=on) to PGN 127500 state (0=off, 2=on).
		var pgnState uint8
		if s.state == 1 {
			pgnState = 2
		}

		data := buildLoadControllerCommand(uint8(s.num-1), pgnState)
		if err := client.Send(ctx, sendPGN, switchSetSrc, dst, switchSetPrio, data); err != nil {
			return fmt.Errorf("send failed (switch %d): %w", s.num, err)
		}

		state := "OFF"
		if s.state == 1 {
			state = "ON"
		}
		log.Printf("switch %d → %s (dst=%d, connection=%d)", s.num, state, dst, s.num-1)
	}

	return nil
}

// buildLoadControllerCommand builds a PGN 126208 Command payload targeting
// PGN 127500 (Load Controller Connection State/Control) for a single connection.
// All 8 fields are included. The Mastervolt CLMD12 ignores 0xFF sentinel values
// and requires explicit values for all fields.
func buildLoadControllerCommand(connectionID, state uint8) []byte {
	var commandedPGN uint32 = 127500
	data := make([]byte, 23) // 6 header + 8 pairs (7x2 + 1x3 for 16-bit status)
	data[0] = 0x01           // function code: Command
	data[1] = byte(commandedPGN)
	data[2] = byte(commandedPGN >> 8)
	data[3] = byte(commandedPGN >> 16)
	data[4] = 0xF8 // priority=8 (don't change) | reserved=0xF0
	data[5] = 8    // all 8 fields of PGN 127500
	data[6] = 1    // field 1: sid
	data[7] = 0xFF // sid = don't care
	data[8] = 2    // field 2: connection_id
	data[9] = connectionID
	data[10] = 3 // field 3: state
	data[11] = state
	data[12] = 4 // field 4: status (16-bit)
	data[13] = 0 // normal
	data[14] = 0
	data[15] = 5 // field 5: operational_status_control
	data[16] = 0
	data[17] = 6   // field 6: pwm_duty_cycle
	data[18] = 100 // 100%
	data[19] = 7   // field 7: time_on
	data[20] = 100
	data[21] = 8 // field 8: time_off
	data[22] = 100
	return data
}
// resolveSwitchBankDevice queries the values endpoint for PGN 127501 and finds
// the device that reports the given switch bank instance. Returns an error if
// no device or multiple devices match.
func resolveSwitchBankDevice(ctx context.Context, client *lplexc.Client, instance uint8) (uint8, error) {
	const statusPGN uint32 = 127501
	values, err := client.Values(ctx, &lplexc.Filter{PGNs: []uint32{statusPGN}})
	if err != nil {
		return 0, fmt.Errorf("querying switch banks for auto-detect: %w", err)
	}

	type match struct {
		src          uint8
		manufacturer string
	}
	var matches []match
	for _, dv := range values {
		for _, pv := range dv.Values {
			if pv.PGN != statusPGN {
				continue
			}
			data, err := hex.DecodeString(pv.Data)
			if err != nil || len(data) < 1 {
				continue
			}
			if data[0] == instance {
				matches = append(matches, match{src: dv.Source, manufacturer: dv.Manufacturer})
			}
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("no device found reporting switch bank instance %d (use --dst to specify)", instance)
	case 1:
		return matches[0].src, nil
	default:
		var parts []string
		for _, m := range matches {
			label := fmt.Sprintf("src=%d", m.src)
			if m.manufacturer != "" {
				label += " (" + m.manufacturer + ")"
			}
			parts = append(parts, label)
		}
		return 0, fmt.Errorf("multiple devices report switch bank instance %d: %s (use --dst to pick one)",
			instance, strings.Join(parts, ", "))
	}
}

// decodeSwitchBank extracts switch states from PGN 127501 data.
// Byte 0 is the instance, bytes 1+ contain 2-bit switch states.
func decodeSwitchBank(data []byte) []switchState {
	if len(data) < 2 {
		return nil
	}

	var states []switchState
	for i := 1; i < len(data); i++ {
		b := data[i]
		for bit := 0; bit < 4; bit++ {
			st := switchState((b >> (bit * 2)) & 0x03)
			if st == switchUnavailable {
				return states
			}
			states = append(states, st)
		}
	}
	return states
}
