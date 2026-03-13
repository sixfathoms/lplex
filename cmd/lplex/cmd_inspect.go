package main

import (
	"fmt"
	"os"
	"time"

	"github.com/sixfathoms/lplex/journal"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <file.lpj>",
	Short: "Inspect journal file structure",
	Long:  "Display block-level details of an .lpj journal file: offsets, compression, frame counts, time spans.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runInspectFile(args[0])
	},
}

func compressionName(c journal.CompressionType) string {
	switch c {
	case journal.CompressionNone:
		return "none"
	case journal.CompressionZstd:
		return "zstd"
	case journal.CompressionZstdDict:
		return "zstd+dict"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}

func runInspectFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	reader, err := journal.NewReader(f)
	if err != nil {
		return err
	}

	compressed := reader.Compression() != journal.CompressionNone
	hasDict := reader.Compression() == journal.CompressionZstdDict

	// Header
	fmt.Printf("File: %s (%s)\n\n", path, formatBytes(uint64(fi.Size())))
	fmt.Printf("Header:\n")
	fmt.Printf("  Magic:       LPJ\n")
	fmt.Printf("  Version:     %d\n", reader.Version())
	fmt.Printf("  BlockSize:   %d\n", reader.BlockSize())
	fmt.Printf("  Compression: %s (%d)\n", compressionName(reader.Compression()), reader.Compression())
	fmt.Println()

	nBlocks := reader.BlockCount()
	if nBlocks == 0 {
		fmt.Printf("Blocks: 0\n")
		return nil
	}

	fmt.Printf("Blocks: %d\n\n", nBlocks)

	// Table header
	isV2 := reader.Version() == journal.Version2
	if compressed && hasDict {
		if isV2 {
			fmt.Printf("  %-4s  %10s  %12s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"#", "OFFSET", "BASE SEQ", "DICT", "COMPRESSED", "BLOCK SIZE", "RATIO", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %12s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"----", "----------", "------------", "----------", "----------", "----------", "------", "------", "-----", "----------------------------")
		} else {
			fmt.Printf("  %-4s  %10s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"#", "OFFSET", "DICT", "COMPRESSED", "BLOCK SIZE", "RATIO", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"----", "----------", "----------", "----------", "----------", "------", "------", "-----", "----------------------------")
		}
	} else if compressed {
		if isV2 {
			fmt.Printf("  %-4s  %10s  %12s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"#", "OFFSET", "BASE SEQ", "COMPRESSED", "BLOCK SIZE", "RATIO", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %12s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"----", "----------", "------------", "----------", "----------", "------", "------", "-----", "----------------------------")
		} else {
			fmt.Printf("  %-4s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"#", "OFFSET", "COMPRESSED", "BLOCK SIZE", "RATIO", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %10s  %10s  %6s  %6s  %5s  %s\n",
				"----", "----------", "----------", "----------", "------", "------", "-----", "----------------------------")
		}
	} else {
		if isV2 {
			fmt.Printf("  %-4s  %10s  %12s  %10s  %6s  %5s  %s\n",
				"#", "OFFSET", "BASE SEQ", "BLOCK SIZE", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %12s  %10s  %6s  %5s  %s\n",
				"----", "----------", "------------", "----------", "------", "-----", "----------------------------")
		} else {
			fmt.Printf("  %-4s  %10s  %10s  %6s  %5s  %s\n",
				"#", "OFFSET", "BLOCK SIZE", "FRAMES", "DEVS", "BASE TIME (UTC)")
			fmt.Printf("  %-4s  %10s  %10s  %6s  %5s  %s\n",
				"----", "----------", "----------", "------", "-----", "----------------------------")
		}
	}

	var totalCompressed int64
	var totalUncompressed int64
	var totalDictOverhead int64

	for i := range nBlocks {
		bi, err := reader.InspectBlock(i)
		if err != nil {
			fmt.Printf("  %-4d  error: %v\n", i, err)
			continue
		}

		tsStr := bi.BaseTime.UTC().Format("2006-01-02T15:04:05Z")

		if compressed && hasDict {
			ratio := float64(reader.BlockSize()) / float64(bi.CompressedLen+bi.DictLen)
			if isV2 {
				fmt.Printf("  %-4d  %10d  %12d  %10d  %10d  %10d  %5.1fx  %6d  %5d  %s\n",
					i, bi.Offset, bi.BaseSeq, bi.DictLen, bi.CompressedLen, reader.BlockSize(), ratio, bi.FrameCount, bi.DeviceCount, tsStr)
			} else {
				fmt.Printf("  %-4d  %10d  %10d  %10d  %10d  %5.1fx  %6d  %5d  %s\n",
					i, bi.Offset, bi.DictLen, bi.CompressedLen, reader.BlockSize(), ratio, bi.FrameCount, bi.DeviceCount, tsStr)
			}
			totalCompressed += int64(bi.CompressedLen) + int64(bi.DictLen) + int64(journal.BlockHeaderLenDict)
			totalDictOverhead += int64(bi.DictLen)
			totalUncompressed += int64(reader.BlockSize())
		} else if compressed {
			ratio := float64(reader.BlockSize()) / float64(bi.CompressedLen)
			if isV2 {
				fmt.Printf("  %-4d  %10d  %12d  %10d  %10d  %5.1fx  %6d  %5d  %s\n",
					i, bi.Offset, bi.BaseSeq, bi.CompressedLen, reader.BlockSize(), ratio, bi.FrameCount, bi.DeviceCount, tsStr)
			} else {
				fmt.Printf("  %-4d  %10d  %10d  %10d  %5.1fx  %6d  %5d  %s\n",
					i, bi.Offset, bi.CompressedLen, reader.BlockSize(), ratio, bi.FrameCount, bi.DeviceCount, tsStr)
			}
			totalCompressed += int64(bi.CompressedLen) + int64(journal.BlockHeaderLen)
			totalUncompressed += int64(reader.BlockSize())
		} else {
			if isV2 {
				fmt.Printf("  %-4d  %10d  %12d  %10d  %6d  %5d  %s\n",
					i, bi.Offset, bi.BaseSeq, reader.BlockSize(), bi.FrameCount, bi.DeviceCount, tsStr)
			} else {
				fmt.Printf("  %-4d  %10d  %10d  %6d  %5d  %s\n",
					i, bi.Offset, reader.BlockSize(), bi.FrameCount, bi.DeviceCount, tsStr)
			}
			totalUncompressed += int64(reader.BlockSize())
		}
	}

	fmt.Println()

	// Summary
	if compressed {
		ratio := float64(totalUncompressed) / float64(totalCompressed)
		fmt.Printf("Totals:\n")
		fmt.Printf("  Uncompressed: %s (%d bytes)\n", formatBytes(uint64(totalUncompressed)), totalUncompressed)
		fmt.Printf("  Compressed:   %s (%d bytes, including block headers)\n", formatBytes(uint64(totalCompressed)), totalCompressed)
		if totalDictOverhead > 0 {
			fmt.Printf("  Dict overhead: %s (%d bytes)\n", formatBytes(uint64(totalDictOverhead)), totalDictOverhead)
		}
		fmt.Printf("  Ratio:        %.1fx\n", ratio)
	}

	// Footer
	if compressed {
		hasIndex := reader.HasBlockIndex()
		fmt.Println()
		fmt.Printf("Footer:\n")
		if hasIndex {
			indexSize := nBlocks*8 + 8
			fmt.Printf("  Block Index: present (LPJI magic)\n")
			fmt.Printf("  Entries:     %d\n", nBlocks)
			fmt.Printf("  Index Size:  %d bytes\n", indexSize)
		} else {
			fmt.Printf("  Block Index: missing (recovered via forward scan)\n")
		}
	}

	// Time span + seq range
	first, err := reader.InspectBlock(0)
	if err == nil {
		last, err := reader.InspectBlock(nBlocks - 1)
		if err == nil {
			duration := last.BaseTime.Sub(first.BaseTime)
			fmt.Println()
			fmt.Printf("Time Span:\n")
			fmt.Printf("  First: %s\n", first.BaseTime.UTC().Format(time.RFC3339))
			fmt.Printf("  Last:  %s\n", last.BaseTime.UTC().Format(time.RFC3339))
			fmt.Printf("  Span:  %s\n", duration.Truncate(time.Second))

			if isV2 && first.BaseSeq > 0 {
				lastSeq := last.BaseSeq + uint64(last.FrameCount) - 1
				fmt.Println()
				fmt.Printf("Sequence Range:\n")
				fmt.Printf("  First: %d\n", first.BaseSeq)
				fmt.Printf("  Last:  %d\n", lastSeq)
				fmt.Printf("  Total: %d frames\n", lastSeq-first.BaseSeq+1)
			}
		}
	}

	return nil
}
