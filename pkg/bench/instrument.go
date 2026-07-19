package bench

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// computeMarker is the sentinel the injected timer prints on its own final line
// of stdout: computeMarker followed by the in-script elapsed time in seconds. It
// is deliberately unmistakable so no workload's real output collides with it, and
// the harness strips the line before comparing output to the oracle.
const computeMarker = "__UBCOMPUTE__="

// instrument writes an instrumented copy of a workload's source next to it in the
// workdir and returns a Workload pointing at the copy plus a cleanup. The copy is
// the original source with a monotonic timer started before the module body and
// read after it, printing the elapsed compute time on stdout under computeMarker.
// Wrapping this way is engine-agnostic: an interpreter runs the copy directly and
// unagi compiles it, so both report the same in-script figure through the same
// channel (unagi does not yet expose sys.stderr, so stdout carries the marker).
//
// A `from __future__` import must stay the first statement, so the timer prologue
// is inserted after any leading future imports, blank lines, and comments rather
// than blindly at the top.
func instrument(workdir string, w Workload) (Workload, func(), error) {
	src, err := os.ReadFile(w.Path)
	if err != nil {
		return w, func() {}, err
	}
	wrapped := wrapSource(string(src))

	path := filepath.Join(workdir, sanitize(w.Name)+"__inst.py")
	if err := os.WriteFile(path, []byte(wrapped), 0o644); err != nil {
		return w, func() {}, err
	}
	iw := w
	iw.Path = path
	return iw, func() { _ = os.Remove(path) }, nil
}

// wrapSource returns the source with the compute timer injected. The prologue
// imports a private timer alias and stamps a start; the epilogue prints the
// elapsed seconds under the marker. Private, double-underscored names keep the
// injection from colliding with the workload's own identifiers.
func wrapSource(src string) string {
	const prologue = "import time as __ub_time\n__ub_t0 = __ub_time.perf_counter()\n"
	const epilogue = "\nprint(\"" + computeMarker + "%.9f\" % (__ub_time.perf_counter() - __ub_t0))\n"

	lines := strings.SplitAfter(src, "\n")
	at := prologueInsertIndex(lines)
	var b strings.Builder
	for i, ln := range lines {
		if i == at {
			b.WriteString(prologue)
		}
		b.WriteString(ln)
	}
	if at >= len(lines) {
		b.WriteString(prologue)
	}
	b.WriteString(epilogue)
	return b.String()
}

// prologueInsertIndex returns the line index at which the timer prologue is safe
// to insert: past a leading run of blank lines, comments, and `from __future__`
// imports, which Python requires to precede any other statement.
func prologueInsertIndex(lines []string) int {
	at := 0
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "from __future__") {
			at = i + 1
			continue
		}
		return i
	}
	return at
}

// extractCompute pulls the injected timer's reading out of a program's stdout and
// returns the stdout with that marker line removed and the compute time it
// reported. Uninstrumented output has no marker, so it comes back unchanged with a
// zero duration. The marker is the last line the instrumented program prints, but
// the last matching line is used regardless of position to stay robust.
func extractCompute(out string) (string, time.Duration) {
	parts := strings.SplitAfter(out, "\n")
	idx := -1
	for i, p := range parts {
		if strings.HasPrefix(strings.TrimRight(p, "\n"), computeMarker) {
			idx = i
		}
	}
	if idx < 0 {
		return out, 0
	}
	line := strings.TrimRight(parts[idx], "\n")
	secs, err := strconv.ParseFloat(strings.TrimPrefix(line, computeMarker), 64)
	var d time.Duration
	if err == nil && secs > 0 {
		d = time.Duration(secs * float64(time.Second))
	}
	parts = append(parts[:idx], parts[idx+1:]...)
	return strings.Join(parts, ""), d
}
