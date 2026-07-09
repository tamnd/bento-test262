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
// link step. Those we want to keep.
//
// The root-cause fix is to pin the cache to exactly those dependency archives so
// it never grows in the first place, rather than letting it climb and trimming it
// back. The run wipes the cache, warms the dependencies, and snapshots the entry
// set that leaves behind; that snapshot is the baseline, the floor the cache is
// held at. The janitor then reclaims every entry outside the baseline each tick,
// which is precisely the per-test main package and linked binary a finished build
// left behind. The dependency archives stay by identity, so builds stay fast, and
// the footprint is flat at the dependency floor for the whole run at any scale.
// The ceiling janitor below is kept as a fallback for a run without a baseline;
// with a baseline the pinned janitor makes the ceiling moot.

// GoBuildCacheBytes reports the reclaimable size of the go build cache rooted at
// dir, the sum the janitor holds under its ceiling. The caller uses it to read
// the warm dependency floor once the module root is staged, so the ceiling can
// be sized to that floor plus a fixed headroom rather than a fixed large number.
func GoBuildCacheBytes(dir string) (uint64, error) { return goBuildCacheBytes(dir) }

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
// run from ever filling the disk: the cache only grows by write-once test
// binaries between ticks, and each tick sheds the coldest of them back under the
// ceiling. The ceiling is sized to the warm dependency floor plus a small
// headroom, so a short tick keeps the footprint flat just above the deps rather
// than letting it climb toward a distant limit. It trims down to a fraction
// below the ceiling so it is not deleting a handful of entries on every tick,
// and it trims once up front so a resumed run's leftover cache is squared away
// before the first build rather than a tick later. A nil w silences the line.
func watchCache(dir string, maxBytes uint64, every time.Duration, w io.Writer, stop <-chan struct{}) {
	if dir == "" || maxBytes == 0 {
		return
	}
	if every <= 0 {
		every = 5 * time.Second
	}
	// Trim below the ceiling so the next trim is a while off rather than every
	// tick shaving the few entries added since the last one.
	target := maxBytes - maxBytes/4
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
	trim()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			trim()
		}
	}
}

// SnapshotCacheEntries returns the set of build-cache entry files present under
// dir right now, keyed by absolute path. The caller takes this snapshot once the
// dependency archives are warmed into a freshly wiped cache and before the first
// test build, so it captures exactly the shared archives every generated program
// links. The pinned janitor treats that set as the floor the cache is held at:
// anything not in it is a per-test write-once artifact to reclaim.
func SnapshotCacheEntries(dir string) (map[string]struct{}, error) {
	set := map[string]struct{}{}
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
		set[path] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return set, nil
}

// pinToBaseline deletes every build-cache entry under dir that is not in the
// baseline set and whose modification time is before cutoff, returning the bytes
// reclaimed. Baseline entries are the warmed dependency archives, kept by path
// identity no matter how old they are, so the deps every build links always
// survive. A non-baseline entry is a per-test main package or linked binary,
// private to the one build that wrote it and never read by another, so reclaiming
// it the tick after its build finished is what keeps the cache flat at the
// dependency floor. The cutoff protects an entry a still-running build may be
// reading: the caller passes the start time of the oldest in-flight build, so an
// entry newer than that (possibly mid-build) is left alone and only the residue of
// builds that have already finished is swept.
func pinToBaseline(dir string, baseline map[string]struct{}, cutoff time.Time) (uint64, error) {
	var reclaimed uint64
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
		if _, ok := baseline[path]; ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		reclaimed += uint64(info.Size())
		return nil
	})
	if err != nil {
		return reclaimed, err
	}
	return reclaimed, nil
}

// watchCachePinned holds the build cache flat at the warmed dependency floor for
// the life of the run, so it can never grow rather than being trimmed back under a
// ceiling after the fact. On each tick it reclaims every cache entry outside the
// baseline (the dependency archives snapshotted before the first build) that
// predates every in-flight build, which is exactly the per-test binaries and main
// packages that finished builds left behind. oldestInflight returns the start time
// of the oldest running build, or the zero time when none are running, in which
// case every non-baseline entry is reclaimed. It sweeps once more on stop so an
// interrupted run does not leave residue behind. A nil w silences the line.
func watchCachePinned(dir string, baseline map[string]struct{}, oldestInflight func() time.Time, every time.Duration, w io.Writer, stop <-chan struct{}) {
	if dir == "" || len(baseline) == 0 {
		return
	}
	if every <= 0 {
		every = 5 * time.Second
	}
	sweep := func() {
		cutoff := time.Now()
		if oldestInflight != nil {
			if t := oldestInflight(); !t.IsZero() && t.Before(cutoff) {
				cutoff = t
			}
		}
		reclaimed, err := pinToBaseline(dir, baseline, cutoff)
		if err != nil {
			if w != nil {
				fmt.Fprintf(w, "cache janitor: pin error: %v\n", err)
			}
			return
		}
		if reclaimed>>20 > 0 && w != nil {
			fmt.Fprintf(w, "cache janitor: reclaimed %d MB of per-test build residue (pinned to %d dependency entries)\n",
				reclaimed>>20, len(baseline))
		}
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			sweep()
			return
		case <-tick.C:
			sweep()
		}
	}
}
