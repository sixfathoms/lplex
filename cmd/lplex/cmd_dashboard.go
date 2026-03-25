package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sixfathoms/lplex/lplexc"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash"},
	Short:   "Interactive TUI showing live boat data",
	Long: `Interactive terminal dashboard showing live device table, frame rates,
GPS position, and decoded sensor values in a tabbed interface.

Tab 1 (Overview): decoded sensor values and PGN activity summary.
Tab 2 (Devices): full live device table with packet statistics.

Use Tab/Shift+Tab or 1/2 to switch tabs. Press 'q' or Ctrl+C to quit.

Examples:
  lplex dashboard
  lplex dashboard --server http://inuc1.local:8089
  lplex dashboard --refresh 2s`,
	RunE: runDashboard,
}

var dashRefresh time.Duration

func init() {
	dashboardCmd.Flags().DurationVar(&dashRefresh, "refresh", 1*time.Second, "refresh interval")
}

// --- Tab definitions ---

const (
	tabOverview = iota
	tabDevices
	tabCount
)

var tabNames = [tabCount]string{"Overview", "Devices"}

// --- Bubbletea model ---

type dashModel struct {
	client     *lplexc.Client
	ctx        context.Context
	cancel     context.CancelFunc
	devices    []lplexc.Device
	values     []lplexc.DecodedDeviceValues
	err        error
	width      int
	height     int
	lastUpdate time.Time
	activeTab  int
}

type tickMsg time.Time
type dataMsg struct {
	devices []lplexc.Device
	values  []lplexc.DecodedDeviceValues
	err     error
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func fetchData(client *lplexc.Client, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		devs, devErr := client.Devices(fetchCtx)

		valCtx, valCancel := context.WithTimeout(ctx, 3*time.Second)
		defer valCancel()
		vals, valErr := client.DecodedValues(valCtx, nil)

		if devErr != nil {
			return dataMsg{err: devErr}
		}
		if valErr != nil {
			return dataMsg{devices: devs, err: valErr}
		}
		return dataMsg{devices: devs, values: vals}
	}
}

func (m dashModel) Init() tea.Cmd {
	return tea.Batch(
		fetchData(m.client, m.ctx),
		tickCmd(dashRefresh),
	)
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % tabCount
		case "shift+tab":
			m.activeTab = (m.activeTab + tabCount - 1) % tabCount
		case "1":
			m.activeTab = tabOverview
		case "2":
			m.activeTab = tabDevices
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		return m, tea.Batch(
			fetchData(m.client, m.ctx),
			tickCmd(dashRefresh),
		)

	case dataMsg:
		m.lastUpdate = time.Now()
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.devices = msg.devices
			m.values = msg.values
		}
	}

	return m, nil
}

func (m dashModel) View() string {
	if m.width == 0 {
		return "loading..."
	}

	var b strings.Builder
	maxW := min(m.width, 100)

	// Header with tabs
	m.renderHeader(&b, maxW)

	// Active tab content
	switch m.activeTab {
	case tabOverview:
		m.renderOverview(&b, maxW)
	case tabDevices:
		m.renderDevicesTab(&b, maxW)
	}

	// Footer
	m.renderFooter(&b)

	return b.String()
}

func (m dashModel) renderHeader(b *strings.Builder, maxW int) {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	b.WriteString(headerStyle.Render("lplex dashboard"))
	if m.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		b.WriteString("  " + errStyle.Render(m.err.Error()))
	}
	b.WriteString("\n")

	// Tab bar
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Underline(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	for i, name := range tabNames {
		if i > 0 {
			b.WriteString("  ")
		}
		label := fmt.Sprintf(" %d:%s ", i+1, name)
		if i == m.activeTab {
			b.WriteString(activeStyle.Render(label))
		} else {
			b.WriteString(inactiveStyle.Render(label))
		}
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", maxW))
	b.WriteString("\n\n")
}

func (m dashModel) renderOverview(b *strings.Builder, _ int) {
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))

	// Live Values
	b.WriteString(sectionStyle.Render("Live Values"))
	b.WriteString("\n")

	gps := extractValue(m.values, 129025, "latitude", "longitude")
	sog := extractValue(m.values, 129026, "sog", "cog")
	depth := extractValue(m.values, 128267, "depth")
	wind := extractValue(m.values, 130306, "wind_speed", "wind_angle")
	waterTemp := extractValue(m.values, 130312, "temperature")
	heading := extractValue(m.values, 127250, "heading")
	engineRPM := extractValue(m.values, 127488, "engine_speed")
	batteryV := extractValue(m.values, 127508, "voltage")

	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("83"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))

	writeVal := func(label, val string) {
		if val != "" {
			b.WriteString("  " + labelStyle.Render(fmt.Sprintf("%-14s", label)) + valueStyle.Render(val) + "\n")
		}
	}

	writeVal("Position:", gps)
	writeVal("SOG/COG:", sog)
	writeVal("Depth:", depth)
	writeVal("Wind:", wind)
	writeVal("Water Temp:", waterTemp)
	writeVal("Heading:", heading)
	writeVal("Engine RPM:", engineRPM)
	writeVal("Battery:", batteryV)

	if gps == "" && sog == "" && depth == "" && wind == "" {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		b.WriteString("  " + dimStyle.Render("no decoded values available") + "\n")
	}
	b.WriteString("\n")

	// PGN Activity
	b.WriteString(sectionStyle.Render("PGN Activity"))
	fmt.Fprintf(b, " (%d devices)\n", len(m.devices))

	type pgnEntry struct {
		pgn  uint32
		desc string
		srcs int
	}
	var pgns []pgnEntry
	seen := make(map[uint32]bool)
	for _, dv := range m.values {
		for _, v := range dv.Values {
			if !seen[v.PGN] {
				seen[v.PGN] = true
				pgns = append(pgns, pgnEntry{pgn: v.PGN, desc: v.Description, srcs: 1})
			} else {
				for i := range pgns {
					if pgns[i].pgn == v.PGN {
						pgns[i].srcs++
						break
					}
				}
			}
		}
	}
	sort.Slice(pgns, func(i, j int) bool { return pgns[i].pgn < pgns[j].pgn })

	if len(pgns) > 0 {
		fmt.Fprintf(b, "  %-8s %-35s %s\n", "PGN", "Description", "Sources")
		fmt.Fprintf(b, "  %-8s %-35s %s\n", "───", "───────────", "───────")
		maxPGNs := 15
		if len(pgns) < maxPGNs {
			maxPGNs = len(pgns)
		}
		for _, p := range pgns[:maxPGNs] {
			desc := p.desc
			if len(desc) > 35 {
				desc = desc[:32] + "..."
			}
			fmt.Fprintf(b, "  %-8d %-35s %d\n", p.pgn, desc, p.srcs)
		}
		if len(pgns) > maxPGNs {
			fmt.Fprintf(b, "  ... and %d more PGNs\n", len(pgns)-maxPGNs)
		}
	} else {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		b.WriteString("  " + dimStyle.Render("no PGN data") + "\n")
	}
	b.WriteString("\n")
}

func (m dashModel) renderDevicesTab(b *strings.Builder, _ int) {
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	b.WriteString(sectionStyle.Render("Devices"))
	fmt.Fprintf(b, " (%d)\n", len(m.devices))

	if len(m.devices) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		b.WriteString("  " + dimStyle.Render("no devices discovered") + "\n\n")
		return
	}

	fmt.Fprintf(b, "  %-4s %-18s %-14s %-14s %-10s %-10s\n",
		"Src", "Manufacturer", "Model", "Serial", "Packets", "Bytes")
	fmt.Fprintf(b, "  %-4s %-18s %-14s %-14s %-10s %-10s\n",
		"───", "────────────", "─────", "──────", "───────", "─────")

	devs := make([]lplexc.Device, len(m.devices))
	copy(devs, m.devices)
	sort.Slice(devs, func(i, j int) bool { return devs[i].Src < devs[j].Src })

	for _, d := range devs {
		mfr := truncate(d.Manufacturer, 18)
		model := truncate(d.ModelID, 14)
		serial := truncate(d.ModelSerial, 14)
		fmt.Fprintf(b, "  %-4d %-18s %-14s %-14s %-10d %-10d\n",
			d.Src, mfr, model, serial, d.PacketCount, d.ByteCount)
	}
	b.WriteString("\n")
}

func (m dashModel) renderFooter(b *strings.Builder) {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	updated := "never"
	if !m.lastUpdate.IsZero() {
		updated = m.lastUpdate.Format("15:04:05")
	}
	b.WriteString(dimStyle.Render(fmt.Sprintf("Updated %s  |  Refresh %s  |  Tab/1/2: switch  |  'q': quit", updated, dashRefresh)))
	b.WriteString("\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// extractValue looks for specific fields in decoded values and formats them.
func extractValue(values []lplexc.DecodedDeviceValues, pgn uint32, fields ...string) string {
	for _, dv := range values {
		for _, v := range dv.Values {
			if v.PGN != pgn {
				continue
			}
			m, ok := v.Fields.(map[string]any)
			if !ok {
				continue
			}
			var parts []string
			for _, f := range fields {
				if val, ok := m[f]; ok {
					parts = append(parts, fmt.Sprintf("%s=%v", f, val))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		}
	}
	return ""
}

func runDashboard(cmd *cobra.Command, _ []string) error {
	if flagQuiet {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(os.Stderr)
	}

	boatSet := cmd.Flags().Changed("boat") || rootCmd.PersistentFlags().Changed("boat")
	if boatSet && flagServer != "" {
		return fmt.Errorf("--boat and --server are mutually exclusive")
	}

	boat, mdnsTimeout, _, _, err := loadBoatConfig(flagBoat, flagConfig, boatSet)
	if err != nil {
		return err
	}

	serverURL := resolveServerURL(flagServer, boat, mdnsTimeout)

	ctx, cancel := context.WithCancel(cmd.Context())
	client := lplexc.NewClient(serverURL)

	m := dashModel{
		client: client,
		ctx:    ctx,
		cancel: cancel,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		cancel()
		return err
	}
	cancel()
	return nil
}
