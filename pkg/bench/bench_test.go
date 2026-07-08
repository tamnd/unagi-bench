package bench

import (
	"bytes"
	"os"
	"path/filepath"
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
