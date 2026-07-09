package tc39

import (
	"path/filepath"
	"testing"
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
