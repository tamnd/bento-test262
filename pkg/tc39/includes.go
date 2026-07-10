package tc39

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IncludeStat is the reach of one harness include across the discovered corpus:
// how many jobs and how many files name it, and whether a TypeScript port exists
// for it under the ports directory. Jobs weighs a file by the number of modes it
// runs in, so a default test that runs sloppy and strict counts twice, matching
// the job count the run reports.
type IncludeStat struct {
	Name   string
	Ported bool
	Jobs   int
	Files  int
}

// IncludeReport is the audit of harness include coverage over a discovered case
// list: the per-include reach split into the includes that have a port and the
// ones that do not. An unported include gates every job that names it, so its
// reach is the exact job count that port would unlock, the number the campaign
// checklist tracks.
type IncludeReport struct {
	Unported []IncludeStat
	Ported   []IncludeStat
}

// AuditIncludes resolves, for every case, the same harness include names Compose
// would load, and tallies each name's reach. It mirrors Compose's name resolution
// exactly, the mandatory sta.js and assert.js, then the test's own includes, then
// doneprintHandle.js for an async test, so a name the report calls ported is one
// Compose can always load and a name it calls unported is one Compose hands back
// on. The two lists are sorted by descending job reach, so the largest coverage
// gap reads first.
func AuditIncludes(portsDir string, cases []Case) (IncludeReport, error) {
	type tally struct {
		jobs  int
		files int
	}
	counts := map[string]*tally{}
	for _, c := range cases {
		modes, err := modesOf(c.Meta)
		if err != nil {
			return IncludeReport{}, err
		}
		if len(modes) == 0 {
			continue
		}
		for _, name := range includeNames(c) {
			t := counts[name]
			if t == nil {
				t = &tally{}
				counts[name] = t
			}
			t.jobs += len(modes)
			t.files++
		}
	}

	var report IncludeReport
	for name, t := range counts {
		stat := IncludeStat{
			Name:   name,
			Ported: hasPort(portsDir, name),
			Jobs:   t.jobs,
			Files:  t.files,
		}
		if stat.Ported {
			report.Ported = append(report.Ported, stat)
		} else {
			report.Unported = append(report.Unported, stat)
		}
	}
	sortByReach(report.Unported)
	sortByReach(report.Ported)
	return report, nil
}

// includeNames returns the harness include names a case composes, deduplicated in
// the order Compose stitches them. Keeping this in step with Compose is what makes
// the audit authoritative: the two must resolve the same names or the report would
// disagree with the run.
func includeNames(c Case) []string {
	names := []string{"sta.js", "assert.js"}
	names = append(names, c.Meta.Includes...)
	if c.Meta.HasFlag("async") {
		names = append(names, "doneprintHandle.js")
	}
	seen := map[string]bool{}
	out := names[:0:0]
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// hasPort reports whether a TypeScript port exists for an include name, the same
// harness/<base>.ts lookup Compose makes.
func hasPort(portsDir, name string) bool {
	port := strings.TrimSuffix(name, ".js") + ".ts"
	_, err := os.Stat(filepath.Join(portsDir, port))
	return err == nil
}

// sortByReach orders include stats by descending job reach, then by name so the
// order is stable when two includes gate the same job count.
func sortByReach(stats []IncludeStat) {
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Jobs != stats[j].Jobs {
			return stats[i].Jobs > stats[j].Jobs
		}
		return stats[i].Name < stats[j].Name
	})
}
