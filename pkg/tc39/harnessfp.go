package tc39

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// harnessSource embeds this package's own Go files at build time. Hashing them
// gives a digest that moves whenever the harness logic that decides how a test
// is composed, lowered, run, and judged changes, and the digest travels inside
// the binary, so it is correct without any help from the build script. The .go
// files in this directory are exactly the composition and judging logic
// (compose.go, discover.go, jobs are in aot.go, worker.go, orchestrator.go);
// the harness prelude the tests actually splice in lives in the ports directory
// and is hashed separately at run time.
//
//go:embed *.go
var harnessSource embed.FS

// HarnessFingerprint returns a short digest of everything that decides a test's
// verdict but is not the bento compiler: the harness Go logic (embedded above)
// and the TypeScript prelude ports under portsDir. It exists to key the results
// cache. The results cache replays a prior verdict by (bento version, job id),
// which is sound only while the harness that produced that verdict is unchanged.
// A compose.go edit or an edited port can turn a former handback into a real
// fail (the assert-prelude change did exactly this), and without this digest in
// the key the run would replay the stale handback forever. Folding this digest
// into the cache file name starts a fresh ledger on any harness change, while a
// pure bento edit still shares the harness digest and only bumps the bento part.
//
// The tail cache does not need this: it keys on the emitted Go itself, so a
// harness change that alters composition changes the emitted Go and misses
// naturally, and a harness change that does not touch a job's emitted Go is by
// definition irrelevant to that job's build-and-run verdict.
func HarnessFingerprint(portsDir string) (string, error) {
	h := sha256.New()

	entries, err := harnessSource.ReadDir(".")
	if err != nil {
		return "", fmt.Errorf("read embedded harness source: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := harnessSource.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read embedded harness source %s: %w", name, err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}

	// The ports directory holds the ported harness (sta.ts, assert.ts, and the
	// includes) the composer splices into every test. An edit there changes what
	// the tests run against, so it must move the digest too. Walk it in a stable
	// order and fold in each .ts file's path and bytes.
	var ports []string
	err = filepath.WalkDir(portsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".ts") {
			ports = append(ports, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk ports dir %s: %w", portsDir, err)
	}
	sort.Strings(ports)
	for _, path := range ports {
		rel, err := filepath.Rel(portsDir, path)
		if err != nil {
			rel = path
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read port %s: %w", path, err)
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))[:12], nil
}
