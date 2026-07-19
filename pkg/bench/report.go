package bench

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// fpf writes to w and discards the error, the way a report renderer that only
// targets an in-memory buffer or stdout does; a write failure there is not
// actionable and would only clutter every call site.
func fpf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// Summary is the run's headline: unagi's aggregate speedup against CPython and
// against the fastest competitor on each workload, and whether the fastest-
// competitor figure clears the 2x goal. The aggregate is a geometric mean, the
// right average for ratios, taken only over workloads unagi ran correctly.
type Summary struct {
	VsCPython    float64 // geomean of unagi median wall speedup over cpython
	VsBest       float64 // geomean of unagi median wall speedup over the fastest competitor
	GoalMet      bool    // VsBest >= Goal
	Counted      int     // workloads that contributed (unagi ran and matched the oracle)
	FasterCount  int     // workloads where unagi's median wall time beat cpython
	Mismatches   int     // workloads where an engine disagreed with the oracle
	UnagiSkipped int     // workloads unagi could not build or run

	ComputeVsCPython   float64 // geomean of unagi median compute speedup over cpython
	ComputeCounted     int     // workloads with a compute-time pair for both engines
	ComputeFasterCount int     // workloads where unagi's median compute time beat cpython

	MemVsCPython float64 // geomean of unagi peak-RSS advantage over cpython (>1 is leaner)
	MemCounted   int     // workloads with a usable peak-RSS pair for both engines
	LeanerCount  int     // workloads where unagi held less peak RSS than cpython
}

// Summarize reduces a run to its headline figures across both time and memory.
// Time and memory are counted independently: a platform that cannot report peak
// RSS still yields a full timing summary, and MemCounted stays 0 so the report
// omits the memory headline rather than printing a hollow ratio.
func Summarize(r Results) Summary {
	var s Summary
	var prodCPy, prodBest, prodMem, prodCompute float64 = 1, 1, 1, 1
	for _, wr := range r.Workloads {
		u, ok := wr.Engines["unagi"]
		if !ok || !u.OK {
			s.UnagiSkipped++
			continue
		}
		if anyMismatch(wr) {
			s.Mismatches++
		}
		if u.Mismatch {
			// A wrong answer is never counted as a win.
			continue
		}
		cpy, hasCpy := wr.Engines["cpython"]
		if !hasCpy || !cpy.OK {
			continue
		}
		best := cpy.Stats.Median
		if pp, ok := wr.Engines["pypy"]; ok && pp.OK && !pp.Mismatch && pp.Stats.Median < best {
			best = pp.Stats.Median
		}
		vsCpy := speedup(cpy.Stats.Median, u.Stats.Median)
		vsBest := speedup(best, u.Stats.Median)
		if vsCpy <= 0 || vsBest <= 0 {
			continue
		}
		prodCPy *= vsCpy
		prodBest *= vsBest
		s.Counted++
		if vsCpy > 1 {
			s.FasterCount++
		}
		// Compute time is scored only where both engines self-reported a reading.
		if cpy.ComputeOK && u.ComputeOK {
			if c := speedup(cpy.Compute.Median, u.Compute.Median); c > 0 {
				prodCompute *= c
				s.ComputeCounted++
				if c > 1 {
					s.ComputeFasterCount++
				}
			}
		}
		// Memory is scored only where both engines gave a real peak-RSS figure.
		if mem := leaner(cpy.Mem.Median, u.Mem.Median); mem > 0 {
			prodMem *= mem
			s.MemCounted++
			if mem > 1 {
				s.LeanerCount++
			}
		}
	}
	if s.Counted > 0 {
		s.VsCPython = round2(math.Pow(prodCPy, 1/float64(s.Counted)))
		s.VsBest = round2(math.Pow(prodBest, 1/float64(s.Counted)))
		s.GoalMet = s.VsBest >= Goal
	}
	if s.ComputeCounted > 0 {
		s.ComputeVsCPython = round2(math.Pow(prodCompute, 1/float64(s.ComputeCounted)))
	}
	if s.MemCounted > 0 {
		s.MemVsCPython = round2(math.Pow(prodMem, 1/float64(s.MemCounted)))
	}
	return s
}

// anyMismatch reports whether any engine disagreed with the oracle on a workload.
func anyMismatch(wr WorkloadResult) bool {
	for _, m := range wr.Engines {
		if m.OK && m.Mismatch {
			return true
		}
	}
	return false
}

// Render writes the full human report: the machine and engine header, a per-
// workload table of medians and speedups, and the headline summary with the goal
// verdict.
func Render(w io.Writer, r Results) {
	renderHeader(w, r)
	renderTable(w, r)
	renderSummary(w, r)
}

func renderHeader(w io.Writer, r Results) {
	fpf(w, "unagi-bench\n")
	fpf(w, "machine: %s/%s, %s\n", r.Machine.OS, r.Machine.Arch, r.Machine.GoVersion)
	fpf(w, "reps: %d, warmup: %d\n", r.Reps, r.Warmup)
	fpf(w, "scope: %s\n", scopeLine(r))
	for _, name := range engineOrder(r) {
		info := r.Engines[name]
		if !info.Available {
			fpf(w, "  %-8s not found\n", name)
			continue
		}
		v := info.Version
		if v == "" {
			v = info.Bin
		}
		fpf(w, "  %-8s %s\n", name, v)
	}
	fpf(w, "\n")
}

// renderTable prints one block per workload: each engine's median time, its
// speedup over CPython, and a status flag for a skip or an output mismatch.
func renderTable(w io.Writer, r Results) {
	for _, wr := range r.Workloads {
		tier := ""
		if wr.Tier > 0 {
			tier = fmt.Sprintf("  tier %d", wr.Tier)
		}
		fpf(w, "%s%s\n", wr.Name, tier)
		if wr.Desc != "" {
			fpf(w, "  %s\n", wr.Desc)
		}
		for _, name := range engineOrder(r) {
			m, ok := wr.Engines[name]
			if !ok {
				continue
			}
			switch {
			case m.Skip != "":
				fpf(w, "    %-8s skipped: %s\n", name, m.Skip)
			case !m.OK:
				fpf(w, "    %-8s no result\n", name)
			default:
				compute := "compute n/a"
				if m.ComputeOK {
					compute = "compute " + fmtDur(m.Compute.Median)
				}
				fpf(w, "    %-8s wall %10s  %-18s  mem %9s%s\n",
					name, fmtDur(m.Stats.Median), compute, fmtBytes(m.Mem.Median), engineRatios(name, m))
			}
		}
		fpf(w, "\n")
	}
}

// engineRatios formats an engine's advantage over CPython for the table row: the
// wall speedup, the compute speedup when both were measured, the memory ratio, and
// a mismatch flag. CPython is the baseline, so its own row carries no ratios.
func engineRatios(name string, m Measure) string {
	if m.Mismatch {
		return "  MISMATCH vs oracle"
	}
	if name == "cpython" {
		return "  (baseline)"
	}
	var b strings.Builder
	if m.SpeedupVsCPython > 0 {
		fmt.Fprintf(&b, "  %.2fx wall", m.SpeedupVsCPython)
	}
	if m.ComputeSpeedupVsCPython > 0 {
		fmt.Fprintf(&b, "  %.2fx compute", m.ComputeSpeedupVsCPython)
	}
	if m.MemRatioVsCPython > 0 {
		fmt.Fprintf(&b, "  %.2fx mem", m.MemRatioVsCPython)
	}
	return b.String()
}

func renderSummary(w io.Writer, r Results) {
	s := Summarize(r)
	scope := "full suite"
	if r.Partial() {
		scope = "partial run"
	}
	fpf(w, "summary — %s, geometric mean over %d workloads unagi ran correctly\n", scope, s.Counted)
	fpf(w, "  (ratios are cpython over unagi: >1 means unagi wins)\n")
	fpf(w, "  wall    vs cpython:            %.2fx  (faster on %d/%d)\n", s.VsCPython, s.FasterCount, s.Counted)
	fpf(w, "  wall    vs fastest competitor: %.2fx\n", s.VsBest)
	if s.ComputeCounted > 0 {
		fpf(w, "  compute vs cpython:            %.2fx  (faster on %d/%d)\n", s.ComputeVsCPython, s.ComputeFasterCount, s.ComputeCounted)
	} else {
		fpf(w, "  compute vs cpython:            n/a (no in-script timer readings)\n")
	}
	if s.MemCounted > 0 {
		fpf(w, "  memory  vs cpython:            %.2fx  (leaner on %d/%d)\n", s.MemVsCPython, s.LeanerCount, s.MemCounted)
	} else {
		fpf(w, "  memory  vs cpython:            n/a (peak RSS unavailable on this platform)\n")
	}
	verdict := "not yet met"
	if s.GoalMet {
		verdict = "MET"
	}
	fpf(w, "  goal (%.0fx over fastest):       %s\n", Goal, verdict)
	if s.Mismatches > 0 {
		fpf(w, "  output mismatches:             %d (investigate before trusting any speedup)\n", s.Mismatches)
	}
	if s.UnagiSkipped > 0 {
		fpf(w, "  workloads unagi skipped:       %d\n", s.UnagiSkipped)
	}
}

// scopeLine describes how much of the suite this run covered, so a partial run's
// figures are never read as the whole-suite result.
func scopeLine(r Results) string {
	ran := len(r.Workloads)
	if r.Discovered > 0 && r.Filter != "" {
		return fmt.Sprintf("%d of %d workloads (filter %q)", ran, r.Discovered, r.Filter)
	}
	if r.Discovered > 0 && ran < r.Discovered {
		return fmt.Sprintf("%d of %d workloads (partial)", ran, r.Discovered)
	}
	if r.Discovered > 0 {
		return fmt.Sprintf("all %d workloads (full suite)", ran)
	}
	return fmt.Sprintf("%d workloads", ran)
}

// engineOrder returns the engine names in a stable display order: cpython first as
// the reference, then pypy, then unagi, then any others alphabetically.
func engineOrder(r Results) []string {
	prio := map[string]int{"cpython": 0, "pypy": 1, "unagi": 2}
	names := make([]string, 0, len(r.Engines))
	for n := range r.Engines {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		pi, oki := prio[names[i]]
		pj, okj := prio[names[j]]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki
		}
		return names[i] < names[j]
	})
	return names
}

// fmtBytes renders a byte count in a compact binary-unit form, or "n/a" when the
// platform could not report a peak RSS (a zero sample).
func fmtBytes(b int64) string {
	if b <= 0 {
		return "n/a"
	}
	const unit = 1024
	switch {
	case b >= unit*unit*unit:
		return fmt.Sprintf("%.2fGiB", float64(b)/(unit*unit*unit))
	case b >= unit*unit:
		return fmt.Sprintf("%.1fMiB", float64(b)/(unit*unit))
	case b >= unit:
		return fmt.Sprintf("%.0fKiB", float64(b)/unit)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// fmtDur renders a duration in a compact, aligned form.
func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.3fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.0fus", float64(d)/float64(time.Microsecond))
	}
}
