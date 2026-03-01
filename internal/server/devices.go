package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Device represents an NMEA 2000 device discovered via ISO Address Claim (PGN 60928)
// and optionally enriched with Product Information (PGN 126996).
type Device struct {
	Source           uint8  `json:"src"`
	NAME             uint64 `json:"-"`
	NAMEHex          string `json:"name"`
	Manufacturer     string `json:"manufacturer"`
	ManufacturerCode uint16 `json:"manufacturer_code"`
	DeviceClass      uint8  `json:"device_class"`
	DeviceFunction   uint8  `json:"device_function"`
	DeviceInstance    uint8  `json:"device_instance"`
	UniqueNumber     uint32 `json:"unique_number,omitempty"`

	// PGN 126996 Product Information fields.
	ModelID         string `json:"model_id,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
	ModelVersion    string `json:"model_version,omitempty"`
	ModelSerial     string `json:"model_serial,omitempty"`
	ProductCode     uint16 `json:"product_code,omitempty"`

	// Per-source packet statistics.
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	PacketCount uint64    `json:"packet_count"`
	ByteCount   uint64    `json:"byte_count"`
}

// DeviceRegistry tracks NMEA 2000 devices discovered via PGN 60928.
// Thread-safe for concurrent reads (SSE streams) and writes (broker goroutine).
type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[uint8]*Device // keyed by source address
}

// NewDeviceRegistry creates an empty device registry.
func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{
		devices: make(map[uint8]*Device),
	}
}

// RecordPacket updates per-source packet statistics.
// Returns true if this is a previously unseen source address.
func (r *DeviceRegistry) RecordPacket(source uint8, ts time.Time, dataLen int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if dev, ok := r.devices[source]; ok {
		dev.LastSeen = ts
		dev.PacketCount++
		dev.ByteCount += uint64(dataLen)
		return false
	}

	r.devices[source] = &Device{
		Source:      source,
		FirstSeen:   ts,
		LastSeen:    ts,
		PacketCount: 1,
		ByteCount:   uint64(dataLen),
	}
	return true
}

// HandleAddressClaim processes a PGN 60928 ISO Address Claim.
// Returns the device if this is a new or changed device, nil otherwise.
func (r *DeviceRegistry) HandleAddressClaim(source uint8, data []byte) *Device {
	if len(data) < 8 {
		return nil
	}

	name := binary.LittleEndian.Uint64(data[0:8])

	r.mu.Lock()
	defer r.mu.Unlock()

	existing := r.devices[source]
	if existing != nil && existing.NAME == name {
		return nil // no change
	}

	dev := decodeNAME(name, source)

	// Preserve stats and product info from prior calls.
	if existing != nil {
		dev.FirstSeen = existing.FirstSeen
		dev.LastSeen = existing.LastSeen
		dev.PacketCount = existing.PacketCount
		dev.ByteCount = existing.ByteCount
	}

	r.devices[source] = dev
	return dev
}

// HandleProductInfo processes a PGN 126996 Product Information response.
// Returns the device if fields changed, nil if source is unknown or unchanged.
func (r *DeviceRegistry) HandleProductInfo(source uint8, data []byte) *Device {
	if len(data) < 134 {
		return nil
	}

	productCode := binary.LittleEndian.Uint16(data[2:4])
	modelID := decodeFixedString(data[4:36])
	softwareVersion := decodeFixedString(data[36:76])
	modelVersion := decodeFixedString(data[76:100])
	modelSerial := decodeFixedString(data[100:132])

	r.mu.Lock()
	defer r.mu.Unlock()

	dev, ok := r.devices[source]
	if !ok {
		return nil
	}

	if dev.ProductCode == productCode &&
		dev.ModelID == modelID &&
		dev.SoftwareVersion == softwareVersion &&
		dev.ModelVersion == modelVersion &&
		dev.ModelSerial == modelSerial {
		return nil
	}

	dev.ProductCode = productCode
	dev.ModelID = modelID
	dev.SoftwareVersion = softwareVersion
	dev.ModelVersion = modelVersion
	dev.ModelSerial = modelSerial

	snapshot := *dev
	return &snapshot
}

// decodeFixedString extracts the ASCII string from a fixed-width field,
// terminating at the first null (0x00) or padding (0xFF) byte.
func decodeFixedString(data []byte) string {
	for i, b := range data {
		if b == 0x00 || b == 0xFF {
			return string(data[:i])
		}
	}
	return string(data)
}

// Get returns a snapshot of the device at the given source address, or nil.
func (r *DeviceRegistry) Get(source uint8) *Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dev, ok := r.devices[source]
	if !ok {
		return nil
	}
	snapshot := *dev
	return &snapshot
}

// Snapshot returns a copy of all known devices.
func (r *DeviceRegistry) Snapshot() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		result = append(result, *d)
	}
	return result
}

// SnapshotJSON returns the device list as pre-serialized JSON.
func (r *DeviceRegistry) SnapshotJSON() json.RawMessage {
	devices := r.Snapshot()
	b, _ := json.Marshal(devices)
	return b
}

// decodeNAME parses the 64-bit ISO NAME field from PGN 60928.
//
// NAME field bit layout (64 bits, little-endian):
//
//	bits  0-20:  unique number (21 bits)
//	bits 21-31:  manufacturer code (11 bits)
//	bits 32-34:  device instance lower (3 bits)
//	bits 35-39:  device instance upper (5 bits)
//	bits 40-47:  device function (8 bits)
//	bit  48:     reserved
//	bits 49-55:  device class (7 bits)
//	bits 56-59:  system instance (4 bits)
//	bits 60-62:  industry group (3 bits)
//	bit  63:     arbitrary address capable
func decodeNAME(name uint64, source uint8) *Device {
	uniqueNumber := uint32(name & 0x1FFFFF)
	manufacturerCode := uint16((name >> 21) & 0x7FF)
	instanceLower := uint8((name >> 32) & 0x07)
	instanceUpper := uint8((name >> 35) & 0x1F)
	deviceFunction := uint8((name >> 40) & 0xFF)
	deviceClass := uint8((name >> 49) & 0x7F)

	deviceInstance := (instanceUpper << 3) | instanceLower

	return &Device{
		Source:           source,
		NAME:             name,
		NAMEHex:          fmt.Sprintf("%016x", name),
		Manufacturer:     lookupManufacturer(manufacturerCode),
		ManufacturerCode: manufacturerCode,
		DeviceClass:      deviceClass,
		DeviceFunction:   deviceFunction,
		DeviceInstance:    deviceInstance,
		UniqueNumber:     uniqueNumber,
	}
}

// lookupManufacturer returns a human-readable manufacturer name for common NMEA 2000 codes.
// Source: NMEA manufacturer code database.
func lookupManufacturer(code uint16) string {
	if name, ok := manufacturers[code]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (%d)", code)
}

var manufacturers = map[uint16]string{
	69:   "Maretron",
	78:   "FW Murphy",
	80:   "Twin Disc",
	85:   "Kohler",
	88:   "Hemisphere GPS",
	116:  "BEP",
	135:  "Airmar",
	137:  "Simrad",
	140:  "Lowrance",
	144:  "Mercury Marine",
	147:  "Nautibus",
	148:  "Blue Water Data",
	154:  "Westerbeke",
	163:  "Evinrude",
	165:  "CPAC Systems",
	168:  "Xantrex",
	174:  "Yanmar",
	176:  "Mastervolt",
	185:  "BEP Marine",
	192:  "Floscan",
	198:  "Mystic Valley Comms",
	199:  "Actia",
	211:  "Nobeltec",
	215:  "Oceanic Systems",
	224:  "Yacht Monitoring Solutions",
	228:  "ZF",
	229:  "Garmin",
	233:  "Yacht Devices",
	235:  "SilverHook/Fusion",
	243:  "Coelmo",
	257:  "Honda",
	272:  "Groco",
	273:  "Actisense",
	274:  "Amphenol",
	275:  "Navico",
	283:  "Hamilton Jet",
	285:  "Sea Recovery",
	286:  "Coelmo",
	295:  "BEP Marine",
	304:  "Empir Bus",
	305:  "NovAtel",
	306:  "Sleipner",
	315:  "ICOM",
	328:  "Qwerty",
	341:  "Victron Energy",
	345:  "Korea Maritime University",
	351:  "Thrane and Thrane",
	355:  "Mastervolt",
	356:  "Fischer Panda",
	358:  "Victron",
	370:  "Rolls Royce Marine",
	373:  "Electronic Design",
	374:  "Northern Lights",
	378:  "Glendinning",
	381:  "B&G",
	384:  "Rose Point Navigation",
	385:  "Johnson Outdoors",
	394:  "Capi 2",
	396:  "Beyond Measure",
	400:  "Livorsi Marine",
	404:  "ComNav",
	409:  "Chetco Digital Instruments",
	419:  "Fusion",
	421:  "Standard Horizon",
	422:  "True Heading",
	426:  "Egersund Marine Electronics",
	427:  "Em-Trak Marine Electronics",
	431:  "Tohatsu",
	437:  "Digital Yacht",
	440:  "Comar Systems",
	443:  "VDO/Continental",
	451:  "Parker Hannifin",
	459:  "Alltek Marine Electronics",
	460:  "SAN Giorgio",
	466:  "Ocean Signal",
	467:  "Mastervolt",
	470:  "Webasto",
	471:  "Torqeedo",
	473:  "Silvertek",
	476:  "GME/Standard Communications",
	478:  "Humminbird",
	481:  "Sea Cross Marine",
	493:  "LCJ Capteurs",
	499:  "Vesper Marine",
	502:  "Attwood Marine",
	503:  "Naviop",
	504:  "Vessel Systems & Electronics",
	510:  "Marinesoft",
	517:  "NoLand Engineering",
	518:  "Transas Marine",
	529:  "National Instruments Korea",
	532:  "Shenzhen Jiuzhou Himunication",
	540:  "Cummins",
	557:  "Suzuki",
	571:  "Volvo Penta",
	573:  "Watcheye",
	578:  "Advansea",
	579:  "KVH",
	580:  "San Jose Technology",
	583:  "Yacht Control",
	586:  "Ewol",
	591:  "Raymarine",
	595:  "Diverse Yacht Services",
	600:  "Furuno",
	605:  "Si-Tex",
	612:  "Samwon IT",
	614:  "Seekeeper",
	637:  "Cox Powertrain",
	641:  "Humphree",
	644:  "Ocean LED",
	645:  "Prospec",
	658:  "NovAtel",
	688:  "Poly Planar",
	715:  "Lumishore",
	717:  "Bilt Solar",
	735:  "Yamaha",
	739:  "Dometic",
	743:  "Simrad",
	744:  "Intellian",
	773:  "Broyda Industries",
	776:  "Canadian Automotive",
	795:  "Technicold",
	796:  "Blue Water Desalination",
	803:  "Gill Sensors",
	811:  "HelmSmith",
	815:  "Quick",
	824:  "Undheim Systems",
	838:  "TeamSurv",
	845:  "Honda",
	862:  "Oceanvolt",
	868:  "Prospec",
	890:  "Oceanvolt",
	909:  "Still Water Designs",
	911:  "BlueSea",
	1850: "Yamaha",
	1851: "Yamaha",
	1852: "Yamaha",
	1853: "Yamaha",
	1854: "Yamaha",
	1855: "Yamaha",
}
