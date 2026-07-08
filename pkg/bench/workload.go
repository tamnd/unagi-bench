// Package bench is the measurement core of unagi-bench: it discovers the Python
// workloads, runs each one under every available engine, verifies the engines
// agree on the output, times them, and reports the speedup against CPython. The
// goal it measures against is 2x over the fastest competitor on the provable
// subset, so the report always pairs a time with the static-tier fraction that
// earned it.
package bench

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Workload is one benchmark program: a Python source file plus the metadata its
// header comment carries. Tier maps to the static-lowering frontier tiers (a
// numeric loop is tier 1, monomorphic dispatch tier 2, and so on), so a report
// can group results by how much of each workload the typed tier should reach.
type Workload struct {
	Name string // stable id, the path relative to the workloads root without .py
	Path string // absolute path to the source
	Tier int    // frontier tier from the header, 0 if unset
	Tag  string // coarse category, numeric/collections/strings/generators/recursion
	Desc string // one-line description from the header
}

// Discover walks a workloads directory and returns every .py file as a Workload,
// sorted by name so a run is deterministic. A file missing a header still loads;
// its tier is 0 and its tag is the parent directory, so a new workload benches
// even before its header is filled in.
func Discover(root string) ([]Workload, error) {
	var out []Workload
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}
		w, err := loadWorkload(root, path)
		if err != nil {
			return err
		}
		out = append(out, w)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// loadWorkload reads a single workload's header. The header is a run of leading
// `# key: value` comment lines, the same shape the corpus files carry, so the
// metadata lives next to the code it describes rather than in a side manifest.
func loadWorkload(root, path string) (Workload, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Workload{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	name := strings.TrimSuffix(filepath.ToSlash(rel), ".py")

	w := Workload{Name: name, Path: abs, Tag: firstDir(rel)}

	f, err := os.Open(path)
	if err != nil {
		return Workload{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break // the header ends at the first line of code
		}
		key, val, ok := headerField(line)
		if !ok {
			continue
		}
		switch key {
		case "tier":
			fmt.Sscanf(val, "%d", &w.Tier)
		case "tag":
			w.Tag = val
		case "desc":
			w.Desc = val
		}
	}
	if err := sc.Err(); err != nil {
		return Workload{}, err
	}
	return w, nil
}

// headerField parses a `# key: value` comment line, returning ok false for a bare
// comment with no colon (like the corpus banner line).
func headerField(line string) (key, val string, ok bool) {
	body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	i := strings.IndexByte(body, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+1:]), true
}

// firstDir is the leading path element of a relative path, the default tag.
func firstDir(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}
