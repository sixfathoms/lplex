package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sixfathoms/lplex/journal"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <file.lpj|directory>",
	Short: "Verify journal file integrity",
	Long: `Walk one or more .lpj journal files checking CRC32C checksums,
block continuity, and sequence gaps. Reports errors per file and
exits with code 1 if any issues are found.

When given a directory, all .lpj files in it are verified in
chronological order and cross-file sequence continuity is checked.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runVerify(args[0])
	},
}

type verifyResult struct {
	Path       string
	Blocks     int
	Frames     int
	Errors     []string
	FirstSeq   uint64
	LastSeq    uint64
}

func runVerify(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}

	var files []string
	if fi.IsDir() {
		matches, err := filepath.Glob(filepath.Join(path, "*.lpj"))
		if err != nil {
			return err
		}
		sort.Strings(matches)
		files = matches
	} else {
		files = []string{path}
	}

	if len(files) == 0 {
		fmt.Println("no .lpj files found")
		return nil
	}

	var results []verifyResult
	totalErrors := 0

	for _, f := range files {
		result := verifyFile(f)
		results = append(results, result)
		totalErrors += len(result.Errors)
	}

	// Check cross-file sequence continuity
	var crossFileErrors []string
	for i := 1; i < len(results); i++ {
		prev := results[i-1]
		cur := results[i]
		if prev.LastSeq > 0 && cur.FirstSeq > 0 && cur.FirstSeq != prev.LastSeq+1 {
			gap := cur.FirstSeq - prev.LastSeq - 1
			msg := fmt.Sprintf("sequence gap between %s and %s: expected %d, got %d (%d missing)",
				filepath.Base(prev.Path), filepath.Base(cur.Path),
				prev.LastSeq+1, cur.FirstSeq, gap)
			crossFileErrors = append(crossFileErrors, msg)
		}
	}

	// Print results
	for _, r := range results {
		status := "OK"
		if len(r.Errors) > 0 {
			status = fmt.Sprintf("FAIL (%d errors)", len(r.Errors))
		}
		seqRange := ""
		if r.FirstSeq > 0 {
			seqRange = fmt.Sprintf(" seq %d..%d", r.FirstSeq, r.LastSeq)
		}
		fmt.Printf("%-50s %d blocks, %d frames%s  %s\n",
			filepath.Base(r.Path), r.Blocks, r.Frames, seqRange, status)
		for _, e := range r.Errors {
			fmt.Printf("  ERROR: %s\n", e)
		}
	}

	if len(crossFileErrors) > 0 {
		fmt.Println()
		for _, e := range crossFileErrors {
			fmt.Printf("CROSS-FILE: %s\n", e)
		}
		totalErrors += len(crossFileErrors)
	}

	fmt.Println()
	totalFiles := len(results)
	totalBlocks := 0
	totalFrames := 0
	for _, r := range results {
		totalBlocks += r.Blocks
		totalFrames += r.Frames
	}
	fmt.Printf("%d files, %d blocks, %d frames verified", totalFiles, totalBlocks, totalFrames)
	if totalErrors > 0 {
		fmt.Printf(", %d errors\n", totalErrors)
		return fmt.Errorf("%d integrity errors found", totalErrors)
	}
	fmt.Println(", all OK")
	return nil
}

func verifyFile(path string) verifyResult {
	result := verifyResult{Path: path}

	f, err := os.Open(path)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("open: %v", err))
		return result
	}
	defer func() { _ = f.Close() }()

	reader, err := journal.NewReader(f)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("header: %v", err))
		return result
	}
	defer reader.Close()

	result.Blocks = reader.BlockCount()

	var lastSeq uint64

	for reader.Next() {
		seq := reader.FrameSeq()
		result.Frames++

		if seq > 0 {
			// Track first/last seq
			if result.FirstSeq == 0 {
				result.FirstSeq = seq
			}
			result.LastSeq = seq

			// Check sequence continuity within the file
			if lastSeq > 0 && seq != lastSeq+1 {
				gap := seq - lastSeq - 1
				result.Errors = append(result.Errors,
					fmt.Sprintf("sequence gap at frame %d: expected %d, got %d (%d missing)",
						result.Frames, lastSeq+1, seq, gap))
			}
			lastSeq = seq
		}
	}

	if err := reader.Err(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("read: %v", err))
	}

	// Verify block count matches what we iterated
	if reader.BlockCount() > 0 && result.Frames == 0 && reader.Version() == journal.Version2 {
		result.Errors = append(result.Errors, "file has blocks but no readable frames")
	}

	return result
}
