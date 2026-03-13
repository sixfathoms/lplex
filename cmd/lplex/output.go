package main

import (
	"fmt"
	"os"
)

// ANSI escape codes.
const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
	ansiHiGreen = "\033[92m"
	ansiHiYell  = "\033[93m"
	ansiHiBlue  = "\033[94m"
	ansiHiMag   = "\033[95m"
	ansiHiCyan  = "\033[96m"
	ansiRed     = "\033[31m"
)

var srcPalette = []string{
	ansiGreen, ansiYellow, ansiBlue, ansiMagenta, ansiCyan,
	ansiHiGreen, ansiHiYell, ansiHiBlue, ansiHiMag, ansiHiCyan,
}

func colorForSrc(src uint8) string {
	return srcPalette[int(src)%len(srcPalette)]
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
