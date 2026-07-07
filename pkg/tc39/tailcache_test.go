package tc39

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamnd/bento/pkg/build"
)

// TestRuntimeHashStableAndSensitive checks the two properties the tail cache
// leans on: the hash is stable for an unchanged runtime, and it moves when a
// file the binary links changes.
func TestRuntimeHashStableAndSensitive(t *testing.T) {
	root := t.TempDir()
	writeRuntimeTree(t, root, "package value\n\nfunc A() int { return 1 }\n")

	h1, err := RuntimeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := RuntimeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not stable: %s vs %s", h1, h2)
	}

	// A _test.go file must not move the hash; it never reaches the binary.
	if err := os.WriteFile(filepath.Join(root, "pkg", "value", "value_test.go"), []byte("package value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, err := RuntimeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if h3 != h1 {
		t.Errorf("a test file changed the runtime hash: %s vs %s", h3, h1)
	}

	// A real source change must move it.
	writeRuntimeTree(t, root, "package value\n\nfunc A() int { return 2 }\n")
	h4, err := RuntimeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if h4 == h1 {
		t.Error("runtime hash did not change when pkg/value changed")
	}
}

// TestTailKeyDistinguishesGoAndMeta checks the key separates programs that would
// build to different binaries or be judged differently.
func TestTailKeyDistinguishesGoAndMeta(t *testing.T) {
	base := Job{ID: "x"}
	k := TailKey("GO", "RT", base)
	if TailKey("GO2", "RT", base) == k {
		t.Error("different emitted Go shared a key")
	}
	if TailKey("GO", "RT2", base) == k {
		t.Error("different runtime shared a key")
	}
	if TailKey("GO", "RT", Job{ID: "x", Async: true}) == k {
		t.Error("async flag did not separate the key")
	}
	if TailKey("GO", "RT", Job{ID: "x", NegType: "TypeError"}) == k {
		t.Error("negative type did not separate the key")
	}
	// The ID is not part of the key: two tests that emit the same Go share it.
	if TailKey("GO", "RT", Job{ID: "y"}) != k {
		t.Error("job ID leaked into the key")
	}
}

// TestExecuteAOTTailHitSkipsBuild proves a tail hit replays the stored verdict
// without touching the toolchain: the module root is a path where a real go
// build could not succeed, so a pass can only come from the cache.
func TestExecuteAOTTailHitSkipsBuild(t *testing.T) {
	const timeout = 5 * time.Second
	j := Job{ID: "cached", Name: "c.ts", Source: "let n: number = 1;\nconsole.log(n);"}

	// The verdict the cache will replay.
	tail := &Cache{path: filepath.Join(t.TempDir(), "tail.ndjson"), entries: map[string]cacheEntry{}}

	// Compile the source the same way ExecuteAOT will, so we can precompute the
	// key and seed the cache with a verdict.
	scratch := t.TempDir()
	entry := filepath.Join(scratch, "test262.ts")
	if err := os.WriteFile(entry, []byte(j.Source), 0o644); err != nil {
		t.Fatal(err)
	}
	goSrc, err := build.Compile(entry)
	if err != nil {
		t.Skipf("compile unavailable: %v", err)
	}
	key := TailKey(goSrc, "rt", j)
	tail.entries[key] = cacheEntry{Key: key, Status: "pass"}

	// A module root that cannot build anything: if ExecuteAOT reached go build
	// it would fail, so a pass proves the hit short-circuited the build.
	res := ExecuteAOT(j, filepath.Join(t.TempDir(), "does-not-exist"), timeout, tail, "rt")
	if res.Status != "pass" {
		t.Fatalf("tail hit should replay pass without building, got %+v", res)
	}
	if res.TailKey != key {
		t.Errorf("result carried the wrong tail key: %s", res.TailKey)
	}
}

func writeRuntimeTree(t *testing.T, root, valueGo string) {
	t.Helper()
	dir := filepath.Join(root, "pkg", "value")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value.go"), []byte(valueGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
}
