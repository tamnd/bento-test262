package tc39

import (
	"os"
	"path/filepath"
	"strings"
)

// The composed source has to survive bento's type checker before any test
// body gets a verdict, so the harness prelude comes from harness/, this
// repo's TypeScript ports of the upstream files, not from test262/harness
// directly. The upstream files lean on idioms the checker rejects, like
// constructor functions growing properties and a redeclared print global.
// Each port keeps the upstream logic and messages; porting one is the way a
// new include enters the runnable set.
//
// There is no $262 shim. A test that touches the host object should say so
// in its verdict, and with no declaration in scope the checker hands it back
// with a name error, which is the truthful status until bento grows host
// hooks.

// UnportedInclude reports a test262 include with no TypeScript port yet. Jobs
// turns it into a handback result instead of failing the run, so the summary
// counts how much coverage each missing port is worth.
type UnportedInclude struct {
	Name string
}

func (e *UnportedInclude) Error() string {
	return "include not ported: " + e.Name
}

// Compose builds the source a job actually executes: the mode prologue, the
// mandatory harness ports, the ports of the test's own includes, the async
// completion handler when the test needs it, and finally the test body. A raw
// test runs its bytes untouched, that is the point of the flag.
func Compose(portsDir string, c Case, mode string) (string, error) {
	if mode == "raw" {
		return c.Source, nil
	}

	var b strings.Builder
	if mode == "strict" {
		b.WriteString("\"use strict\";\n")
	}

	names := []string{"sta.js", "assert.js"}
	names = append(names, c.Meta.Includes...)
	if c.Meta.HasFlag("async") {
		names = append(names, "doneprintHandle.js")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		port := strings.TrimSuffix(name, ".js") + ".ts"
		inc, err := os.ReadFile(filepath.Join(portsDir, port))
		if err != nil {
			if os.IsNotExist(err) {
				return "", &UnportedInclude{Name: name}
			}
			return "", err
		}
		b.Write(inc)
		b.WriteString("\n")
	}

	// A non-strict test may lean on a sloppy-mode implicit global through a
	// for-of, for-in, or for-await-of binding it never declares. Give those
	// targets a top-level var so the strict checker binds them, exactly the
	// names the test only ever writes through the loop head. A strict or a
	// negative test is left alone: strict wants the name error and a negative
	// test wants its failure.
	if mode == "sloppy" && c.Meta.Negative == nil {
		b.WriteString(hoistSloppyForBindings(c.Source))
	}
	b.WriteString(c.Source)

	// A module test runs under the Module goal, where the early errors differ
	// from a script: await is reserved at the top level, an undeclared export
	// is an error, and so on. typescript-go picks the goal from the syntax, so a
	// test with no import or export of its own would parse as a script and miss
	// those errors. An empty export at the end marks the whole composed file a
	// module without adding a binding or shifting a body line, so the goal the
	// checker applies matches the goal the test was written for. It is inert when
	// the test already exports: an empty export list names nothing to collide.
	if mode == "module" {
		b.WriteString("\nexport {};\n")
	}
	return b.String(), nil
}
