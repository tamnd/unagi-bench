# unagi-bench

A benchmark harness that times the [unagi](https://github.com/tamnd/unagi)
Python-to-Go compiler against CPython 3.14 and, when it is installed, PyPy.

The point is an honest picture: how much faster and leaner is a unagi-compiled
binary than the interpreter it replaces, on the same program, producing the same
output. The campaign goal is 2x over the fastest competitor on the provable
subset, and this harness is the instrument that tracks progress toward it.

Every run compares three metrics against CPython, per workload and in aggregate:

- **wall time** — the whole process from exec to exit, the end-to-end cost a user
  feels, including interpreter or binary startup.
- **compute time** — the in-script cost. Each workload is run through an
  instrumented copy that starts a monotonic timer before the module body and
  reads it after, so the figure isolates the work the program did from the fixed
  per-process startup. It is measured the same way for every engine.
- **peak memory** — the high-water resident set size the kernel observed for the
  process, so "same data for less RAM" shows up as a ratio, not a claim.

Ratios are reported as CPython-over-unagi: a value above 1 means unagi wins.

## How it works

For every workload under `workloads/`, unagi-bench:

1. builds the workload to a native binary with `unagi build`,
2. runs it under every engine it can find (CPython, optionally PyPy, unagi),
3. takes CPython's output as the oracle and flags any engine that disagrees,
4. times each engine over a few repetitions after a warmup, capturing wall time,
   in-script compute time, and peak memory, and
5. reports the median of each metric and the ratio against CPython and against the
   fastest competitor.

Wall timing brackets the whole process, so an interpreter pays its startup on
every run and the compiled binary pays its small startup too; that is the
end-to-end cost a user actually sees. Compute time comes from a timer injected
around the workload's body, printed on stdout under a sentinel the harness reads
back and strips before it checks output, so it works uniformly for an interpreter
and for a unagi binary. Peak memory is read from the finished process's rusage. A
workload whose output does not match CPython is timed but marked `MISMATCH`, and
it never counts toward a win, because a fast wrong answer is not a win.

The report labels its scope, so a filtered or partial run is never read as the
whole-suite figure.

## Usage

```
go build -o unagi-bench ./cmd/unagi-bench

# run the matrix, pointing at a unagi binary (or leave --unagi off to use PATH)
./unagi-bench run --unagi /path/to/unagi

# store the run and render it later
./unagi-bench run --unagi /path/to/unagi --json results.json
./unagi-bench report --file results.json

# a single workload, more repetitions
./unagi-bench run --only numeric/float_sumsq --reps 11
```

Flags:

- `--workloads` directory of `.py` workloads (default `workloads`)
- `--reps` timed repetitions per engine (default 5)
- `--warmup` discarded warmup passes per engine (default 1)
- `--only` run only workloads whose name contains this substring
- `--unagi` path to the unagi binary (defaults to `unagi` on PATH)
- `--json` write a `results.json` for later `report` or diffing
- `--gate` exit non-zero when the 2x goal is not met

CPython is required because it is the correctness oracle. PyPy is optional: a
machine without it still benches CPython against unagi, and the PyPy column is
omitted.

## Workloads

Each workload is a small, self-contained Python program that prints a
deterministic result, so the same output across engines proves they did the same
work. A header comment carries its metadata:

```python
# unagi-bench workload
# tier: 1
# tag: numeric
# desc: float sum of squares, the canonical static-tier numeric loop
```

The `tier` maps to the static-lowering frontier: tier 1 is numeric loops and
homogeneous collections, tier 2 is monomorphic dispatch and generators, tier 3
is strings and the harder cases. Grouping by tier shows where the typed tier is
already paying off and where it has yet to reach.

Adding a workload is dropping a `.py` file under `workloads/`. It benches on the
next run, header or not; without a header its tag defaults to its directory.

## Reading the result

Today unagi compiles correctly but still runs most workloads on the boxed tier,
so wall and compute time sit near or below CPython on the numeric loops while
PyPy's tracing JIT is ahead. Memory already leans unagi's way: a compiled binary
holds well under the interpreter's peak on recursion and collection workloads.
That is the honest starting point. As the typed tier lands in the build pipeline,
the numeric and collection tiers should cross CPython on time first, then the goal
line, with memory staying ahead. The harness does not move the number; it keeps
the number honest.
