package tc39

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCacheEntry drops a file of size bytes into a two-hex-char shard under
// root and back-dates it, standing in for one go build cache entry of a given
// age.
func writeCacheEntry(t *testing.T, root, shard, name string, size int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, shard)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestGoBuildCacheBytesExcludesBookkeeping pins that the size the janitor tracks
// counts only the toolchain's entry files under the two-char shards, not the
// root-level bookkeeping files it must never delete.
func TestGoBuildCacheBytesExcludesBookkeeping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "trim.txt"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCacheEntry(t, root, "ab", "aaaa-d", 500, time.Minute)
	writeCacheEntry(t, root, "cd", "bbbb-d", 4_000_000, time.Hour)

	got, err := goBuildCacheBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(500 + 4_000_000); got != want {
		t.Fatalf("cache size = %d, want %d (bookkeeping must not count)", got, want)
	}
}

// TestTrimGoBuildCacheShedsColdestFirst is the guarantee the whole fix rests on:
// when the cache is over the ceiling the janitor deletes the least-recently-used
// entries first, so the cold write-once test binaries go while the hot
// dependency archives every build touches stay resident, and the root-level
// bookkeeping files are never touched.
func TestTrimGoBuildCacheShedsColdestFirst(t *testing.T) {
	root := t.TempDir()
	trim := filepath.Join(root, "trim.txt")
	if err := os.WriteFile(trim, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	hot := writeCacheEntry(t, root, "ab", "hotdep-d", 500, time.Minute)
	newish := writeCacheEntry(t, root, "12", "newbin-d", 4_000_000, time.Hour)
	old1 := writeCacheEntry(t, root, "cd", "coldbin1-d", 4_000_000, 10*time.Hour)
	old2 := writeCacheEntry(t, root, "ef", "coldbin2-d", 4_000_000, 9*time.Hour)

	reclaimed, err := trimGoBuildCache(root, 5_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(8_000_000); reclaimed != want {
		t.Fatalf("reclaimed = %d, want %d", reclaimed, want)
	}

	present := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	if present(old1) || present(old2) {
		t.Error("the two coldest entries should have been trimmed")
	}
	if !present(hot) {
		t.Error("the hot dependency archive should have survived")
	}
	if !present(newish) {
		t.Error("the recent entry under the ceiling should have survived")
	}
	if !present(trim) {
		t.Error("root bookkeeping (trim.txt) must never be deleted")
	}
}

// TestTrimGoBuildCacheNoopUnderTarget pins that a cache already within the
// ceiling is left entirely alone.
func TestTrimGoBuildCacheNoopUnderTarget(t *testing.T) {
	root := t.TempDir()
	e := writeCacheEntry(t, root, "ab", "aaaa-d", 1000, time.Hour)
	reclaimed, err := trimGoBuildCache(root, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 0 {
		t.Fatalf("reclaimed = %d, want 0 when under target", reclaimed)
	}
	if _, err := os.Stat(e); err != nil {
		t.Error("nothing should be deleted when under the ceiling")
	}
}

// TestIsCacheEntry pins that only files one shard deep count, keeping the
// janitor off the root bookkeeping files and any deeper subtrees.
func TestIsCacheEntry(t *testing.T) {
	root := "/cache"
	cases := []struct {
		path string
		want bool
	}{
		{"/cache/ab/abcdef-d", true},
		{"/cache/00/xxxx-a", true},
		{"/cache/trim.txt", false},
		{"/cache/README", false},
		{"/cache/zz/deep-d", false}, // zz is not hex
		{"/cache/ab/sub/deep-d", false},
	}
	for _, c := range cases {
		if got := isCacheEntry(root, c.path); got != c.want {
			t.Errorf("isCacheEntry(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
