package bench

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Config controls a run: where the workloads live, how many timed repetitions and
// warmup passes each engine gets, an optional name filter, and the unagi binary to
// bench. Warmup passes are discarded, so a cold cache or a first-run page fault
// does not land in the reported minimum.
type Config struct {
	WorkloadsDir string
	Reps         int
	Warmup       int
	Only         string
	UnagiPath    string
	Log          func(string) // optional progress sink, nil to stay quiet
}

// withDefaults fills the zero values a caller left unset.
func (c Config) withDefaults() Config {
	if c.Reps <= 0 {
		c.Reps = 5
	}
	if c.Warmup < 0 {
		c.Warmup = 0
	}
	if c.Log == nil {
		c.Log = func(string) {}
	}
	return c
}

// Run executes the full benchmark matrix and returns the collected results.
// CPython is required because it is the correctness oracle every other engine is
// checked against; a missing CPython is a hard error, while a missing PyPy or a
// workload that fails to compile under unagi is only a skip.
func Run(ctx context.Context, cfg Config) (Results, error) {
	cfg = cfg.withDefaults()

	workloads, err := Discover(cfg.WorkloadsDir)
	if err != nil {
		return Results{}, fmt.Errorf("discover workloads: %w", err)
	}
	if cfg.Only != "" {
		workloads = filterWorkloads(workloads, cfg.Only)
	}
	if len(workloads) == 0 {
		return Results{}, fmt.Errorf("no workloads found under %s", cfg.WorkloadsDir)
	}

	cpython := NewCPython()
	if !cpython.Available() {
		return Results{}, fmt.Errorf("cpython not found: it is the correctness oracle and is required")
	}
	engines := []Engine{cpython, NewPyPy(), NewUnagi(cfg.UnagiPath)}

	workdir, err := os.MkdirTemp("", "unagi-bench-")
	if err != nil {
		return Results{}, err
	}
	defer os.RemoveAll(workdir)

	res := Results{
		Schema:  SchemaVersion,
		Machine: thisMachine(),
		Reps:    cfg.Reps,
		Warmup:  cfg.Warmup,
		Engines: map[string]EngineInfo{},
	}
	for _, e := range engines {
		res.Engines[e.Name()] = engineInfo(ctx, e)
	}

	for _, w := range workloads {
		cfg.Log(fmt.Sprintf("benchmarking %s", w.Name))
		res.Workloads = append(res.Workloads, benchWorkload(ctx, cfg, engines, cpython, w, workdir))
	}
	return res, nil
}

// benchWorkload runs one workload under every available engine, taking CPython's
// output as the oracle and flagging any engine that disagrees.
func benchWorkload(ctx context.Context, cfg Config, engines []Engine, cpython *CPython, w Workload, workdir string) WorkloadResult {
	wr := WorkloadResult{Name: w.Name, Tag: w.Tag, Tier: w.Tier, Desc: w.Desc, Engines: map[string]Measure{}}

	// The oracle is CPython's output; capture it first so every engine is checked
	// against the reference rather than against each other.
	oracle, _, err := runOnce(ctx, []string{cpython.Bin(), w.Path})
	if err != nil {
		wr.Oracle = ""
	} else {
		wr.Oracle = strings.TrimRight(oracle, "\n")
	}

	for _, e := range engines {
		wr.Engines[e.Name()] = measure(ctx, cfg, e, w, workdir, wr.Oracle)
	}

	// Speedups are computed against CPython once every engine has a time.
	if base, ok := wr.Engines["cpython"]; ok && base.OK {
		for name, m := range wr.Engines {
			if name == "cpython" || !m.OK {
				continue
			}
			m.SpeedupVsCPython = round2(speedup(base.Stats.Median, m.Stats.Median))
			wr.Engines[name] = m
		}
	}
	return wr
}

// measure times one engine on one workload. An unavailable engine or a prepare
// failure is recorded as a skip, never fatal; a run whose output differs from the
// oracle is timed but flagged, because a fast wrong answer is not a win.
func measure(ctx context.Context, cfg Config, e Engine, w Workload, workdir, oracle string) Measure {
	m := Measure{Engine: e.Name()}
	if !e.Available() {
		m.Skip = "engine not available"
		return m
	}
	argv, cleanup, err := e.Prepare(ctx, w, workdir)
	if err != nil {
		m.Skip = err.Error()
		return m
	}
	defer cleanup()

	for i := 0; i < cfg.Warmup; i++ {
		if _, _, err := runOnce(ctx, argv); err != nil {
			m.Skip = fmt.Sprintf("warmup failed: %v", err)
			return m
		}
	}

	samples := make([]time.Duration, 0, cfg.Reps)
	var out string
	for i := 0; i < cfg.Reps; i++ {
		o, d, err := runOnce(ctx, argv)
		if err != nil {
			m.Skip = fmt.Sprintf("run failed: %v", err)
			return m
		}
		out = strings.TrimRight(o, "\n")
		samples = append(samples, d)
	}
	m.Stats = summarize(samples)
	m.Output = out
	m.OK = true
	m.Mismatch = oracle != "" && out != oracle
	return m
}

// runOnce runs an argv to completion, returning its stdout and wall-clock time.
// Timing brackets the whole process: an interpreter pays its startup on every run
// and a compiled binary pays its tiny startup too, which is the honest end-to-end
// cost a user sees.
func runOnce(ctx context.Context, argv []string) (string, time.Duration, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	return out.String(), dur, err
}

// engineInfo probes an engine's availability and version for the results header.
func engineInfo(ctx context.Context, e Engine) EngineInfo {
	info := EngineInfo{Available: e.Available()}
	if !info.Available {
		return info
	}
	switch v := e.(type) {
	case *CPython:
		info.Bin = v.bin
		info.Version = probeVersion(ctx, v.bin, "--version")
	case *PyPy:
		info.Bin = v.bin
		info.Version = probeVersion(ctx, v.bin, "--version")
	case *Unagi:
		info.Bin = v.bin
		info.Version = probeVersion(ctx, v.bin, "version")
	}
	return info
}

// probeVersion runs an engine's version command and returns its first output line.
func probeVersion(ctx context.Context, bin, arg string) string {
	cmd := exec.CommandContext(ctx, bin, arg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	line := string(out)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// filterWorkloads keeps the workloads whose name contains the filter substring.
func filterWorkloads(ws []Workload, only string) []Workload {
	var out []Workload
	for _, w := range ws {
		if strings.Contains(w.Name, only) {
			out = append(out, w)
		}
	}
	return out
}
