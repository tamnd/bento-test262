package tc39

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The tail cache is what keeps a delta run fast when bento changes. The results
// cache in cache.go keys every entry on the bento version, so a version bump
// misses every job and re-runs the whole suite, which is correct because a new
// compiler can change any outcome. But most slices touch one lowering path, so
// the Go emitted for the overwhelming majority of tests comes out byte-for-byte
// unchanged. The expensive part of running such a job is not the compile, which
// is now a millisecond, but the go build and the binary run that follow.
//
// The tail cache short-circuits exactly that tail. Its key is the emitted Go
// plus a fingerprint of the runtime the binary links, not the bento version, so
// a job whose lowering output did not change hits the cache across a version
// bump and skips go build and the run entirely. The key is sound because a
// binary is a pure function of its main package source and every package it
// links: identical emitted Go plus an identical pkg/value and identical pinned
// dependencies produce the identical binary and therefore the identical verdict.
// Timeouts and crashes stay out of the cache the same way they stay out of the
// results cache, so only deterministic outcomes are ever replayed.

// RuntimeHash fingerprints everything a generated binary links besides its own
// main package. Generated programs import only pkg/value, and pkg/value has no
// bento-internal dependencies of its own, so hashing its source together with
// the module's go.mod and go.sum pins both the runtime code and the exact
// versions of every third-party and standard dependency the build resolves.
func RuntimeHash(moduleRoot string) (string, error) {
	h := sha256.New()
	valueDir := filepath.Join(moduleRoot, "pkg", "value")

	var files []string
	err := filepath.WalkDir(valueDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Test files never reach the built binary, so they must not perturb the
		// fingerprint; every other file under pkg/value can.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)

	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(content)
		h.Write([]byte{0})
	}

	for _, name := range []string{"go.mod", "go.sum"} {
		content, err := os.ReadFile(filepath.Join(moduleRoot, name))
		if err != nil {
			return "", err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(content)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TailKey identifies a job by what actually determines its binary and verdict:
// the emitted Go, the runtime fingerprint, and the judge-relevant metadata. It
// deliberately omits the bento version, which is the whole point: two versions
// that emit the same Go for a test share one entry.
func TailKey(goSrc, runtimeHash string, j Job) string {
	h := sha256.New()
	h.Write([]byte(runtimeHash))
	h.Write([]byte{0})
	h.Write([]byte(j.NegType))
	h.Write([]byte{0})
	h.Write([]byte(j.NegPhase))
	h.Write([]byte{0})
	if j.Async {
		h.Write([]byte{1})
	}
	h.Write([]byte{0})
	h.Write([]byte(goSrc))
	return hex.EncodeToString(h.Sum(nil))
}
