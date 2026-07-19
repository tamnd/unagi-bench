package bench

import (
	"math"
	"sort"
	"time"
)

// stats summarizes a set of timing samples. The report leans on the minimum and
// the median rather than the mean: process benchmarks are noise-above, never
// noise-below, so the minimum is the closest estimate of the true cost and the
// median is the robust central value. The mean is kept only to spot a run with a
// long tail.
type stats struct {
	Min    time.Duration `json:"min"`
	Median time.Duration `json:"median"`
	Mean   time.Duration `json:"mean"`
	Max    time.Duration `json:"max"`
	Runs   int           `json:"runs"`
}

// summarize reduces raw samples to the reported statistics. It sorts a copy, so
// the caller's slice is untouched.
func summarize(samples []time.Duration) stats {
	n := len(samples)
	if n == 0 {
		return stats{}
	}
	sorted := make([]time.Duration, n)
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	return stats{
		Min:    sorted[0],
		Median: median(sorted),
		Mean:   total / time.Duration(n),
		Max:    sorted[n-1],
		Runs:   n,
	}
}

// median returns the middle value of an already-sorted slice, averaging the two
// central samples for an even count.
func median(sorted []time.Duration) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// memStats summarizes a set of peak-RSS samples in bytes. Peak memory is far
// steadier across repetitions than wall time, but the same minimum-and-median
// pairing is kept: the minimum is the cleanest cost estimate and the median the
// robust central value, with the maximum retained to spot a run that ballooned.
type memStats struct {
	Min    int64 `json:"min"`    // bytes
	Median int64 `json:"median"` // bytes
	Max    int64 `json:"max"`    // bytes
	Runs   int   `json:"runs"`
}

// summarizeMem reduces raw peak-RSS samples to the reported memory statistics. A
// sample of 0 means the platform could not report peak RSS; when every sample is
// 0 the summary stays zero and the report shows memory as unavailable rather than
// as a real 0-byte figure.
func summarizeMem(samples []int64) memStats {
	n := len(samples)
	if n == 0 {
		return memStats{}
	}
	sorted := make([]int64, n)
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return memStats{
		Min:    sorted[0],
		Median: median64(sorted),
		Max:    sorted[n-1],
		Runs:   n,
	}
}

// median64 returns the middle value of an already-sorted int64 slice, averaging
// the two central samples for an even count.
func median64(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// leaner is base over ours for memory: how many times less peak RAM we used than
// the baseline. A value of 2 means we held half the baseline's peak. It returns 0
// when either side is non-positive, so a missing or unsupported measurement never
// prints a bogus ratio.
func leaner(base, ours int64) float64 {
	if base <= 0 || ours <= 0 {
		return 0
	}
	return float64(base) / float64(ours)
}

// speedup is base over ours: how many times faster we are than the baseline. A
// value of 2 means we ran in half the baseline's time. It returns 0 when either
// side is non-positive, so a missing measurement never prints a bogus ratio.
func speedup(base, ours time.Duration) float64 {
	if base <= 0 || ours <= 0 {
		return 0
	}
	return float64(base) / float64(ours)
}

// round2 trims a ratio to two decimals for stable, readable output.
func round2(v float64) float64 { return math.Round(v*100) / 100 }
