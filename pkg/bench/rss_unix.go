//go:build unix

package bench

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSSBytes reads a finished process's peak resident set size in bytes from
// its rusage. The kernel reports the high-water mark of physical memory the
// process ever held, which is the honest "how much RAM did this cost" figure a
// user cares about, and it survives the process exit in ProcessState. The unit
// of ru_maxrss is not portable: Linux and the BSDs report kilobytes, macOS
// reports bytes, so the value is normalized here and every caller sees bytes.
func peakRSSBytes(ps *os.ProcessState) int64 {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	max := int64(ru.Maxrss)
	if max < 0 {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return max // already bytes
	}
	return max * 1024 // kilobytes elsewhere
}
