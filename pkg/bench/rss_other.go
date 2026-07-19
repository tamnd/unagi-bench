//go:build !unix

package bench

import "os"

// peakRSSBytes is the fallback on platforms without a rusage-style peak RSS
// (notably Windows). It reports 0, which the report renders as "n/a" so a memory
// column never prints a fabricated figure where the kernel gave none.
func peakRSSBytes(_ *os.ProcessState) int64 { return 0 }
