package bench

import (
	"fmt"
	"io"
	"math"
	"sort"
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
	VsCPython    float64 // geomean of unagi median speedup over cpython
	VsBest       float64 // geomean of unagi median speedup over the fastest competitor
	GoalMet      bool    // VsBest >= Goal
	Counted      int     // workloads that contributed (unagi ran and matched the oracle)
	Mismatches   int     // workloads where an engine disagreed with the oracle
	UnagiSkipped int     // workloads unagi could not build or run
}

// Summarize reduces a run to its headline figures.
func Summarize(r Results) Summary {
	var s Summary
	var prodCPy, prodBest float64 = 1, 1
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
	}
	if s.Counted > 0 {
		s.VsCPython = round2(math.Pow(prodCPy, 1/float64(s.Counted)))
		s.VsBest = round2(math.Pow(prodBest, 1/float64(s.Counted)))
		s.GoalMet = s.VsBest >= Goal
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
				status := ""
				if m.Mismatch {
					status = "  MISMATCH vs oracle"
				}
				sp := ""
				if name != "cpython" && m.SpeedupVsCPython > 0 {
					sp = fmt.Sprintf("  %.2fx vs cpython", m.SpeedupVsCPython)
				}
				fpf(w, "    %-8s %10s%s%s\n", name, fmtDur(m.Stats.Median), sp, status)
			}
		}
		fpf(w, "\n")
	}
}

func renderSummary(w io.Writer, r Results) {
	s := Summarize(r)
	fpf(w, "summary (geometric mean over %d workloads unagi ran correctly)\n", s.Counted)
	fpf(w, "  unagi vs cpython:            %.2fx\n", s.VsCPython)
	fpf(w, "  unagi vs fastest competitor: %.2fx\n", s.VsBest)
	verdict := "not yet met"
	if s.GoalMet {
		verdict = "MET"
	}
	fpf(w, "  goal (%.0fx over fastest):     %s\n", Goal, verdict)
	if s.Mismatches > 0 {
		fpf(w, "  output mismatches:           %d (investigate before trusting any speedup)\n", s.Mismatches)
	}
	if s.UnagiSkipped > 0 {
		fpf(w, "  workloads unagi skipped:     %d\n", s.UnagiSkipped)
	}
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
