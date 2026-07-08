package bench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Engine is one runtime under test. CPython is the reference and the correctness
// oracle; PyPy is an optional tracing-JIT competitor; unagi is us. Each engine
// turns a workload into a runnable argv through Prepare, so the timing loop is the
// same for an interpreter that runs source directly and a compiler that builds a
// binary first.
type Engine interface {
	// Name is the stable label used in results and the report.
	Name() string
	// Available reports whether the engine's toolchain was found on this machine.
	Available() bool
	// Prepare readies a workload and returns the argv to run and a cleanup. For an
	// interpreter this is just the source path; for unagi it compiles a binary. A
	// prepare error (a build failure) marks the workload skipped for this engine,
	// never the whole run.
	Prepare(ctx context.Context, w Workload, workdir string) (argv []string, cleanup func(), err error)
}

// CPython runs the reference interpreter. It prefers the pinned python3.14 so the
// oracle matches the compiler's target, falling back to python3 when the pinned
// build is absent, which the report notes.
type CPython struct{ bin string }

// NewCPython resolves the CPython interpreter, preferring python3.14.
func NewCPython() *CPython {
	for _, name := range []string{"python3.14", "python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return &CPython{bin: p}
		}
	}
	return &CPython{}
}

func (c *CPython) Name() string    { return "cpython" }
func (c *CPython) Available() bool { return c.bin != "" }
func (c *CPython) Bin() string     { return c.bin }
func (c *CPython) Prepare(_ context.Context, w Workload, _ string) ([]string, func(), error) {
	return []string{c.bin, w.Path}, func() {}, nil
}

// PyPy runs the optional PyPy interpreter when its binary is present. It is not
// required: a machine without PyPy still benches CPython against unagi, and the
// report simply omits the PyPy column.
type PyPy struct{ bin string }

// NewPyPy resolves a PyPy interpreter, or an unavailable engine when none is found.
func NewPyPy() *PyPy {
	for _, name := range []string{"pypy3.14", "pypy3", "pypy"} {
		if p, err := exec.LookPath(name); err == nil {
			return &PyPy{bin: p}
		}
	}
	return &PyPy{}
}

func (p *PyPy) Name() string    { return "pypy" }
func (p *PyPy) Available() bool { return p.bin != "" }
func (p *PyPy) Prepare(_ context.Context, w Workload, _ string) ([]string, func(), error) {
	return []string{p.bin, w.Path}, func() {}, nil
}

// Unagi compiles each workload to a native binary with `unagi build` and times the
// binary, so the measurement is the compiled program's steady-state cost, not the
// compile. The binary lives in the run's workdir and is cleaned up after.
type Unagi struct{ bin string }

// NewUnagi resolves the unagi compiler from an explicit path or PATH. An empty
// path falls back to the `unagi` name on PATH.
func NewUnagi(path string) *Unagi {
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return &Unagi{bin: path}
	}
	if p, err := exec.LookPath("unagi"); err == nil {
		return &Unagi{bin: p}
	}
	return &Unagi{}
}

func (u *Unagi) Name() string    { return "unagi" }
func (u *Unagi) Available() bool { return u.bin != "" && fileExists(u.bin) }

// Prepare builds the workload to a binary in workdir. A build failure returns an
// error carrying the compiler's output, which the runner records as a skip so the
// rest of the matrix still runs.
func (u *Unagi) Prepare(ctx context.Context, w Workload, workdir string) ([]string, func(), error) {
	out := filepath.Join(workdir, sanitize(w.Name))
	cmd := exec.CommandContext(ctx, u.bin, "build", "-o", out, w.Path)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, func() {}, fmt.Errorf("unagi build failed: %v: %s", err, trim(combined))
	}
	cleanup := func() { _ = os.Remove(out) }
	return []string{out}, cleanup, nil
}

// fileExists reports whether a path is a regular readable file.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// sanitize turns a workload name into a safe single filename for the built binary.
func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == '\\' || r == ' ' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return "wl_" + string(out)
}

// trim shortens a compiler error to its last non-empty line for a compact skip note.
func trim(b []byte) string {
	s := string(b)
	last := ""
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				last = line
			}
			start = i + 1
		}
	}
	return last
}
