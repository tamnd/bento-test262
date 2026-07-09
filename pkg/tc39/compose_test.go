package tc39

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portsDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"sta.ts":    "class Test262Error {}\n",
		"assert.ts": "const assert = 0;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestComposeStrictPrologueAndPorts(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{Rel: "test/x.js", Source: "var a = 1;\n"}
	got, err := Compose(dir, c, "strict")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "\"use strict\";\n") {
		t.Errorf("missing strict prologue:\n%s", got)
	}
	if !strings.Contains(got, "class Test262Error") || !strings.Contains(got, "const assert") {
		t.Errorf("mandatory ports missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "var a = 1;\n") {
		t.Errorf("test body must come last:\n%s", got)
	}
}

func TestComposeModuleGoalMarker(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{Rel: "test/x.js", Source: "class await {}\n"}
	got, err := Compose(dir, c, "module")
	if err != nil {
		t.Fatal(err)
	}
	// The empty export marks the file a module so the checker applies the Module
	// goal's early errors, and it comes after the body so it never shifts a body
	// line the front end would report on.
	if !strings.Contains(got, "export {};") {
		t.Errorf("module job missing the module-goal marker:\n%s", got)
	}
	body := strings.Index(got, "class await {}")
	marker := strings.Index(got, "export {};")
	if body < 0 || marker < body {
		t.Errorf("marker must follow the test body:\n%s", got)
	}
}

func TestComposeScriptHasNoModuleMarker(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{Rel: "test/x.js", Source: "var a = 1;\n"}
	for _, mode := range []string{"sloppy", "strict"} {
		got, err := Compose(dir, c, mode)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "export {};") {
			t.Errorf("%s job must stay a script, no module marker:\n%s", mode, got)
		}
	}
}

func TestComposeRawUntouched(t *testing.T) {
	c := Case{Rel: "test/x.js", Source: "anything at all"}
	got, err := Compose(t.TempDir(), c, "raw")
	if err != nil {
		t.Fatal(err)
	}
	if got != c.Source {
		t.Errorf("raw source changed: %q", got)
	}
}

func TestJobsUnportedIncludeBecomesHandback(t *testing.T) {
	dir := portsDirForTest(t)
	cases := []Case{
		{Rel: "test/a.js", Source: "var a = 1;\n", Meta: Meta{Flags: []string{"onlyStrict"}}},
		{Rel: "test/b.js", Source: "var b = 1;\n", Meta: Meta{Flags: []string{"onlyStrict"}, Includes: []string{"propertyHelper.js"}}},
	}
	jobs, precooked, err := Jobs(dir, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "test/a.js#strict" {
		t.Errorf("jobs = %+v", jobs)
	}
	if len(precooked) != 1 {
		t.Fatalf("precooked = %+v", precooked)
	}
	r := precooked[0]
	if r.ID != "test/b.js#strict" || r.Status != "handback" || !strings.Contains(r.Error, "propertyHelper.js") {
		t.Errorf("precooked = %+v", r)
	}
}
