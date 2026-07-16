package tc39

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A module-goal test imports its sibling fixtures by a relative specifier, like
// import { x } from './name_FIXTURE.js'. The harness rides every test behind a
// single-file front door, so those specifiers point at nothing and the front
// end declines with a cannot-find-module error before the lowerer sees the
// program. Composing the graph is what clears that: read the test's relative
// imports, follow them to the fixture files sitting beside it in the corpus, and
// stage each one next to the entry so the specifier resolves against a real
// file.
//
// The graph is small and static. Every module-code specifier in the corpus is a
// single-level sibling (./name.js), and a fixture may import another fixture or
// even import back into the test, so the walk follows edges transitively and
// guards against the cycles those back-edges form. A specifier that does not
// resolve to a readable sibling is left alone: the front end declines it, which
// is the honest status for a test whose target is missing or computed at run
// time.

// StagedModule is one sibling source file staged alongside the entry so an
// import specifier resolves. Name is the file written into the scratch
// directory, a POSIX-relative path with a .ts extension so the checker parses it
// as TypeScript the same way the entry rides in as .ts. Source is its content.
type StagedModule struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// fromSpecifier matches the specifier of a static import or export-from, the two
// forms that carry a `from` clause: import ... from '...', export ... from '...',
// and export * [as ns] from '...'. relImportSpecifier matches a bare
// side-effect import, import '...', which has no from clause.
var (
	fromSpecifier    = regexp.MustCompile(`\bfrom\s*['"]([^'"]+)['"]`)
	bareSpecifier    = regexp.MustCompile(`\bimport\s*['"]([^'"]+)['"]`)
	frontmatterBlock = regexp.MustCompile(`(?s)/\*---.*?---\*/`)
)

// relativeSpecifiers returns the relative module specifiers a source statically
// imports or re-exports, in the order they appear, deduplicated. Only ./ and ../
// specifiers are returned; a bare package name or a built-in is not a sibling to
// stage. The frontmatter block is stripped first so a `from '...'` written in a
// test's description does not read as an import.
func relativeSpecifiers(source string) []string {
	body := frontmatterBlock.ReplaceAllString(source, "")

	// Collect matches from both forms with their source positions, so the result
	// keeps the order the specifiers appear in rather than grouping by form.
	type hit struct {
		at   int
		spec string
	}
	var hits []hit
	for _, re := range []*regexp.Regexp{fromSpecifier, bareSpecifier} {
		for _, idx := range re.FindAllStringSubmatchIndex(body, -1) {
			hits = append(hits, hit{at: idx[0], spec: body[idx[2]:idx[3]]})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })

	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		if !strings.HasPrefix(h.spec, "./") && !strings.HasPrefix(h.spec, "../") {
			continue
		}
		if seen[h.spec] {
			continue
		}
		seen[h.spec] = true
		out = append(out, h.spec)
	}
	return out
}

// stagedName turns a relative specifier into the POSIX-relative name its staged
// file takes in the scratch directory. It keeps the specifier's directory shape
// so a nested import resolves against the same relative path, and rewrites a
// JavaScript extension to .ts so the checker parses the staged file as
// TypeScript, matching how the specifier's .js resolves to its .ts sibling.
func stagedName(spec string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(spec, "./")))
	return tsExtension(clean)
}

// tsExtension rewrites a JavaScript extension to the TypeScript one the checker
// resolves it to, leaving any other name untouched.
func tsExtension(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return strings.TrimSuffix(name, ".js") + ".ts"
	case strings.HasSuffix(name, ".mjs"):
		return strings.TrimSuffix(name, ".mjs") + ".mts"
	case strings.HasSuffix(name, ".cjs"):
		return strings.TrimSuffix(name, ".cjs") + ".cts"
	default:
		return name
	}
}

// resolveModuleGraph walks the sibling modules a module-goal test imports,
// transitively, and returns them staged. entryPath is the test's real path in
// the corpus, so specifiers resolve against the directory it lives in; its own
// staged name (the entry the caller writes) is seeded as visited, so a fixture
// that re-exports back into the test does not stage a second copy of it. A
// specifier that points outside the entry's directory, or at a file that does
// not exist or cannot be read, is skipped rather than an error: the front end
// declines the unresolved import, which is the truthful status for it.
func resolveModuleGraph(entryPath, entrySource string) ([]StagedModule, error) {
	entryDir := filepath.Dir(entryPath)
	entryName := tsExtension(filepath.Base(entryPath))

	visited := map[string]bool{entryName: true}
	var out []StagedModule

	type work struct {
		dir     string
		sources []string
	}
	queue := []work{{dir: entryDir, sources: []string{entrySource}}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, src := range cur.sources {
			for _, spec := range relativeSpecifiers(src) {
				name := stagedName(spec)
				// A specifier that climbs out of the entry's directory would stage
				// outside the scratch tree; leave it for the front end to decline.
				if name == ".." || strings.HasPrefix(name, "../") {
					continue
				}
				if visited[name] {
					continue
				}
				visited[name] = true

				target := filepath.Join(cur.dir, filepath.FromSlash(strings.TrimPrefix(spec, "./")))
				content, ok := readModuleFile(target)
				if !ok {
					continue
				}
				out = append(out, StagedModule{Name: name, Source: content})
				queue = append(queue, work{dir: filepath.Dir(target), sources: []string{content}})
			}
		}
	}
	return out, nil
}

// stageJob writes a job's entry source and every sibling module it imports into
// dir, returning the path of the entry to hand the front end. A module test's
// entry takes its real base name (EntryName) so a self-import or a fixture that
// re-exports back into it resolves against the entry; a single-file test falls
// back to defaultEntry. The siblings land under the .ts names their specifiers
// resolve to, with nested directories created as needed, so the front end
// resolves each import against a real staged file.
func stageJob(dir, defaultEntry string, j Job) (string, error) {
	for _, m := range j.Modules {
		path := filepath.Join(dir, filepath.FromSlash(m.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(m.Source), 0o644); err != nil {
			return "", err
		}
	}
	name := j.EntryName
	if name == "" {
		name = defaultEntry
	}
	entry := filepath.Join(dir, name)
	if err := os.WriteFile(entry, []byte(j.Source), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// readModuleFile reads a sibling module named by a specifier, trying the path as
// written and then its TypeScript sibling, matching how the checker resolves a
// .js import to a .ts source. It reports false when nothing readable sits at the
// path, so an unresolved specifier is skipped rather than fatal.
func readModuleFile(target string) (string, bool) {
	candidates := []string{target}
	switch {
	case strings.HasSuffix(target, ".js"):
		candidates = append(candidates, strings.TrimSuffix(target, ".js")+".ts")
	case strings.HasSuffix(target, ".mjs"):
		candidates = append(candidates, strings.TrimSuffix(target, ".mjs")+".mts")
	case strings.HasSuffix(target, ".cjs"):
		candidates = append(candidates, strings.TrimSuffix(target, ".cjs")+".cts")
	}
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil {
			return string(b), true
		}
	}
	return "", false
}
