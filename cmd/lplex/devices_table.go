package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
)

// ---------------------------------------------------------------------------
// Device tracking
// ---------------------------------------------------------------------------

type deviceMap struct {
	mu      sync.RWMutex
	devices map[uint8]lplexc.Device
}

func newDeviceMap() *deviceMap {
	return &deviceMap{devices: make(map[uint8]lplexc.Device)}
}

func (dm *deviceMap) update(d lplexc.Device) {
	dm.mu.Lock()
	dm.devices[d.Src] = d
	dm.mu.Unlock()
}

func (dm *deviceMap) get(src uint8) (lplexc.Device, bool) {
	dm.mu.RLock()
	d, ok := dm.devices[src]
	dm.mu.RUnlock()
	return d, ok
}

func (dm *deviceMap) loadAll(devs []lplexc.Device) {
	dm.mu.Lock()
	for _, d := range devs {
		dm.devices[d.Src] = d
	}
	dm.mu.Unlock()
}

func (dm *deviceMap) sorted() []lplexc.Device {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make([]lplexc.Device, 0, len(dm.devices))
	for _, d := range dm.devices {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Src < result[j].Src })
	return result
}

// ---------------------------------------------------------------------------
// Device table rendering
// ---------------------------------------------------------------------------

func classLabel(code uint8) string {
	if name, ok := deviceClasses[code]; ok {
		return fmt.Sprintf("%s (%d)", name, code)
	}
	return fmt.Sprintf("%d", code)
}

func funcLabel(class, fn uint8) string {
	key := uint16(class)<<8 | uint16(fn)
	if name, ok := deviceFunctions[key]; ok {
		return fmt.Sprintf("%s (%d)", name, fn)
	}
	return fmt.Sprintf("%d", fn)
}

func formatTime(s string) string {
	if s == "" || s == "0001-01-01T00:00:00Z" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04:05")
}

func displayManufacturer(d lplexc.Device) string {
	if d.Manufacturer != "" {
		return fmt.Sprintf("%s (%d)", d.Manufacturer, d.ManufacturerCode)
	}
	return fmt.Sprintf("[src=%d]", d.Src)
}

func printDeviceTable(w *os.File, dm *deviceMap) {
	devs := dm.sorted()
	if len(devs) == 0 {
		return
	}

	type row struct {
		dev                    lplexc.Device
		mfctrStr, modelStr     string
		classStr, funcStr      string
		trafficStr             string
		firstStr, lastStr      string
	}
	rows := make([]row, len(devs))
	mfctrW := len("MANUFACTURER")
	modelW := len("MODEL")
	classW := len("CLASS")
	funcW := len("FUNCTION")
	trafficW := len("TRAFFIC")
	for i, d := range devs {
		mfctr := displayManufacturer(d)
		model := d.ModelID
		if d.ProductCode > 0 {
			model = fmt.Sprintf("%s (%d)", d.ModelID, d.ProductCode)
		}
		traffic := formatBytes(d.ByteCount)
		rows[i] = row{
			dev:        d,
			mfctrStr:   mfctr,
			modelStr:   model,
			classStr:   classLabel(d.DeviceClass),
			funcStr:    funcLabel(d.DeviceClass, d.DeviceFunction),
			trafficStr: traffic,
			firstStr:   formatTime(d.FirstSeen),
			lastStr:    formatTime(d.LastSeen),
		}
		mfctrW = max(mfctrW, len(mfctr))
		modelW = max(modelW, len(model))
		classW = max(classW, len(rows[i].classStr))
		funcW = max(funcW, len(rows[i].funcStr))
		trafficW = max(trafficW, len(traffic))
	}

	hLine := func(left, mid, right, fill string) string {
		return left +
			strings.Repeat(fill, 5) + mid +
			strings.Repeat(fill, 18) + mid +
			strings.Repeat(fill, mfctrW+2) + mid +
			strings.Repeat(fill, modelW+2) + mid +
			strings.Repeat(fill, classW+2) + mid +
			strings.Repeat(fill, funcW+2) + mid +
			strings.Repeat(fill, 6) + mid +
			strings.Repeat(fill, trafficW+2) + mid +
			strings.Repeat(fill, 10) + mid +
			strings.Repeat(fill, 10) + right
	}

	top := hLine("┌", "┬", "┐", "─")
	sep := hLine("├", "┼", "┤", "─")
	bot := hLine("└", "┴", "┘", "─")

	fmt.Fprintf(w, "\n%s%s%s\n", ansiDim, top, ansiReset)
	fmt.Fprintf(w, "%s│%s %sSRC%s %s│%s %sNAME            %s %s│%s %s%-*s%s %s│%s %s%-*s%s %s│%s %s%-*s%s %s│%s %s%-*s%s %s│%s %sINST%s %s│%s %s%*s%s %s│%s %sFIRST   %s %s│%s %sLAST    %s %s│%s\n",
		ansiDim, ansiReset,
		ansiBold, ansiReset,
		ansiDim, ansiReset,
		ansiBold, ansiReset,
		ansiDim, ansiReset,
		ansiBold, mfctrW, "MANUFACTURER", ansiReset,
		ansiDim, ansiReset,
		ansiBold, modelW, "MODEL", ansiReset,
		ansiDim, ansiReset,
		ansiBold, classW, "CLASS", ansiReset,
		ansiDim, ansiReset,
		ansiBold, funcW, "FUNCTION", ansiReset,
		ansiDim, ansiReset,
		ansiBold, ansiReset,
		ansiDim, ansiReset,
		ansiBold, trafficW, "TRAFFIC", ansiReset,
		ansiDim, ansiReset,
		ansiBold, ansiReset,
		ansiDim, ansiReset,
		ansiBold, ansiReset,
		ansiDim, ansiReset,
	)
	fmt.Fprintf(w, "%s%s%s\n", ansiDim, sep, ansiReset)

	for _, r := range rows {
		sc := colorForSrc(r.dev.Src)
		fmt.Fprintf(w, "%s│%s %s%3d%s %s│%s %s%-16s%s %s│%s %s%-*s%s %s│%s %-*s %s│%s %-*s %s│%s %-*s %s│%s %4d %s│%s %*s %s│%s %8s %s│%s %8s %s│%s\n",
			ansiDim, ansiReset,
			sc+ansiBold, r.dev.Src, ansiReset,
			ansiDim, ansiReset,
			ansiDim, r.dev.Name, ansiReset,
			ansiDim, ansiReset,
			sc, mfctrW, r.mfctrStr, ansiReset,
			ansiDim, ansiReset,
			modelW, r.modelStr,
			ansiDim, ansiReset,
			classW, r.classStr,
			ansiDim, ansiReset,
			funcW, r.funcStr,
			ansiDim, ansiReset,
			r.dev.DeviceInstance,
			ansiDim, ansiReset,
			trafficW, r.trafficStr,
			ansiDim, ansiReset,
			r.firstStr,
			ansiDim, ansiReset,
			r.lastStr,
			ansiDim, ansiReset,
		)
	}

	fmt.Fprintf(w, "%s%s%s\n\n", ansiDim, bot, ansiReset)
}

// ---------------------------------------------------------------------------
// NMEA 2000 device class names
// ---------------------------------------------------------------------------

var deviceClasses = map[uint8]string{
	0:   "Reserved",
	10:  "System Tools",
	20:  "Safety",
	25:  "Internetwork",
	30:  "Electrical Distribution",
	35:  "Electrical Generation",
	40:  "Steering/Control",
	50:  "Propulsion",
	60:  "Navigation",
	70:  "Communication",
	75:  "Sensor Interface",
	80:  "Instrumentation",
	85:  "External Environment",
	90:  "Internal Environment",
	100: "Deck/Cargo/Fishing",
	110: "Human Interface",
	120: "Display",
	125: "Entertainment",
}

// ---------------------------------------------------------------------------
// NMEA 2000 device function names, keyed by (class<<8 | function)
// ---------------------------------------------------------------------------

var deviceFunctions = map[uint16]string{
	10<<8 | 130: "Diagnostic",
	10<<8 | 140: "Bus Traffic Logger",
	20<<8 | 110: "Alarm Enunciator",
	20<<8 | 130: "EPIRB",
	20<<8 | 135: "Man Overboard",
	20<<8 | 140: "Voyage Data Recorder",
	20<<8 | 150: "Camera",
	25<<8 | 130: "PC Gateway",
	25<<8 | 131: "N2K-Analog Gateway",
	25<<8 | 132: "Analog-N2K Gateway",
	25<<8 | 133: "N2K-Serial Gateway",
	25<<8 | 135: "NMEA 0183 Gateway",
	25<<8 | 136: "NMEA Network Gateway",
	25<<8 | 137: "N2K Wireless Gateway",
	25<<8 | 140: "Router",
	25<<8 | 150: "Bridge",
	25<<8 | 160: "Repeater",
	30<<8 | 130: "Binary Event Monitor",
	30<<8 | 140: "Load Controller",
	30<<8 | 141: "AC/DC Input",
	30<<8 | 150: "Function Controller",
	35<<8 | 140: "Engine",
	35<<8 | 141: "DC Generator",
	35<<8 | 142: "Solar Panel",
	35<<8 | 143: "Wind Generator",
	35<<8 | 144: "Fuel Cell",
	35<<8 | 145: "Network Power Supply",
	35<<8 | 151: "AC Generator",
	35<<8 | 152: "AC Bus",
	35<<8 | 153: "AC Mains/Shore",
	35<<8 | 154: "AC Output",
	35<<8 | 160: "Battery Charger",
	35<<8 | 161: "Charger+Inverter",
	35<<8 | 162: "Inverter",
	35<<8 | 163: "DC Converter",
	35<<8 | 170: "Battery",
	35<<8 | 180: "Engine Gateway",
	40<<8 | 130: "Follow-up Controller",
	40<<8 | 140: "Mode Controller",
	40<<8 | 150: "Autopilot",
	40<<8 | 155: "Rudder",
	40<<8 | 160: "Heading Sensors",
	40<<8 | 170: "Trim/Interceptors",
	40<<8 | 180: "Attitude Control",
	50<<8 | 130: "Engineroom Monitor",
	50<<8 | 140: "Engine",
	50<<8 | 141: "DC Generator",
	50<<8 | 150: "Engine Controller",
	50<<8 | 151: "AC Generator",
	50<<8 | 155: "Motor",
	50<<8 | 160: "Engine Gateway",
	50<<8 | 165: "Transmission",
	50<<8 | 170: "Throttle/Shift",
	50<<8 | 180: "Actuator",
	50<<8 | 190: "Gauge Interface",
	50<<8 | 200: "Gauge Large",
	50<<8 | 210: "Gauge Small",
	60<<8 | 130: "Depth",
	60<<8 | 135: "Depth/Speed",
	60<<8 | 136: "Depth/Speed/Temp",
	60<<8 | 140: "Attitude",
	60<<8 | 145: "GNSS",
	60<<8 | 150: "Loran C",
	60<<8 | 155: "Speed",
	60<<8 | 160: "Turn Rate",
	60<<8 | 170: "Integrated Nav",
	60<<8 | 175: "Integrated Nav System",
	60<<8 | 190: "Nav Management",
	60<<8 | 195: "AIS",
	60<<8 | 200: "Radar",
	60<<8 | 201: "Infrared Imaging",
	60<<8 | 205: "ECDIS",
	60<<8 | 210: "ECS",
	60<<8 | 220: "Direction Finder",
	60<<8 | 230: "Voyage Status",
	70<<8 | 130: "EPIRB",
	70<<8 | 140: "AIS",
	70<<8 | 150: "DSC",
	70<<8 | 160: "Data Transceiver",
	70<<8 | 170: "Satellite",
	70<<8 | 180: "MF/HF Radio",
	70<<8 | 190: "VHF Radio",
	75<<8 | 130: "Temperature",
	75<<8 | 140: "Pressure",
	75<<8 | 150: "Fluid Level",
	75<<8 | 160: "Flow",
	75<<8 | 170: "Humidity",
	80<<8 | 130: "Time/Date",
	80<<8 | 140: "VDR",
	80<<8 | 150: "Integrated Instrumentation",
	80<<8 | 160: "General Purpose Display",
	80<<8 | 170: "General Sensor Box",
	80<<8 | 180: "Weather Instruments",
	80<<8 | 190: "Transducer/General",
	80<<8 | 200: "NMEA 0183 Converter",
	85<<8 | 130: "Atmospheric",
	85<<8 | 160: "Aquatic",
	90<<8 | 130: "HVAC",
	100<<8 | 130: "Scale (Catch)",
	110<<8 | 130: "Button Interface",
	110<<8 | 135: "Switch Interface",
	110<<8 | 140: "Analog Interface",
	120<<8 | 130: "Display",
	120<<8 | 140: "Alarm Enunciator",
	125<<8 | 130: "Multimedia Player",
	125<<8 | 140: "Multimedia Controller",
}
