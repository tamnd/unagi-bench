package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
)

// SchemaVersion is the results.json format version. A reader rejects a file from a
// newer schema rather than silently misreading it, the same contract unagi's own
// build report uses. Version 2 added the peak-RSS memory statistics and the run
// scope fields (Discovered/Filter).
const SchemaVersion = 2

// Goal is the campaign target: unagi should run at least this many times faster
// than the fastest competitor on the provable subset. It is reported against, not
// gated, until the toolchain wires the typed tier into the build pipeline.
const Goal = 2.0

// Results is a whole benchmark run: the machine it ran on, the engines it found,
// and one record per workload. It is schema-versioned so a stored run stays
// readable as the format grows.
type Results struct {
	Schema     int                   `json:"schema"`
	Machine    Machine               `json:"machine"`
	Engines    map[string]EngineInfo `json:"engines"`
	Reps       int                   `json:"reps"`
	Warmup     int                   `json:"warmup"`
	Discovered int                   `json:"discovered,omitempty"` // workloads found before the filter
	Filter     string                `json:"filter,omitempty"`     // the name filter this run applied, empty for a full run
	Workloads  []WorkloadResult      `json:"workloads"`
}

// Partial reports whether this run covered only a subset of the discovered
// workloads, either because a name filter was applied or because fewer workloads
// ran than were found. A partial run's aggregate is still meaningful but the
// report labels it so a subset speedup is never mistaken for the whole-suite
// figure.
func (r Results) Partial() bool {
	return r.Filter != "" || (r.Discovered > 0 && len(r.Workloads) < r.Discovered)
}

// Machine records where a run happened, so two result files are only compared when
// they came from the same hardware and toolchain.
type Machine struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
}

// EngineInfo is what was found for one engine: whether it is available and the
// binary and version behind it, for provenance in the report header.
type EngineInfo struct {
	Available bool   `json:"available"`
	Bin       string `json:"bin,omitempty"`
	Version   string `json:"version,omitempty"`
}

// WorkloadResult holds every engine's measurement for one workload plus the
// CPython oracle output the engines are checked against.
type WorkloadResult struct {
	Name    string             `json:"name"`
	Tag     string             `json:"tag,omitempty"`
	Tier    int                `json:"tier,omitempty"`
	Desc    string             `json:"desc,omitempty"`
	Oracle  string             `json:"oracle"`
	Engines map[string]Measure `json:"engines"`
}

// Measure is one engine's result on one workload: its wall-time, compute-time and
// peak-memory statistics, how it compares to CPython on each, whether its output
// matched the oracle, and a skip reason when it could not run.
//
// Stats is wall time: the whole process from exec to exit, the end-to-end cost a
// user feels. Compute is the in-script cost the program self-reports through an
// injected timer around its module body, so it isolates the work the program did
// from the fixed per-process overhead (interpreter startup for CPython, Go-runtime
// init and the unagi build for a compiled binary). ComputeOK says whether every
// repetition reported a timer reading.
type Measure struct {
	Engine    string   `json:"engine"`
	OK        bool     `json:"ok"`
	Skip      string   `json:"skip,omitempty"`
	Mismatch  bool     `json:"mismatch,omitempty"`
	Output    string   `json:"output,omitempty"`
	Stats     stats    `json:"stats"` // wall time
	Compute   stats    `json:"compute,omitzero"`
	ComputeOK bool     `json:"computeOk,omitempty"`
	Mem       memStats `json:"mem"`

	SpeedupVsCPython        float64 `json:"speedupVsCpython,omitempty"`        // CPython median wall / ours, >1 is faster
	ComputeSpeedupVsCPython float64 `json:"computeSpeedupVsCpython,omitempty"` // CPython median compute / ours, >1 is faster
	MemRatioVsCPython       float64 `json:"memRatioVsCpython,omitempty"`       // CPython median peak RSS / ours, >1 is leaner
}

// thisMachine captures the current host for a results header.
func thisMachine() Machine {
	return Machine{OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version()}
}

// Marshal writes results as stable, indented JSON with HTML escaping off, so a
// stored run diffs cleanly and round-trips byte for byte.
func Marshal(w io.Writer, r Results) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// Parse reads a results file, rejecting a schema newer than this build understands.
func Parse(r io.Reader) (Results, error) {
	var res Results
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return Results{}, err
	}
	if res.Schema > SchemaVersion {
		return Results{}, fmt.Errorf("results schema %d is newer than this build supports (%d)", res.Schema, SchemaVersion)
	}
	return res, nil
}
