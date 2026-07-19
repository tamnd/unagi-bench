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
	discovered := len(workloads)
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
		Schema:     SchemaVersion,
		Machine:    thisMachine(),
		Reps:       cfg.Reps,
		Warmup:     cfg.Warmup,
		Discovered: discovered,
		Filter:     cfg.Only,
		Engines:    map[string]EngineInfo{},
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

	// The oracle is CPython's output on the untouched source, captured first so
	// every engine is checked against the reference rather than against each other.
	oracle, _, _, _, err := runOnce(ctx, []string{cpython.Bin(), w.Path})
	if err != nil {
		wr.Oracle = ""
	} else {
		wr.Oracle = strings.TrimRight(oracle, "\n")
	}

	for _, e := range engines {
		wr.Engines[e.Name()] = measure(ctx, cfg, e, w, workdir, wr.Oracle)
	}

	// Speedups and memory ratios are computed against CPython once every engine
	// has a time and a peak-RSS figure.
	if base, ok := wr.Engines["cpython"]; ok && base.OK {
		for name, m := range wr.Engines {
			if name == "cpython" || !m.OK {
				continue
			}
			m.SpeedupVsCPython = round2(speedup(base.Stats.Median, m.Stats.Median))
			m.MemRatioVsCPython = round2(leaner(base.Mem.Median, m.Mem.Median))
			if base.ComputeOK && m.ComputeOK {
				m.ComputeSpeedupVsCPython = round2(speedup(base.Compute.Median, m.Compute.Median))
			}
			wr.Engines[name] = m
		}
	}
	return wr
}

// measure times one engine on one workload. An unavailable engine or a prepare
// failure is recorded as a skip, never fatal; a run whose output differs from the
// oracle is timed but flagged, because a fast wrong answer is not a win.
//
// The workload is run through an instrumented copy of its source: a timer is
// injected around the module body so the program reports its own in-script
// compute time on stdout, which the harness reads back and strips before it
// compares output to the oracle. Wall time still brackets the whole process, so
// each engine yields both the end-to-end cost a user feels and the compute cost
// with the fixed interpreter or runtime startup taken out.
func measure(ctx context.Context, cfg Config, e Engine, w Workload, workdir, oracle string) Measure {
	m := Measure{Engine: e.Name()}
	if !e.Available() {
		m.Skip = "engine not available"
		return m
	}

	// Run an instrumented copy so the program self-times its body; if the copy
	// cannot be written, fall back to the untouched source (wall time only).
	runW := w
	if inst, cleanup, err := instrument(workdir, w); err == nil {
		runW = inst
		defer cleanup()
	}

	argv, cleanup, err := e.Prepare(ctx, runW, workdir)
	if err != nil {
		m.Skip = err.Error()
		return m
	}
	defer cleanup()

	for i := 0; i < cfg.Warmup; i++ {
		if _, _, _, _, err := runOnce(ctx, argv); err != nil {
			m.Skip = fmt.Sprintf("warmup failed: %v", err)
			return m
		}
	}

	samples := make([]time.Duration, 0, cfg.Reps)
	comp := make([]time.Duration, 0, cfg.Reps)
	rss := make([]int64, 0, cfg.Reps)
	var out string
	computeOK := true
	for i := 0; i < cfg.Reps; i++ {
		o, wall, compute, peak, err := runOnce(ctx, argv)
		if err != nil {
			m.Skip = fmt.Sprintf("run failed: %v", err)
			return m
		}
		out = strings.TrimRight(o, "\n")
		samples = append(samples, wall)
		rss = append(rss, peak)
		if compute > 0 {
			comp = append(comp, compute)
		} else {
			computeOK = false
		}
	}
	m.Stats = summarize(samples)
	m.Mem = summarizeMem(rss)
	if computeOK && len(comp) == cfg.Reps {
		m.Compute = summarize(comp)
		m.ComputeOK = true
	}
	m.Output = out
	m.OK = true
	m.Mismatch = oracle != "" && out != oracle
	return m
}

// runOnce runs an argv to completion, returning its stdout, wall-clock time, the
// in-script compute time the program self-reported (0 when the source was not
// instrumented), and peak resident set size in bytes. Wall timing brackets the
// whole process: an interpreter pays its startup on every run and a compiled
// binary pays its tiny startup too, which is the honest end-to-end cost a user
// sees. The compute time is parsed from the timer marker the instrumented source
// prints, which is stripped from the returned stdout so the caller compares clean
// output to the oracle. Peak RSS is read from the finished process's rusage, the
// true high-water mark the kernel observed rather than a sampled approximation.
func runOnce(ctx context.Context, argv []string) (string, time.Duration, time.Duration, int64, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	clean, compute := extractCompute(out.String())
	return clean, dur, compute, peakRSSBytes(cmd.ProcessState), err
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
