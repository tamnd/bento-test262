package tc39

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Why the build cache grows without bound, and why a janitor is the fix.
//
// Every test262 program the harness compiles is a different Go program, so its
// main package and the binary the linker produces are unique. The Go build cache
// keys each compile and link action on the source, so a distinct program always
// mints a fresh cached linked binary, a few megabytes each. Content-addressing
// the build directory (compileGo) makes re-running the SAME program a cache hit,
// but that hit never happens in practice: on a rerun the results cache or the
// tail cache short-circuits the job before compileGo runs at all. So every one
// of the thousands of test binaries is written to the cache exactly once and
// read back never. Across a full suite that is tens of gigabytes of write-once
// garbage, and it only ever grows, which is what filled the disk.
//
// The dependency archives are the opposite: pkg/value, its transitive imports,
// and the standard-library packages are shared by every generated program, so
// they are cache hits on every build and are what make the per-test build a fast
// link step. Those we want to keep. The janitor keeps the cache under a ceiling
// by deleting the least-recently-used entries first, which sheds the cold test
// binaries while the hot dependency archives, touched on every build, survive.

// goBuildCacheBytes returns the total size of the go build cache rooted at dir.
// It sums the cache entry files under the two-hex-character subdirectories the
// toolchain lays entries out in, skipping the bookkeeping files (trim.txt,
// README, log.txt) at the root so the ceiling tracks reclaimable content.
func goBuildCacheBytes(dir string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A cache file another process removes between the walk listing it and
			// this stat is expected churn, not an error the caller should see.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !isCacheEntry(dir, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// trimGoBuildCache deletes the least-recently-used entries in the build cache at
// dir until it fits within target bytes, returning the bytes reclaimed. It only
// touches the toolchain's own entry files, ranked oldest first by modification
// time, so the recompile a deletion costs falls on the coldest program while the
// dependency archives every build touches stay resident. Deleting an entry a
// concurrent build is about to read is safe: the build treats the miss as a
// cache miss and recomputes it, and an already-open file stays valid on unix.
func trimGoBuildCache(dir string, target uint64) (uint64, error) {
	type entry struct {
		path    string
		size    int64
		modTime time.Time
	}
	var (
		entries []entry
		total   uint64
	)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !isCacheEntry(dir, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		entries = append(entries, entry{path, info.Size(), info.ModTime()})
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, err
	}
	if total <= target {
		return 0, nil
	}
	// Oldest first, so the coldest programs are shed before the dependency
	// archives that every build keeps warm.
	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.Before(entries[j].modTime) })
	var reclaimed uint64
	for _, e := range entries {
		if total-reclaimed <= target {
			break
		}
		if err := os.Remove(e.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return reclaimed, err
		}
		reclaimed += uint64(e.size)
	}
	return reclaimed, nil
}

// isCacheEntry reports whether path is one of the toolchain's cache entry files,
// which live one level below the cache root in a directory named by two hex
// characters (the first byte of the entry's hash). The root-level bookkeeping
// files (trim.txt, README, log.txt) and the module download cache never match,
// so the janitor leaves them alone.
func isCacheEntry(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	dir := filepath.Dir(rel)
	// Exactly one directory level deep, and that directory is a two-char shard.
	if filepath.Dir(dir) != "." {
		return false
	}
	return len(dir) == 2 && isHexByte(dir)
}

func isHexByte(s string) bool {
	if len(s) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// watchCache trims the build cache at dir whenever it exceeds maxBytes, on a
// tick, until stop is closed. It is the standing guard that keeps a full suite
// run from ever filling the disk again: the cache only grows by write-once test
// binaries between ticks, and each tick sheds the coldest of them back under the
// ceiling. It trims down to a fraction below the ceiling so it is not deleting a
// handful of entries on every tick. A nil w silences the progress line.
func watchCache(dir string, maxBytes uint64, every time.Duration, w io.Writer, stop <-chan struct{}) {
	if dir == "" || maxBytes == 0 {
		return
	}
	if every <= 0 {
		every = 30 * time.Second
	}
	// Trim below the ceiling so the next trim is a while off rather than every
	// tick shaving the few entries added since the last one.
	target := maxBytes - maxBytes/4
	tick := time.NewTicker(every)
	defer tick.Stop()
	trim := func() {
		size, err := goBuildCacheBytes(dir)
		if err != nil || size <= maxBytes {
			return
		}
		reclaimed, err := trimGoBuildCache(dir, target)
		if err != nil {
			if w != nil {
				fmt.Fprintf(w, "cache janitor: trim error: %v\n", err)
			}
			return
		}
		if w != nil {
			fmt.Fprintf(w, "cache janitor: build cache reached %d MB, reclaimed %d MB (ceiling %d MB)\n",
				size>>20, reclaimed>>20, maxBytes>>20)
		}
	}
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			trim()
		}
	}
}
