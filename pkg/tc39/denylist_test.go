package tc39

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDenylist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "denylist.txt")
	body := "# a comment\n" +
		"\n" +
		"   \n" +
		"test/language/expressions/foo.js\n" +
		"  test/built-ins/Array  \n" +
		"# trailing note\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadDenylist(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"test/language/expressions/foo.js",
		"test/built-ins/Array",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patterns = %q, want %q", got, want)
	}
}

func TestLoadDenylistMissingFile(t *testing.T) {
	got, err := LoadDenylist(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("missing file should yield nil patterns, got %q", got)
	}
}

func TestQuarantine(t *testing.T) {
	cases := []Case{
		{Rel: "test/language/expressions/foo.js"},
		{Rel: "test/language/expressions/bar.js"},
		{Rel: "test/built-ins/Array/every.js"},
		{Rel: "test/built-ins/String/at.js"},
	}
	patterns := []string{
		"expressions/foo.js",
		"test/built-ins/Array",
	}

	kept, dropped := Quarantine(cases, patterns)

	var keptRel, droppedRel []string
	for _, c := range kept {
		keptRel = append(keptRel, c.Rel)
	}
	for _, c := range dropped {
		droppedRel = append(droppedRel, c.Rel)
	}
	wantKept := []string{
		"test/language/expressions/bar.js",
		"test/built-ins/String/at.js",
	}
	wantDropped := []string{
		"test/language/expressions/foo.js",
		"test/built-ins/Array/every.js",
	}
	if !reflect.DeepEqual(keptRel, wantKept) {
		t.Errorf("kept = %q, want %q", keptRel, wantKept)
	}
	if !reflect.DeepEqual(droppedRel, wantDropped) {
		t.Errorf("dropped = %q, want %q", droppedRel, wantDropped)
	}
}

func TestQuarantineNoPatterns(t *testing.T) {
	cases := []Case{
		{Rel: "test/language/expressions/foo.js"},
		{Rel: "test/built-ins/Array/every.js"},
	}
	kept, dropped := Quarantine(cases, nil)
	if len(dropped) != 0 {
		t.Errorf("no patterns should drop nothing, dropped %d", len(dropped))
	}
	if !reflect.DeepEqual(kept, cases) {
		t.Errorf("no patterns should keep every case unchanged")
	}
}
