# unagi-bench

A benchmark harness that times the [unagi](https://github.com/tamnd/unagi)
Python-to-Go compiler against CPython 3.14 and, when it is installed, PyPy.

The point is a single honest number: how much faster is a unagi-compiled binary
than the interpreter it replaces, on the same program, producing the same output.
The campaign goal is 2x over the fastest competitor on the provable subset, and
this harness is the instrument that tracks progress toward it.

## How it works

For every workload under `workloads/`, unagi-bench:

1. builds the workload to a native binary with `unagi build`,
2. runs it under every engine it can find (CPython, optionally PyPy, unagi),
3. takes CPython's output as the oracle and flags any engine that disagrees,
4. times each engine over a few repetitions after a warmup, and
5. reports the median time and the speedup against CPython and against the
   fastest competitor.

Timing brackets the whole process, so an interpreter pays its startup on every
run and the compiled binary pays its small startup too. That is the end-to-end
cost a user actually sees. A workload whose output does not match CPython is
timed but marked `MISMATCH`, and it never counts toward a speedup, because a fast
wrong answer is not a win.

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
so the numbers show it near or below CPython while PyPy's tracing JIT is ahead.
That is the honest starting point. As the typed tier lands in the build
pipeline, the numeric and collection tiers should cross CPython first, then the
goal line. The harness does not move the number; it keeps the number honest.
