package tc39

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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
	var out []Case
	for _, prefix := range prefixes {
		base := filepath.Join(root, filepath.FromSlash(prefix))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".js") || strings.HasSuffix(path, "_FIXTURE.js") {
				return nil
			}
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
			out = append(out, Case{Rel: rel, Abs: path, Meta: meta, Source: string(src)})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Job is one execution of one case in one mode. A default test expands to a
// sloppy job and a strict job; the flags collapse that to a single mode.
type Job struct {
	// ID is Rel plus the mode suffix, like path.js#strict, the key used in
	// expectations files and reports.
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Async    bool   `json:"async,omitempty"`
	NegType  string `json:"negType,omitempty"`
	NegPhase string `json:"negPhase,omitempty"`
}

// Jobs expands cases into concrete executions with composed sources. The
// ports directory supplies the TypeScript harness includes. A case whose
// include has no port yet cannot compose, so it comes back as a precooked
// handback result instead of a job; the missing port is a coverage gap the
// summary should count, not an error that stops the run.
func Jobs(portsDir string, cases []Case) ([]Job, []Result, error) {
	var out []Job
	var precooked []Result
	for _, c := range cases {
		modes, err := modesOf(c.Meta)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", c.Rel, err)
		}
		for _, mode := range modes {
			src, err := Compose(portsDir, c, mode)
			if err != nil {
				var unported *UnportedInclude
				if errors.As(err, &unported) {
					precooked = append(precooked, Result{
						ID:     c.Rel + "#" + mode,
						Status: "handback",
						Error:  unported.Error(),
					})
					continue
				}
				return nil, nil, fmt.Errorf("%s: %w", c.Rel, err)
			}
			j := Job{
				ID:     c.Rel + "#" + mode,
				Name:   c.Abs,
				Source: src,
				Async:  c.Meta.HasFlag("async"),
			}
			if c.Meta.Negative != nil {
				j.NegType = c.Meta.Negative.Type
				j.NegPhase = c.Meta.Negative.Phase
			}
			out = append(out, j)
		}
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
