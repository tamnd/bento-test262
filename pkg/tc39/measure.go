package tc39

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tamnd/bento/pkg/build"
	"github.com/tamnd/bento/pkg/lower"
)

// LowerReport is the outcome of a lower-only pass: how many jobs the front end
// and lowerer accepted versus declined, with the decline reasons tallied.
type LowerReport struct {
	Jobs     int
	Lowered  int
	Handback int
	Front    int
	Reasons  map[string]int
}

// LowerOnly measures how far each job gets through the AOT front half without
// ever building or running a binary. It lowers every job in this process with
// build.Compile, which type-checks and lowers to Go source and stops there, so
// the pass grows no go-build cache, writes no binary, and touches no disk beyond
// a reused scratch file. That makes it the fast, disk-safe way to measure a
// front-door or lowerer change: the number that moves is Lowered, the jobs that
// now clear the checker and lower rather than being refused.
//
// It is not a substitute for a real run. A lowered job is an upper bound on a
// passing one: the emitted Go still has to compile and behave. Use LowerOnly to
// size a lowering delta cheaply, then confirm the newly-lowered jobs with a
// normal build-and-run over a small scope.
//
// Concurrency bounds the number of in-flight lowerings, and with it the resident
// memory: each concurrent Compile holds a checker over the bundled lib types, so
// this is the knob that keeps a lower-only pass inside the box's RAM. One
// process with a small bound stays far under the many-worker run, which pays for
// one checker per worker subprocess.
func LowerOnly(jobs []Job, precooked []Result, concurrency int, out io.Writer) LowerReport {
	if concurrency < 1 {
		concurrency = 1
	}
	rep := LowerReport{Jobs: len(jobs) + len(precooked), Reasons: map[string]int{}}

	var mu sync.Mutex
	note := func(bucket, reason string) {
		mu.Lock()
		rep.Reasons[bucket+" "+reason]++
		mu.Unlock()
	}

	// A composed test whose include has no port never reaches the front end; it
	// is a handback before lowering, the same verdict a real run gives it.
	for _, r := range precooked {
		rep.Handback++
		note("PRECOOK", r.Error)
	}

	scratchDir, err := os.MkdirTemp("", "bento262-lower-*")
	if err != nil {
		fmt.Fprintf(out, "lower-only: scratch dir: %v\n", err)
		return rep
	}
	defer func() { _ = os.RemoveAll(scratchDir) }()
	// The front end canonicalizes a resolved sibling's path but takes the entry
	// root as written, so on a platform whose temp dir is a symlink (macOS points
	// /var at /private/var) a staged sibling would resolve to a path the entry
	// never matches and the import would be declined. Canonicalize the scratch
	// root up front so the entry and its siblings share one real prefix; this is a
	// no-op where the temp dir is already canonical.
	if real, err := filepath.EvalSymlinks(scratchDir); err == nil {
		scratchDir = real
	}

	var loweredN, handbackN, frontN int
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var cntMu sync.Mutex
	for i, j := range jobs {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, j Job) {
			defer wg.Done()
			defer func() { <-sem }()
			// Each goroutine owns its own subdirectory so concurrent lowerings do
			// not race on a path and a module test's siblings, which share a real
			// base name across jobs, do not collide in one flat scratch dir. The
			// entry rides in under t<i>.ts unless it is a module test that pins its
			// own base name; its imported siblings are staged beside it. No binary
			// is ever produced from any of it.
			jobDir := filepath.Join(scratchDir, fmt.Sprintf("j%d", i))
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				return
			}
			defer func() { _ = os.RemoveAll(jobDir) }()
			entry, err := stageJob(jobDir, fmt.Sprintf("t%d.ts", i), j)
			if err != nil {
				return
			}
			_, err = build.Compile(entry)
			cntMu.Lock()
			defer cntMu.Unlock()
			if err == nil {
				loweredN++
				return
			}
			if nyl, ok := errors.AsType[*lower.NotYetLowerable](err); ok {
				handbackN++
				note("LOWER", nyl.Reason)
				return
			}
			frontN++
			// Fold the per-job scratch prefix so identical front-end complaints
			// aggregate; this collapses the entry and every staged sibling to a
			// bare name at once.
			note("FRONT", firstLine(strings.ReplaceAll(err.Error(), jobDir+string(os.PathSeparator), "")))
		}(i, j)
	}
	wg.Wait()

	rep.Lowered = loweredN
	rep.Handback += handbackN
	rep.Front = frontN
	return rep
}

// PrintLowerReport writes a lower-only report as a summary line and the top
// decline reasons, so a before-and-after pair reads as a delta at a glance.
func PrintLowerReport(out io.Writer, rep LowerReport, topN int) {
	fmt.Fprintf(out, "jobs=%d lowered=%d handback=%d front=%d\n",
		rep.Jobs, rep.Lowered, rep.Handback, rep.Front)
	type kv struct {
		k string
		v int
	}
	sorted := make([]kv, 0, len(rep.Reasons))
	for k, v := range rep.Reasons {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].v != sorted[j].v {
			return sorted[i].v > sorted[j].v
		}
		return sorted[i].k < sorted[j].k
	})
	for i, s := range sorted {
		if topN > 0 && i >= topN {
			break
		}
		fmt.Fprintf(out, "%6d  %s\n", s.v, s.k)
	}
}
