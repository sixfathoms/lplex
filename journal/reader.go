package journal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"time"

	"github.com/sixfathoms/lplex/canbus"
)

// Entry is a single decoded frame from the journal.
type Entry struct {
	Timestamp time.Time
	Header    canbus.CANHeader
	Data      []byte
}

// Reader reads frames from a block-based journal file.
type Reader struct {
	r         io.ReadSeeker
	blockSize int
	blockBuf  []byte

	// file-level
	blockCount int

	// current block state
	currentBlock int // -1 = before first block
	blockData    []byte
	blockOff     int // read cursor within block frame data area
	frameIdx     int // frame index within block
	frameCount   int
	baseTimeUs   int64
	lastTimeUs   int64
	devTableOff  int

	// current frame (valid after Next returns true)
	entry Entry
	err   error
}

// NewReader opens a journal for reading. Validates the file header.
func NewReader(r io.ReadSeeker) (*Reader, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("read journal header: %w", err)
	}
	if hdr[0] != Magic[0] || hdr[1] != Magic[1] || hdr[2] != Magic[2] {
		return nil, fmt.Errorf("not a journal file (bad magic)")
	}
	if hdr[3] != Version {
		return nil, fmt.Errorf("unsupported journal version %d", hdr[3])
	}
	blockSize := int(binary.LittleEndian.Uint32(hdr[4:8]))
	if blockSize < 4096 || blockSize&(blockSize-1) != 0 {
		return nil, fmt.Errorf("invalid block size %d", blockSize)
	}

	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("seek to end: %w", err)
	}
	dataBytes := end - int64(FileHeaderSize)
	blockCount := int(dataBytes / int64(blockSize))

	return &Reader{
		r:            r,
		blockSize:    blockSize,
		blockBuf:     make([]byte, blockSize),
		blockCount:   blockCount,
		currentBlock: -1,
	}, nil
}

// BlockCount returns the number of complete blocks in the file.
func (jr *Reader) BlockCount() int {
	return jr.blockCount
}

// CurrentBlock returns the current block index, or -1 if before the first block.
func (jr *Reader) CurrentBlock() int {
	return jr.currentBlock
}

// Next advances to the next frame. Returns false when done or on error.
func (jr *Reader) Next() bool {
	for {
		if jr.blockData != nil && jr.frameIdx < jr.frameCount {
			if jr.parseNextFrame() {
				return true
			}
			if jr.err != nil {
				return false
			}
		}

		nextBlock := jr.currentBlock + 1
		if nextBlock >= jr.blockCount {
			return false
		}
		if err := jr.loadBlock(nextBlock); err != nil {
			jr.err = err
			return false
		}
	}
}

// Frame returns the current frame. Only valid after Next returns true.
func (jr *Reader) Frame() Entry {
	return jr.entry
}

// Err returns the first error encountered during iteration.
func (jr *Reader) Err() error {
	return jr.err
}

// SeekBlock positions the reader at the start of block n (0-indexed).
func (jr *Reader) SeekBlock(n int) error {
	if n < 0 || n >= jr.blockCount {
		return fmt.Errorf("block %d out of range [0, %d)", n, jr.blockCount)
	}
	return jr.loadBlock(n)
}

// SeekToTime finds the block containing the given timestamp via binary search
// and positions the reader at the start of that block.
func (jr *Reader) SeekToTime(t time.Time) error {
	if jr.blockCount == 0 {
		return fmt.Errorf("empty journal")
	}

	targetUs := t.UnixMicro()
	var timeBuf [8]byte

	lo, hi := 0, jr.blockCount-1
	result := 0
	for lo <= hi {
		mid := lo + (hi-lo)/2
		off := int64(FileHeaderSize) + int64(mid)*int64(jr.blockSize)
		if _, err := jr.r.Seek(off, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(jr.r, timeBuf[:]); err != nil {
			return err
		}
		baseTimeUs := int64(binary.LittleEndian.Uint64(timeBuf[:]))
		if baseTimeUs <= targetUs {
			result = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}

	return jr.loadBlock(result)
}

// BlockDevices returns all device table entries for the current block.
func (jr *Reader) BlockDevices() []Device {
	if jr.blockData == nil {
		return nil
	}
	entries, _ := readDeviceTable(jr.blockData, jr.devTableOff)
	devices := make([]Device, len(entries))
	for i, e := range entries {
		devices[i] = e.toDevice()
	}
	return devices
}

// deviceTableEntry is a raw device table entry with ActiveFrom.
type deviceTableEntry struct {
	Source          uint8
	NAME            uint64
	ActiveFrom      uint32
	ProductCode     uint16
	ModelID         string
	SoftwareVersion string
	ModelVersion    string
	ModelSerial     string
}

func (e *deviceTableEntry) toDevice() Device {
	return Device{
		Source:          e.Source,
		NAME:            e.NAME,
		ProductCode:     e.ProductCode,
		ModelID:         e.ModelID,
		SoftwareVersion: e.SoftwareVersion,
		ModelVersion:    e.ModelVersion,
		ModelSerial:     e.ModelSerial,
	}
}

// BlockDevicesAt returns the resolved device table at the given frame index.
// For each source, the entry with the largest ActiveFrom <= frameIdx wins.
func (jr *Reader) BlockDevicesAt(frameIdx int) []Device {
	if jr.blockData == nil {
		return nil
	}
	entries, _ := readDeviceTable(jr.blockData, jr.devTableOff)

	best := make(map[uint8]deviceTableEntry)
	for _, e := range entries {
		if int(e.ActiveFrom) <= frameIdx {
			if cur, ok := best[e.Source]; !ok || e.ActiveFrom > cur.ActiveFrom {
				best[e.Source] = e
			}
		}
	}

	result := make([]Device, 0, len(best))
	for _, e := range best {
		result = append(result, e.toDevice())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source < result[j].Source })
	return result
}

// loadBlock reads and validates block n, resetting the frame cursor.
func (jr *Reader) loadBlock(n int) error {
	off := int64(FileHeaderSize) + int64(n)*int64(jr.blockSize)
	if _, err := jr.r.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("seek to block %d: %w", n, err)
	}
	if _, err := io.ReadFull(jr.r, jr.blockBuf); err != nil {
		return fmt.Errorf("read block %d: %w", n, err)
	}

	bs := jr.blockSize

	stored := binary.LittleEndian.Uint32(jr.blockBuf[bs-4:])
	computed := crc32.Checksum(jr.blockBuf[:bs-4], CRC32cTable)
	if stored != computed {
		return fmt.Errorf("block %d checksum mismatch: stored=%08x computed=%08x", n, stored, computed)
	}

	trailerOff := bs - BlockTrailerLen
	devTableOff := int(binary.LittleEndian.Uint16(jr.blockBuf[trailerOff:]))
	frameCount := int(binary.LittleEndian.Uint32(jr.blockBuf[trailerOff+2:]))
	baseTimeUs := int64(binary.LittleEndian.Uint64(jr.blockBuf[0:8]))

	jr.currentBlock = n
	jr.blockData = jr.blockBuf
	jr.blockOff = 8
	jr.frameIdx = 0
	jr.frameCount = frameCount
	jr.baseTimeUs = baseTimeUs
	jr.lastTimeUs = baseTimeUs
	jr.devTableOff = devTableOff

	return nil
}

// parseNextFrame decodes the frame at the current offset.
func (jr *Reader) parseNextFrame() bool {
	data := jr.blockData
	off := jr.blockOff
	limit := jr.devTableOff

	if off >= limit {
		jr.err = fmt.Errorf("frame data overrun at frame %d in block %d", jr.frameIdx, jr.currentBlock)
		return false
	}

	deltaUs, n := binary.Uvarint(data[off:limit])
	if n <= 0 {
		jr.err = fmt.Errorf("bad delta varint at frame %d in block %d", jr.frameIdx, jr.currentBlock)
		return false
	}
	off += n

	if off+4 > limit {
		jr.err = fmt.Errorf("truncated CANID at frame %d in block %d", jr.frameIdx, jr.currentBlock)
		return false
	}
	storedID := binary.LittleEndian.Uint32(data[off:])
	off += 4

	standard := storedID&0x80000000 != 0
	canID := storedID & 0x7FFFFFFF
	header := canbus.ParseCANID(canID)

	var frameData []byte
	if standard {
		if off+8 > limit {
			jr.err = fmt.Errorf("truncated standard frame at frame %d in block %d", jr.frameIdx, jr.currentBlock)
			return false
		}
		frameData = make([]byte, 8)
		copy(frameData, data[off:off+8])
		off += 8
	} else {
		dataLen, n := binary.Uvarint(data[off:limit])
		if n <= 0 {
			jr.err = fmt.Errorf("bad data length varint at frame %d in block %d", jr.frameIdx, jr.currentBlock)
			return false
		}
		off += n
		if off+int(dataLen) > limit {
			jr.err = fmt.Errorf("truncated extended frame at frame %d in block %d", jr.frameIdx, jr.currentBlock)
			return false
		}
		frameData = make([]byte, dataLen)
		copy(frameData, data[off:off+int(dataLen)])
		off += int(dataLen)
	}

	var tsUs int64
	if jr.frameIdx == 0 {
		tsUs = jr.baseTimeUs
	} else {
		tsUs = jr.lastTimeUs + int64(deltaUs)
	}
	jr.lastTimeUs = tsUs

	jr.entry = Entry{
		Timestamp: time.UnixMicro(tsUs),
		Header:    header,
		Data:      frameData,
	}
	jr.blockOff = off
	jr.frameIdx++
	return true
}

// readDeviceTable parses variable-length device table entries starting at the given offset.
//
// Entry format:
//
//	Source(1) + NAME(8) + ActiveFrom(4) + ProductCode(2) +
//	ModelIDLen(1) + ModelID + SWVersionLen(1) + SWVersion +
//	ModelVerLen(1) + ModelVersion + SerialLen(1) + Serial
func readDeviceTable(block []byte, offset int) ([]deviceTableEntry, error) {
	if offset+2 > len(block) {
		return nil, fmt.Errorf("device table offset out of range")
	}
	count := int(binary.LittleEndian.Uint16(block[offset:]))
	off := offset + 2

	entries := make([]deviceTableEntry, count)
	for i := range count {
		// Fixed part: Source(1) + NAME(8) + ActiveFrom(4) + ProductCode(2) = 15
		if off+15 > len(block) {
			return nil, fmt.Errorf("device table entry %d: fixed fields out of range", i)
		}
		entries[i].Source = block[off]
		entries[i].NAME = binary.LittleEndian.Uint64(block[off+1:])
		entries[i].ActiveFrom = binary.LittleEndian.Uint32(block[off+9:])
		entries[i].ProductCode = binary.LittleEndian.Uint16(block[off+13:])
		off += 15

		// Four length-prefixed strings.
		for _, dest := range []*string{
			&entries[i].ModelID,
			&entries[i].SoftwareVersion,
			&entries[i].ModelVersion,
			&entries[i].ModelSerial,
		} {
			s, n, err := ReadLenPrefixedString(block, off)
			if err != nil {
				return nil, fmt.Errorf("device table entry %d: %w", i, err)
			}
			*dest = s
			off += n
		}
	}
	return entries, nil
}
