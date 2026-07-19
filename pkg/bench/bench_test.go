package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummarizeStats(t *testing.T) {
	got := summarize([]time.Duration{30, 10, 20, 40, 50})
	if got.Min != 10 {
		t.Fatalf("min = %d, want 10", got.Min)
	}
	if got.Median != 30 {
		t.Fatalf("median = %d, want 30", got.Median)
	}
	if got.Max != 50 {
		t.Fatalf("max = %d, want 50", got.Max)
	}
	if got.Runs != 5 {
		t.Fatalf("runs = %d, want 5", got.Runs)
	}
	// An even count averages the two central samples.
	even := summarize([]time.Duration{10, 20, 30, 40})
	if even.Median != 25 {
		t.Fatalf("even median = %d, want 25", even.Median)
	}
}

func TestSummarizeMem(t *testing.T) {
	got := summarizeMem([]int64{300, 100, 200, 400, 500})
	if got.Min != 100 || got.Median != 300 || got.Max != 500 || got.Runs != 5 {
		t.Fatalf("mem stats = %+v", got)
	}
	if summarizeMem(nil) != (memStats{}) {
		t.Fatal("no samples should summarize to the zero value")
	}
}

func TestLeaner(t *testing.T) {
	if got := leaner(200, 100); got != 2 {
		t.Fatalf("leaner(200,100) = %v, want 2", got)
	}
	if got := leaner(100, 0); got != 0 {
		t.Fatalf("leaner with a zero side = %v, want 0 (unsupported never prints a ratio)", got)
	}
}

func TestFmtBytes(t *testing.T) {
	cases := map[int64]string{0: "n/a", -1: "n/a", 512: "512B", 2048: "2KiB", 3 * 1024 * 1024: "3.0MiB"}
	for in, want := range cases {
		if got := fmtBytes(in); got != want {
			t.Fatalf("fmtBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestInstrumentRoundTrip checks the injected timer's marker is parsed back into a
// compute duration and stripped from stdout, so the remaining output still matches
// what the untouched program printed.
func TestInstrumentRoundTrip(t *testing.T) {
	wrapped := wrapSource("print(1)\nprint(2)\n")
	if !strings.Contains(wrapped, "perf_counter()") {
		t.Fatalf("wrapped source missing the timer:\n%s", wrapped)
	}
	// Simulate the instrumented program's stdout: the real output then the marker.
	stdout := "1\n2\n" + computeMarker + "0.012500000\n"
	clean, d := extractCompute(stdout)
	if clean != "1\n2\n" {
		t.Fatalf("clean stdout = %q, want the marker line stripped", clean)
	}
	if d != 12500*time.Microsecond {
		t.Fatalf("compute = %v, want 12.5ms", d)
	}
	// Uninstrumented output is returned unchanged with a zero duration.
	if c, z := extractCompute("plain\n"); c != "plain\n" || z != 0 {
		t.Fatalf("uninstrumented output changed: %q, %v", c, z)
	}
}

// TestWrapKeepsFutureImportFirst checks the prologue is inserted after a
// `from __future__` import, which Python requires to be the first statement.
func TestWrapKeepsFutureImportFirst(t *testing.T) {
	wrapped := wrapSource("from __future__ import annotations\nprint(1)\n")
	future := strings.Index(wrapped, "from __future__")
	prologue := strings.Index(wrapped, "import time as __ub_time")
	if future < 0 || prologue < 0 || future > prologue {
		t.Fatalf("future import must precede the timer prologue:\n%s", wrapped)
	}
}

func TestSpeedup(t *testing.T) {
	if got := speedup(100, 50); got != 2 {
		t.Fatalf("speedup(100,50) = %v, want 2", got)
	}
	// A non-positive side never yields a bogus ratio.
	if got := speedup(0, 50); got != 0 {
		t.Fatalf("speedup with zero base = %v, want 0", got)
	}
}

func TestHeaderField(t *testing.T) {
	k, v, ok := headerField("# tier: 1")
	if !ok || k != "tier" || v != "1" {
		t.Fatalf("headerField parsed (%q,%q,%v)", k, v, ok)
	}
	if _, _, ok := headerField("# unagi-bench workload"); ok {
		t.Fatal("a bare banner comment should not parse as a field")
	}
}

func TestDiscoverReadsHeaders(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "numeric")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "# unagi-bench workload\n# tier: 1\n# tag: numeric\n# desc: a loop\n\nprint(1)\n"
	if err := os.WriteFile(filepath.Join(sub, "loop.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("discovered %d workloads, want 1", len(ws))
	}
	w := ws[0]
	if w.Name != "numeric/loop" || w.Tier != 1 || w.Tag != "numeric" || w.Desc != "a loop" {
		t.Fatalf("workload metadata not parsed: %+v", w)
	}
}

func TestDiscoverDefaultsTagToDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "strings")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A workload with no header still loads, tagged by its parent directory.
	if err := os.WriteFile(filepath.Join(sub, "bare.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Tag != "strings" || ws[0].Tier != 0 {
		t.Fatalf("bare workload not defaulted: %+v", ws)
	}
}

// TestSummarizeGoalVerdict builds a synthetic run where unagi beats the fastest
// competitor by 3x on one workload and loses on another, and checks the geometric
// mean and the goal verdict, including that a wrong answer is never counted.
func TestSummarizeGoalVerdict(t *testing.T) {
	r := Results{
		Schema: SchemaVersion,
		Workloads: []WorkloadResult{
			{
				Name:   "win",
				Oracle: "42",
				Engines: map[string]Measure{
					"cpython": {Engine: "cpython", OK: true, Output: "42", Stats: stats{Median: 300 * time.Millisecond}},
					"pypy":    {Engine: "pypy", OK: true, Output: "42", Stats: stats{Median: 150 * time.Millisecond}},
					"unagi":   {Engine: "unagi", OK: true, Output: "42", Stats: stats{Median: 50 * time.Millisecond}},
				},
			},
			{
				Name:   "wrong",
				Oracle: "7",
				Engines: map[string]Measure{
					"cpython": {Engine: "cpython", OK: true, Output: "7", Stats: stats{Median: 100 * time.Millisecond}},
					"unagi":   {Engine: "unagi", OK: true, Output: "8", Mismatch: true, Stats: stats{Median: 10 * time.Millisecond}},
				},
			},
		},
	}
	s := Summarize(r)
	if s.Counted != 1 {
		t.Fatalf("counted = %d, want 1 (the wrong answer must not count)", s.Counted)
	}
	if s.Mismatches != 1 {
		t.Fatalf("mismatches = %d, want 1", s.Mismatches)
	}
	// unagi 50ms vs fastest competitor 150ms (pypy) is 3x, which clears the 2x goal.
	if s.VsBest != 3 || !s.GoalMet {
		t.Fatalf("vsBest = %v, goalMet = %v, want 3 and true", s.VsBest, s.GoalMet)
	}
}

// TestSummarizeComputeAndMemory checks the compute-time and peak-memory
// aggregates: unagi runs 2x faster in-script and holds half the peak RSS on the
// one counted workload, and the win-counts reflect it.
func TestSummarizeComputeAndMemory(t *testing.T) {
	r := Results{
		Schema: SchemaVersion,
		Workloads: []WorkloadResult{{
			Name:   "w",
			Oracle: "1",
			Engines: map[string]Measure{
				"cpython": {
					Engine: "cpython", OK: true, Output: "1",
					Stats:   stats{Median: 200 * time.Millisecond},
					Compute: stats{Median: 180 * time.Millisecond}, ComputeOK: true,
					Mem: memStats{Median: 20 << 20},
				},
				"unagi": {
					Engine: "unagi", OK: true, Output: "1",
					Stats:   stats{Median: 100 * time.Millisecond},
					Compute: stats{Median: 90 * time.Millisecond}, ComputeOK: true,
					Mem: memStats{Median: 10 << 20},
				},
			},
		}},
	}
	s := Summarize(r)
	if s.ComputeVsCPython != 2 || s.ComputeCounted != 1 || s.ComputeFasterCount != 1 {
		t.Fatalf("compute summary = %.2fx counted=%d faster=%d, want 2x/1/1", s.ComputeVsCPython, s.ComputeCounted, s.ComputeFasterCount)
	}
	if s.MemVsCPython != 2 || s.MemCounted != 1 || s.LeanerCount != 1 {
		t.Fatalf("memory summary = %.2fx counted=%d leaner=%d, want 2x/1/1", s.MemVsCPython, s.MemCounted, s.LeanerCount)
	}
}

func TestResultsRoundTrip(t *testing.T) {
	r := Results{
		Schema:  SchemaVersion,
		Machine: thisMachine(),
		Reps:    3,
		Engines: map[string]EngineInfo{"cpython": {Available: true, Version: "Python 3.14.6"}},
		Workloads: []WorkloadResult{{
			Name:    "x",
			Oracle:  "1",
			Engines: map[string]Measure{"cpython": {Engine: "cpython", OK: true, Output: "1", Stats: stats{Median: 5 * time.Millisecond}}},
		}},
	}
	var buf bytes.Buffer
	if err := Marshal(&buf, r); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reps != 3 || len(got.Workloads) != 1 || got.Workloads[0].Name != "x" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestParseRejectsNewerSchema(t *testing.T) {
	buf := bytes.NewBufferString(`{"schema": 999}`)
	if _, err := Parse(buf); err == nil {
		t.Fatal("a newer schema should be rejected, not silently misread")
	}
}

func TestRenderContainsVerdict(t *testing.T) {
	r := Results{
		Schema:  SchemaVersion,
		Machine: thisMachine(),
		Engines: map[string]EngineInfo{"cpython": {Available: true, Version: "Python 3.14.6"}},
		Workloads: []WorkloadResult{{
			Name:    "x",
			Oracle:  "1",
			Engines: map[string]Measure{"cpython": {Engine: "cpython", OK: true, Output: "1", Stats: stats{Median: time.Millisecond}}},
		}},
	}
	var buf bytes.Buffer
	Render(&buf, r)
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("goal (2x over fastest)")) {
		t.Fatalf("report should state the goal verdict:\n%s", out)
	}
}
