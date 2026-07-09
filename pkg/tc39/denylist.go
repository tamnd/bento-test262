package tc39

import (
	"bufio"
	"os"
	"strings"
)

// A denylist quarantines tests that must never reach a worker: a test whose
// lowering or build is known to exhaust memory or wedge the toolchain hard
// enough to threaten the machine, not one that merely fails. Excluding it up
// front keeps a full run from tripping over a known landmine, and keeps the
// snapshot honest by leaving the quarantined jobs out of the pass and handback
// tallies entirely rather than recording a verdict the run dared not produce.
//
// The file is one pattern per line. A line is a substring matched against a
// case's path relative to the test262 root (the same match Select's grep uses),
// so a single entry can name one file or a whole directory. Blank lines and
// lines starting with # are ignored, so the file can carry a note next to each
// entry explaining why it is dangerous.

// LoadDenylist reads quarantine patterns from path. A missing file is not an
// error: it means nothing is quarantined, the common case. Comments (#) and
// blank lines are skipped, and surrounding whitespace is trimmed so an aligned
// or commented file parses the same as a bare one.
func LoadDenylist(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, sc.Err()
}

// Quarantine splits cases into the ones to run and the ones a denylist pattern
// matches. A case is dropped when any pattern is a substring of its relative
// path. With no patterns every case is kept, so the caller can pass the result
// of LoadDenylist straight through without a nil check.
func Quarantine(cases []Case, patterns []string) (kept, dropped []Case) {
	if len(patterns) == 0 {
		return cases, nil
	}
	for _, c := range cases {
		if matchesAny(c.Rel, patterns) {
			dropped = append(dropped, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, dropped
}

// matchesAny reports whether rel contains any of the patterns as a substring.
func matchesAny(rel string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(rel, p) {
			return true
		}
	}
	return false
}
