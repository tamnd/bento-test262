package tc39

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCacheStreamSurvivesNoSave proves the streamed cache is durable without a
// closing Save: a run that is killed after recording some jobs but before it can
// compact still leaves those jobs on disk, so the next run loads them and
// resumes. This is the resumability the streaming buys.
func TestCacheStreamSurvivesNoSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.ndjson")

	c, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BeginStream(); err != nil {
		t.Fatal(err)
	}
	c.Put("k1", Result{Status: "pass"})
	c.Put("k2", Result{Status: "handback", Error: "later slice"})
	// Deliberately no CloseStream and no Save, standing in for a killed run.

	// A fresh load sees both jobs, so a rerun would short-circuit them.
	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := reloaded.Get("k1", "id1"); !ok || r.Status != "pass" {
		t.Errorf("k1 did not survive the unsaved stream: %+v ok=%v", r, ok)
	}
	if r, ok := reloaded.Get("k2", "id2"); !ok || r.Status != "handback" || r.Error != "later slice" {
		t.Errorf("k2 did not survive the unsaved stream: %+v ok=%v", r, ok)
	}
}

// TestCacheStreamThenSaveCompacts proves that a key rewritten across a streamed
// run and a saved run ends up with one compacted line, and that timeouts and
// crashes stay out of the stream the way they stay out of the file.
func TestCacheStreamThenSaveCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.ndjson")

	c, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BeginStream(); err != nil {
		t.Fatal(err)
	}
	c.Put("k1", Result{Status: "pass"})
	// A timeout and a crash are nondeterministic and must never be recorded, so a
	// rerun retries them rather than trusting a machine-caused verdict.
	c.Put("t1", Result{Status: "timeout"})
	c.Put("c1", Result{Status: "crash", Error: "worker died"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get("k1", "id"); !ok {
		t.Error("k1 missing after save")
	}
	if _, ok := reloaded.Get("t1", "id"); ok {
		t.Error("a timeout was cached")
	}
	if _, ok := reloaded.Get("c1", "id"); ok {
		t.Error("a crash was cached")
	}
}

// TestCacheCloseStreamIdempotent proves closing a cache that never streamed, or
// closing twice, is safe, since Save calls CloseStream and a caller may too.
func TestCacheCloseStreamIdempotent(t *testing.T) {
	c, err := LoadCache(filepath.Join(t.TempDir(), "r.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CloseStream(); err != nil {
		t.Fatalf("closing an unstreamed cache errored: %v", err)
	}
	if err := c.BeginStream(); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseStream(); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseStream(); err != nil {
		t.Fatalf("second close errored: %v", err)
	}
}

// TestPruneResultsCaches proves the per-version results caches are held to the
// newest few, so a campaign of commits does not pile them onto the disk, while
// the just-loaded file and the shared version-less tail.ndjson always survive.
func TestPruneResultsCaches(t *testing.T) {
	dir := t.TempDir()
	// Five per-version results files plus the shared tail, oldest to newest so the
	// mod times are ordered by index.
	names := []string{
		"results-v1.h0000.ndjson",
		"results-v1.h1111.ndjson",
		"results-v1.h2222.ndjson",
		"results-v1.h3333.ndjson",
		"results-v1.h4444.ndjson",
	}
	base := time.Now().Add(-time.Hour)
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	tail := filepath.Join(dir, "tail.ndjson")
	if err := os.WriteFile(tail, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The oldest file stands in for the current run's keepPath, so the prune must
	// retain it even though it is not among the newest three by mod time.
	keep := filepath.Join(dir, "results-v1.h0000.ndjson")

	PruneResultsCaches(dir, keep, 3)

	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	// The newest three survive by mod time.
	for _, n := range []string{"results-v1.h4444.ndjson", "results-v1.h3333.ndjson", "results-v1.h2222.ndjson"} {
		if !exists(filepath.Join(dir, n)) {
			t.Errorf("%s should have been kept as one of the newest three", n)
		}
	}
	// The keepPath survives even though it is the oldest.
	if !exists(keep) {
		t.Error("the current run's results cache was pruned")
	}
	// The one file that is neither newest-three nor keepPath is gone.
	if exists(filepath.Join(dir, "results-v1.h1111.ndjson")) {
		t.Error("an old results cache outside the keep set was not pruned")
	}
	// The shared tail cache is never a results- file, so it stays.
	if !exists(tail) {
		t.Error("the shared tail cache was pruned")
	}
}
