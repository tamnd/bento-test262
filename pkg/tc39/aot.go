package tc39

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/bento/pkg/build"
	"github.com/tamnd/bento/pkg/lower"
)

// goBuildParallelism is the -p value the per-test go build runs with. It
// defaults low because the harness gets its throughput from many workers, not
// from any one build's internal fan-out, and a high fan-out is what lets the
// concurrent builds spike memory enough to OOM the machine. BENTO262_GO_BUILD_P
// overrides it for a box with memory to spare.
func goBuildParallelism() int {
	const def = 2
	if v := os.Getenv("BENTO262_GO_BUILD_P"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// The harness measures bento's ahead-of-time path, not its interpreter. Each
// job goes through the same pipeline `bento build` uses: type-check and lower
// the composed test to Go in this process, compile that Go with the toolchain
// inside a writable copy of the pinned bento module, run the binary, and judge
// what happened. The statuses split the way the AOT path can decline or fail:
//
//	pass     - lowered, compiled, ran, and behaved as the test demands
//	handback - the front end or the lowerer declined the program; the honest
//	           edge of the compiled subset, a coverage gap rather than a bug
//	fail     - the AOT path claimed the program and got it wrong: the emitted
//	           Go did not compile, or the binary misbehaved
//	timeout  - the binary ran past its budget
//	crash    - the harness or a panic inside the compiler broke the job
//
// Growing pass at the expense of handback is progress; any fail is a bento
// bug to fix.

// ExecuteAOT runs one job through the AOT pipeline. moduleRoot must be a
// writable checkout of the bento module at the same version this binary links,
// which PrepareModuleRoot guarantees. When tail is non-nil the emitted Go is
// looked up in it before the go build, so a job whose lowering output has not
// changed since a previous run skips the build and the run; runtimeHash keys
// that lookup to the runtime the binary would link.
func ExecuteAOT(j Job, moduleRoot string, runTimeout time.Duration, tail *Cache, runtimeHash string) (res Result) {
	res.ID = j.ID
	defer func() {
		if p := recover(); p != nil {
			res.Status = "crash"
			res.Error = fmt.Sprintf("panic: %v", p)
		}
	}()

	scratch, err := os.MkdirTemp("", "bento262-*")
	if err != nil {
		res.Status = "crash"
		res.Error = err.Error()
		return res
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	// The generated package has to live inside the module tree so its import of
	// the value package resolves against the pinned module, but it is write-once
	// garbage the instant the job is judged: a per-job directory, torn down here
	// alongside the scratch, so the build stubs never pile up in the staged root
	// and the harness footprint stays flat across a whole suite run. The name
	// borrows the scratch's unique suffix so two workers never collide.
	buildDir := filepath.Join(moduleRoot, "bento262-build-"+filepath.Base(scratch))
	defer func() { _ = os.RemoveAll(buildDir) }()

	// The AOT front door takes TypeScript entries only, and JavaScript is
	// close enough to a syntactic subset that the composed test rides in
	// under a .ts name; where the checker disagrees with sloppy JS, the job
	// lands in handback, which is the truthful place for it today.
	entry := filepath.Join(scratch, "test262.ts")
	if err := os.WriteFile(entry, []byte(j.Source), 0o644); err != nil {
		res.Status = "crash"
		res.Error = err.Error()
		return res
	}

	goSrc, err := build.Compile(entry)
	if err != nil {
		var nyl *lower.NotYetLowerable
		if errors.As(err, &nyl) {
			res.Status = "handback"
			res.Error = "lower: " + nyl.Reason
			return res
		}
		if j.NegType != "" && (j.NegPhase == "parse" || j.NegPhase == "resolution") {
			// The test demands this source be rejected before it runs, and
			// the build rejected it. An AOT compiler's build error is its
			// early error.
			res.Status = "pass"
			return res
		}
		// The scratch path changes per job; fold it away so identical
		// front-end complaints aggregate into one reason.
		msg := strings.ReplaceAll(err.Error(), entry, "test262.ts")
		res.Status = "handback"
		res.Error = "front: " + firstLine(msg)
		return res
	}

	// The emitted Go is in hand, so the verdict is now a pure function of it and
	// the runtime it links. If a previous run already built and judged this exact
	// program, replay that outcome and skip the go build and the run, the two
	// costs that dominate a job once compiling is cheap.
	if tail != nil {
		key := TailKey(goSrc, runtimeHash, j)
		res.TailKey = key
		if hit, ok := tail.Get(key, j.ID); ok {
			hit.TailKey = key
			return hit
		}
	}

	bin, buildErr := compileGo(buildDir, goSrc, scratch)
	if buildErr != nil {
		// Emitted Go that the toolchain refuses is never acceptable: the
		// lowerer claimed this program, so this is a bento bug, not a gap.
		res.Status = "fail"
		res.Error = "gobuild: " + diagLine(buildErr.Error())
		return res
	}

	judged := judgeRun(j, bin, runTimeout)
	judged.TailKey = res.TailKey
	return judged
}

// compileGo writes the generated program into dir, a per-job package inside the
// bento module tree, and builds it. Building there is what lets the program's
// import of the value package resolve against the pinned module with no network;
// the shared GOCACHE means everything but the one main package is a cache hit
// after the first job.
//
// dir is unique per job and the caller deletes it the instant the job is judged,
// so nothing accumulates in the staged module root. Cross-run reuse of a
// content-addressed directory used to live here, but the results and tail caches
// short-circuit a rerun before compileGo is ever reached, so that reuse never
// paid off in practice; a per-job directory that is always torn down keeps the
// footprint flat instead of leaving a stub per distinct program ever built. The
// GOCACHE entry the build mints for the one main package and its linked binary
// is the only write-once residue, and the run's janitor holds that under its
// ceiling.
func compileGo(dir, goSrc, scratch string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	main := filepath.Join(dir, "main.go")
	if err := os.WriteFile(main, []byte(goSrc), 0o644); err != nil {
		return "", err
	}
	bin := filepath.Join(scratch, "test262bin")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Bound the build's internal parallelism. go build defaults -p to GOMAXPROCS,
	// so on a many-core box one build fans out to that many compile processes at
	// once, each holding a few hundred MB; with several workers each running a
	// build the transient memory multiplies into the tens of GB that can OOM the
	// machine. The harness already gets its parallelism from running many workers,
	// so each individual build needs little of its own. Keep it small and let
	// BENTO262_GO_BUILD_P override.
	// Strip the debug symbol table and DWARF and trim absolute paths. The binary
	// is built to be run once and judged, never debugged, so the symbols are dead
	// weight; dropping them cuts the linked size roughly in half, which halves the
	// write-once bytes the build cache accumulates and the janitor has to reclaim.
	cmd := exec.CommandContext(ctx, "go", "build",
		"-p", strconv.Itoa(goBuildParallelism()),
		"-trimpath", "-ldflags=-s -w",
		"-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return bin, nil
}

// judgeRun executes the compiled test and applies the test262 pass rules: a
// plain test must exit clean, an async test must also print the completion
// line, and a negative test must die mentioning the expected error.
func judgeRun(j Job, bin string, timeout time.Duration) (res Result) {
	res.ID = j.ID
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		res.Status = "timeout"
		return res
	}

	if j.NegType != "" {
		combined := stdout.String() + stderr.String()
		if err == nil {
			res.Status = "fail"
			res.Error = fmt.Sprintf("expected %s during %s, exited clean", j.NegType, j.NegPhase)
		} else if !strings.Contains(combined, j.NegType) {
			res.Status = "fail"
			res.Error = fmt.Sprintf("expected %s, got: %s", j.NegType, firstLine(strings.TrimSpace(combined)))
		} else {
			res.Status = "pass"
		}
		return res
	}

	if err != nil {
		res.Status = "fail"
		msg := firstLine(strings.TrimSpace(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		res.Error = msg
		return res
	}
	if j.Async && !strings.Contains(stdout.String(), asyncDone) {
		res.Status = "fail"
		res.Error = "async test exited without " + asyncDone
		return res
	}
	res.Status = "pass"
	return res
}

// PrepareModuleRoot stages a writable copy of the bento module this binary was
// built against and returns its path plus a version that keys the cache. The
// module cache copy is read-only and go build needs to drop scratch packages
// inside the tree, so the copy lives under dir, keyed by version so a rebuild
// against a new bento lands in a fresh root while old ones are pruned.
//
// The version is the go-list version folded together with a content fingerprint
// of the bento source. Under the local replace this repo uses to link bento in
// process, go list reports the frozen require-line pseudo-version no matter what
// the working tree says, so on its own it would key the results cache to a
// stale identity: edit a lowering, rebuild bento262, and every changed test
// would be served its old cached result. The fingerprint fixes that. Unchanged
// source keeps the same key and the whole run is a results-cache hit; a changed
// source gets a new key and re-runs, while the version-agnostic tail cache still
// skips the go build and run for every test whose emitted Go did not move.
func PrepareModuleRoot(dir string) (root string, version string, err error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}} {{.Version}}", "github.com/tamnd/bento").Output()
	if err != nil {
		return "", "", fmt.Errorf("locate bento module: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("locate bento module: unexpected go list output %q", out)
	}
	src, listVersion := fields[0], fields[1]

	fp, err := moduleFingerprint(src)
	if err != nil {
		return "", "", fmt.Errorf("fingerprint bento module: %w", err)
	}
	version = listVersion + ".h" + fp

	root = filepath.Join(dir, "bento-"+version)
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
		return root, version, nil
	}
	// A new fingerprint means a new staged root; prune older ones so a stream of
	// local edits does not pile staged module copies onto the disk.
	defer func() {
		if err == nil {
			pruneStagedRoots(dir, root, 3)
		}
	}()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	tmp := root + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := exec.Command("cp", "-R", src, tmp).Run(); err != nil {
		return "", "", fmt.Errorf("copy bento module: %w", err)
	}
	if err := exec.Command("chmod", "-R", "u+w", tmp).Run(); err != nil {
		return "", "", fmt.Errorf("unlock bento module copy: %w", err)
	}
	if err := os.Rename(tmp, root); err != nil {
		return "", "", err
	}

	// Warm the build cache on the packages every generated program imports,
	// so the per-test builds start as pure link steps instead of racing to
	// compile the runtime.
	if err := WarmDeps(root); err != nil {
		return "", "", err
	}
	return root, version, nil
}

// WarmDeps compiles the packages every generated program imports into the go
// build cache the process GOCACHE points at, so the per-test builds start as pure
// link steps against warm dependency archives instead of racing to compile the
// runtime. It is also what seeds the janitor's baseline: run against a freshly
// wiped cache it leaves behind exactly the shared dependency entries, which the
// caller snapshots as the floor to pin the cache at for the rest of the run.
//
// A generated program is a package main that links pkg/value plus a wide slice of
// the standard library (sync/atomic, bytes, encoding/binary, strconv, fmt, and so
// on) that pkg/value does not transitively pull in. Warming only pkg/value left
// those stdlib archives out of the baseline, so the janitor reclaimed them every
// tick and each per-test build recompiled them from scratch: that is a full stdlib
// compile per test instead of a link, which is both the multi-hour runtime and the
// eviction race that produced false "no such file" gobuild fails. Warming the whole
// standard library alongside pkg/value pins every archive a test build links, so
// the builds stay pure link steps and the janitor never churns them. The cost is a
// one-time bounded warm (the stdlib archive set is fixed, it does not grow across
// the run), so disk stays flat.
//
// The warm must build with the same flags the per-test build uses, because a flag
// that feeds the compile action id gives a package a different cache key. The
// per-test build passes -trimpath, which changes every package's action id,
// stdlib included, so a warm without it pins archives the per-test builds never
// look up: the build then recompiles the stdlib under the trimpath key and the
// janitor reclaims it, which is what left slices, math/big, time, and the other
// heavier-stdlib tests failing with "could not import cmp ... no such file" even
// after the plain std warm landed. -ldflags only affects the final link and
// produces no compile archive, so it is not needed here.
func WarmDeps(root string) error {
	std := exec.Command("go", "build", "-trimpath", "std")
	std.Dir = root
	std.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := std.CombinedOutput(); err != nil {
		return fmt.Errorf("warm standard library build cache: %v\n%s", err, out)
	}

	warm := exec.Command("go", "build", "-trimpath", "./pkg/value/...")
	warm.Dir = root
	warm.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := warm.CombinedOutput(); err != nil {
		return fmt.Errorf("warm bento build cache: %v\n%s", err, out)
	}
	return nil
}

// moduleFingerprint hashes the bento source that can change what lowering emits:
// every non-test Go file plus go.mod, in the deterministic order WalkDir visits
// them. Test files and testdata are skipped because they never reach a generated
// program, and build artifacts do not live in the source tree. The result is a
// short hex digest that is stable for an unchanged tree and moves on any edit.
func moduleFingerprint(src string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "go.mod" && (!strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go")) {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// SweepBuildDirs removes any per-job build package left behind under root. In the
// normal path ExecuteAOT deletes its build directory the instant the job is
// judged, so none survive. The one exception is a worker killed mid-build on a
// timeout: the kill is a SIGKILL, so the deferred cleanup never runs and that one
// directory is orphaned. Left alone across many runs those orphans are what piled
// thousands of dead stubs into the staged root. Sweeping them when the module
// root is prepared means a run always starts clean and the count can never climb.
func SweepBuildDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "bento262-build-") {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// pruneStagedRoots keeps the newest keep staged bento module roots under dir and
// removes the rest, so the content-addressed roots a run of local edits produces
// do not accumulate on the disk. The just-staged keepRoot is always retained.
func pruneStagedRoots(dir, keepRoot string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type staged struct {
		path    string
		modTime int64
	}
	var roots []staged
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "bento-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		roots = append(roots, staged{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	if len(roots) <= keep {
		return
	}
	// Newest first, so the just-staged keepRoot sorts into the retained head.
	sort.Slice(roots, func(i, j int) bool { return roots[i].modTime > roots[j].modTime })
	for i, r := range roots {
		if i < keep || r.path == keepRoot {
			continue
		}
		_ = os.RemoveAll(r.path)
	}
}
