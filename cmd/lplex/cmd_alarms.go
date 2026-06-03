package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/pgn"
	"github.com/spf13/cobra"
)

// Alarm decoding for NMEA 2000 journals.
//
// No device on the observed bus emits the standard Alert PGNs (126983-126988).
// Real alarms arrive via diagnostic mechanisms that ARE decoded:
//   - PGN 130762 (RV-C ISO Diagnostics / J1939 DM1): WS500 alternator faults
//   - PGN 61184  (Victron register): "Charger Error Code" register
//
// `lplex alarms <file.lpj>...` scans journals, extracts active alarms, and
// groups flapping alarms into episodes (a contiguous run of the same alarm
// with no gap longer than --gap).

var (
	alarmsJSON bool
	alarmsGap  time.Duration
)

var alarmsCmd = &cobra.Command{
	Use:   "alarms <file.lpj> [file.lpj...]",
	Short: "Show NMEA 2000 alarms from journal files",
	Long: "Scan one or more .lpj journal files for active alarms (WS500 ISO " +
		"diagnostics PGN 130762 and Victron charger errors PGN 61184) and print " +
		"them grouped into episodes.",
	Args: cobra.MinimumNArgs(1),
	RunE: runAlarms,
}

func init() {
	f := alarmsCmd.Flags()
	f.BoolVar(&alarmsJSON, "json", false, "output episodes as JSON")
	f.DurationVar(&alarmsGap, "gap", 2*time.Minute, "merge same-alarm events into one episode if within this gap")
}

// alarmRecord is a single decoded alarm observation (one frame).
type alarmRecord struct {
	Time     time.Time
	Src      uint8
	Severity string  // "red", "yellow", "error"
	Source   string  // mechanism, e.g. "WS500 DM1"
	Code     string  // short code, e.g. "FMI 2", "err 67"
	Detail   string  // human-readable description
	SPN      *uint32 // J1939/RV-C suspect parameter number, if any
}

// key groups records that belong to the same logical alarm.
func (r alarmRecord) key() string {
	return fmt.Sprintf("%d|%s|%s", r.Src, r.Source, r.Code)
}

// alarmEpisode is a contiguous run of the same alarm.
type alarmEpisode struct {
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	Src      uint8     `json:"src"`
	Severity string    `json:"severity"`
	Source   string    `json:"source"`
	Code     string    `json:"code"`
	Detail   string    `json:"detail"`
	SPN      *uint32   `json:"spn,omitempty"`
	Count    int       `json:"count"`
}

// extractAlarms turns a decoded PGN value into zero or more alarm records.
// It is pure (no I/O) so it can be unit-tested directly.
func extractAlarms(decoded any, src uint8, ts time.Time) []alarmRecord {
	switch d := decoded.(type) {
	case pgn.RVCISODiagnostics:
		if !d.IsActiveFault() {
			return nil
		}
		code := "lamp"
		detail := "warning lamp active"
		if d.Fmi != nil {
			code = fmt.Sprintf("FMI %d", *d.Fmi)
			detail = pgn.FMIDescription(*d.Fmi)
		}
		return []alarmRecord{{
			Time: ts, Src: src, Severity: d.Severity(),
			Source: "WS500 DM1", Code: code, Detail: detail, SPN: d.SPN(),
		}}
	case pgn.VictronBatteryRegister:
		// Charger Error Code register with a non-zero payload is an active fault.
		if int(d.Register) != pgn.VictronChargerErrorRegister || d.Payload == nil || *d.Payload == 0 {
			return nil
		}
		detail := pgn.VictronChargerErrorText(*d.Payload)
		if detail == "" {
			detail = fmt.Sprintf("charger error %d", *d.Payload)
		}
		return []alarmRecord{{
			Time: ts, Src: src, Severity: "error",
			Source: "Victron charger", Code: fmt.Sprintf("err %d", *d.Payload), Detail: detail,
		}}
	default:
		return nil
	}
}

// groupEpisodes collapses a time-sorted slice of records into episodes, merging
// consecutive same-alarm records that are no more than gap apart.
func groupEpisodes(records []alarmRecord, gap time.Duration) []alarmEpisode {
	sort.SliceStable(records, func(i, j int) bool { return records[i].Time.Before(records[j].Time) })

	open := map[string]*alarmEpisode{}
	var done []alarmEpisode
	for _, r := range records {
		k := r.key()
		ep := open[k]
		if ep != nil && r.Time.Sub(ep.Last) <= gap {
			ep.Last = r.Time
			ep.Count++
			continue
		}
		if ep != nil {
			done = append(done, *ep)
		}
		open[k] = &alarmEpisode{
			First: r.Time, Last: r.Time, Src: r.Src, Severity: r.Severity,
			Source: r.Source, Code: r.Code, Detail: r.Detail, SPN: r.SPN, Count: 1,
		}
	}
	for _, ep := range open {
		done = append(done, *ep)
	}
	sort.SliceStable(done, func(i, j int) bool { return done[i].First.Before(done[j].First) })
	return done
}

func runAlarms(_ *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var records []alarmRecord
	var firstTs, lastTs time.Time
	for _, path := range args {
		if ctx.Err() != nil {
			return nil
		}
		recs, first, last, err := scanJournalAlarms(ctx, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		records = append(records, recs...)
		if !first.IsZero() && (firstTs.IsZero() || first.Before(firstTs)) {
			firstTs = first
		}
		if last.After(lastTs) {
			lastTs = last
		}
	}

	episodes := groupEpisodes(records, alarmsGap)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	if alarmsJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(episodes)
	}
	printAlarmEpisodes(out, episodes, firstTs, lastTs)
	return nil
}

// scanJournalAlarms reads a journal file and returns alarm records plus the
// first/last frame timestamps seen.
func scanJournalAlarms(ctx context.Context, path string) ([]alarmRecord, time.Time, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	defer func() { _ = f.Close() }()

	reader, err := journal.NewReader(f)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}

	var records []alarmRecord
	var firstTs, lastTs time.Time
	for reader.Next() {
		if ctx.Err() != nil {
			break
		}
		e := reader.Frame()
		if firstTs.IsZero() {
			firstTs = e.Timestamp
		}
		lastTs = e.Timestamp

		info, ok := pgn.Registry[e.Header.PGN]
		if !ok || info.Decode == nil {
			continue
		}
		decoded, derr := info.Decode(e.Data)
		if derr != nil {
			continue
		}
		records = append(records, extractAlarms(decoded, e.Header.Source, e.Timestamp.UTC())...)
	}
	if err := reader.Err(); err != nil {
		return records, firstTs, lastTs, fmt.Errorf("journal read: %w", err)
	}
	return records, firstTs, lastTs, nil
}

func printAlarmEpisodes(w *bufio.Writer, episodes []alarmEpisode, firstTs, lastTs time.Time) {
	if !firstTs.IsZero() {
		fmt.Fprintf(w, "Scanned %s → %s (UTC)\n",
			firstTs.UTC().Format("2006-01-02 15:04:05"), lastTs.UTC().Format("2006-01-02 15:04:05"))
	}
	if len(episodes) == 0 {
		fmt.Fprintln(w, "No alarms found.")
		return
	}
	fmt.Fprintf(w, "%d alarm episode(s):\n\n", len(episodes))
	fmt.Fprintf(w, "%-8s %-20s %-7s %5s  %-8s %-8s %-7s  %s\n",
		"SEVERITY", "DEVICE", "CODE", "COUNT", "START", "END", "DUR", "DETAIL")
	for _, ep := range episodes {
		start := ep.First.Format("15:04:05")
		end := ep.Last.Format("15:04:05")
		dur := formatShortDur(ep.Last.Sub(ep.First))
		device := fmt.Sprintf("%s s%d", ep.Source, ep.Src)
		fmt.Fprintf(w, "%-8s %-20s %-7s %5d  %-8s %-8s %-7s  %s\n",
			ep.Severity, device, ep.Code, ep.Count, start, end, dur, ep.Detail)
	}
}

func formatShortDur(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return "-"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

