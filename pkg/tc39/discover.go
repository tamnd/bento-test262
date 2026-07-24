package tc39

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"
)

// composeConcurrency bounds the parallel discovery and compose steps. These
// stages only read files and stitch strings, so they hold no checker and are
// safe to run wide; the memory cap that guards the build workers does not apply
// here. Left at one on a single-core box so the pool degrades to serial.
func composeConcurrency() int {
	return max(runtime.NumCPU(), 1)
}

// Case is one test file with its parsed frontmatter and raw source.
type Case struct {
	// Rel is the path relative to the test262 root, like
	// test/language/expressions/addition/S11.6.1_A1.js. It is the stable
	// identity everything else keys on.
	Rel    string
	Abs    string
	Meta   Meta
	Source string
}

// Discover walks the test262 checkout and returns every runnable case under
// the given relative prefixes, sorted by path. Fixture files are modules other
// tests import, never run on their own, so they are left out.
func Discover(root string, prefixes []string) ([]Case, error) {
	// Collect the runnable paths first, in the deterministic order WalkDir
	// yields, then read and parse them in parallel. Reading and frontmatter
	// parsing dominate discovery time and neither touches shared state, so a
	// bounded pool cuts the wall-clock without disturbing the output order:
	// results land in slots keyed by path index and the slice stays sorted.
	var paths []string
	for _, prefix := range prefixes {
		base := filepath.Join(root, filepath.FromSlash(prefix))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".js") || strings.HasSuffix(path, "_FIXTURE.js") {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	out := make([]Case, len(paths))
	var g errgroup.Group
	g.SetLimit(composeConcurrency())
	for i, path := range paths {
		g.Go(func() error {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			meta, err := ParseMeta(string(src))
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			out[i] = Case{Rel: rel, Abs: path, Meta: meta, Source: string(src)}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// Select narrows a discovered case list for a focused run. grep keeps only
// cases whose relative path contains the substring, matched anywhere rather than
// as a prefix the way the discovery filters are, so a run can home in on a
// feature without spelling out its directory. limit then caps the count, taking
// the first limit cases in path order; a limit of zero or less keeps them all.
// The order Discover returns is stable, so the same grep and limit pick the same
// slice every run.
func Select(cases []Case, grep string, limit int) []Case {
	if grep != "" {
		kept := cases[:0:0]
		for _, c := range cases {
			if strings.Contains(c.Rel, grep) {
				kept = append(kept, c)
			}
		}
		cases = kept
	}
	if limit > 0 && len(cases) > limit {
		cases = cases[:limit]
	}
	return cases
}

// Job is one execution of one case in one mode. A default test expands to a
// sloppy job and a strict job; the flags collapse that to a single mode.
type Job struct {
	// ID is Rel plus the mode suffix, like path.js#strict, the key used in
	// expectations files and reports.
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	// EntryName is the file name the entry source is staged under. A module test
	// gets its real base name so a self-import or a fixture re-exporting back into
	// it resolves against the entry; a blank name defaults to the neutral
	// test262.ts a single-file test rides in as.
	EntryName string `json:"entryName,omitempty"`
	// Modules are the sibling source files the entry statically imports, staged
	// beside it so the front end resolves each specifier against a real file
	// instead of declining with a cannot-find-module error.
	Modules  []StagedModule `json:"modules,omitempty"`
	Async    bool           `json:"async,omitempty"`
	NegType  string         `json:"negType,omitempty"`
	NegPhase string         `json:"negPhase,omitempty"`
}

// Jobs expands cases into concrete executions with composed sources. The
// ports directory supplies the TypeScript harness includes. A case whose
// include has no port yet cannot compose, so it comes back as a precooked
// handback result instead of a job; the missing port is a coverage gap the
// summary should count, not an error that stops the run.
func Jobs(portsDir string, cases []Case) ([]Job, []Result, error) {
	// Composing a case reads its harness includes and stitches the source; it is
	// independent per case and CPU/IO bound, so it runs in a bounded pool. Each
	// case writes its jobs and precooked handbacks into its own slot, then the
	// slots concatenate in case order, keeping the deterministic ordering the
	// expectations files and reports depend on.
	type composed struct {
		jobs      []Job
		precooked []Result
	}
	slots := make([]composed, len(cases))
	var g errgroup.Group
	g.SetLimit(composeConcurrency())
	for i, c := range cases {
		g.Go(func() error {
			modes, err := modesOf(c.Meta)
			if err != nil {
				return fmt.Errorf("%s: %w", c.Rel, err)
			}
			for _, mode := range modes {
				src, err := Compose(portsDir, c, mode)
				if err != nil {
					var unported *UnportedInclude
					if errors.As(err, &unported) {
						slots[i].precooked = append(slots[i].precooked, Result{
							ID:     c.Rel + "#" + mode,
							Status: "handback",
							Error:  unported.Error(),
						})
						continue
					}
					var hostFlag *HostContextFlag
					if errors.As(err, &hostFlag) {
						slots[i].precooked = append(slots[i].precooked, Result{
							ID:     c.Rel + "#" + mode,
							Status: "handback",
							Error:  hostFlag.Error(),
						})
						continue
					}
					return fmt.Errorf("%s: %w", c.Rel, err)
				}
				j := Job{
					ID:     c.Rel + "#" + mode,
					Name:   c.Abs,
					Source: src,
					Async:  c.Meta.HasFlag("async"),
				}
				// A module test resolves its imports against sibling files. Stage
				// the graph it pulls in and pin the entry to its real name so a
				// self-import or a fixture re-exporting into the test resolves.
				if mode == "module" {
					mods, err := resolveModuleGraph(c.Abs, c.Source)
					if err != nil {
						return fmt.Errorf("%s: %w", c.Rel, err)
					}
					j.Modules = mods
					j.EntryName = tsExtension(filepath.Base(c.Abs))
				}
				if c.Meta.Negative != nil {
					j.NegType = c.Meta.Negative.Type
					j.NegPhase = c.Meta.Negative.Phase
				}
				slots[i].jobs = append(slots[i].jobs, j)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	var out []Job
	var precooked []Result
	for i := range slots {
		out = append(out, slots[i].jobs...)
		precooked = append(precooked, slots[i].precooked...)
	}
	return out, precooked, nil
}

// modesOf maps frontmatter flags to the list of modes a test runs in.
func modesOf(m Meta) ([]string, error) {
	switch {
	case m.HasFlag("raw"):
		return []string{"raw"}, nil
	case m.HasFlag("module"):
		return []string{"module"}, nil
	case m.HasFlag("onlyStrict"):
		return []string{"strict"}, nil
	case m.HasFlag("noStrict"):
		return []string{"sloppy"}, nil
	default:
		return []string{"sloppy", "strict"}, nil
	}
}
