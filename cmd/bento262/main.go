// bento262 runs the tc39/test262 conformance suite against bento's
// ahead-of-time path: every test is lowered to Go, compiled, and executed as
// a native binary, the same pipeline `bento build` ships.
//
// The run subcommand discovers tests under the pinned test262 checkout,
// expands them into strict and sloppy executions, fans the jobs out to worker
// subprocesses, and compares the outcome against the committed expectations
// snapshot. The worker subcommand is the internal per-process loop the run
// subcommand spawns; it is not meant to be invoked by hand.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tamnd/bento-test262/pkg/tc39"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "worker" {
		if err := tc39.WorkerMain(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "bento262 worker:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: bento262 run [flags]")
		os.Exit(2)
	}
	if err := runMain(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "bento262:", err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	root := fs.String("root", "test262", "path to the test262 checkout")
	ports := fs.String("ports", "harness", "directory of TypeScript harness ports")
	filters := fs.String("filter", "test/language,test/built-ins,test/harness", "comma-separated path prefixes to run")
	grep := fs.String("grep", "", "keep only cases whose path contains this substring, for a focused run")
	limit := fs.Int("limit", 0, "cap the run to the first N matching cases in path order (0 = no cap)")
	expPath := fs.String("expectations", "expectations/statuses.txt", "expected-statuses snapshot")
	cacheDir := fs.String("cache", ".cache", "directory for the results cache and the staged bento module")
	workers := fs.Int("jobs", max(4, goruntime.NumCPU()-2), "worker subprocesses")
	runTimeout := fs.Duration("run-timeout", 10*time.Second, "per-test binary timeout")
	jobTimeout := fs.Duration("job-timeout", 3*time.Minute, "whole-job timeout, covers a cold go build")
	update := fs.Bool("update", false, "rewrite the expectations snapshot from this run")
	verbose := fs.Bool("v", false, "print each unexpected result's error")
	lowerOnly := fs.Bool("lower-only", false,
		"lower every job in this process and report the lowered/handback split without building or running a binary; fast and disk-safe, use --jobs to bound resident memory")
	minFreeMB := fs.Int64("min-free-disk-mb", 3072,
		"abort before staging if the cache filesystem has less than this many MB free (0 = skip the check)")
	workerMaxJobs := fs.Int("worker-max-jobs", 200,
		"recycle a worker subprocess after this many jobs so its resident set cannot climb without bound (0 = never recycle)")
	denyPath := fs.String("denylist", "expectations/denylist.txt",
		"file of path substrings to quarantine (skip) entirely, one per line; for tests known to exhaust memory or wedge the toolchain")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Quarantine first, before anything spawns or lowers. A denylisted test is
	// not a failing test; it is one whose lowering or build is known to exhaust
	// memory or wedge the toolchain badly enough to threaten the machine, so it
	// must never reach a worker. Skipping it up front keeps a full run off a known
	// landmine and leaves it out of the tallies rather than recording a verdict
	// the run dared not produce.
	deny, err := tc39.LoadDenylist(*denyPath)
	if err != nil {
		return err
	}

	// Each worker holds a typescript-go checker over the bundled lib types, so
	// oversubscribing memory is what OOM-kills a run partway through. Cap the
	// worker count to what fits the machine's RAM before anything spawns, and say
	// so, rather than letting the kernel reap a worker mid-build. This guards both
	// the lower-only pass (it holds checkers too) and the full run.
	if capped, reason := tc39.CapWorkers(*workers); reason != "" {
		fmt.Fprintln(os.Stderr, "bento262:", reason)
		*workers = capped
	}

	// A worker retains checker memory across the jobs it serves, so without a
	// ceiling a long run climbs until it OOM-kills the machine (a lower-only pass
	// climbed past 8 GB in one process locally). Two guards bound it: a soft heap
	// limit makes the runtime collect before the resident set runs away, and the
	// orchestrator recycles a worker after --worker-max-jobs jobs so its peak
	// resets. Size the limit at the RAM budget for this process (the lower-only
	// pass lowers here) and at the per-worker share for the subprocesses, and let
	// GOMEMLIMIT in the environment win if the caller already pinned one.
	var memEnv []string
	memBudget := tc39.MemoryLimitBytes()
	if memBudget > 0 && os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(memBudget)
		perWorker := memBudget / int64(max(*workers, 1))
		memEnv = append(memEnv, fmt.Sprintf("BENTO262_WORKER_MEMLIMIT=%d", perWorker))
		fmt.Fprintf(os.Stderr, "bento262: soft memory limit %d MB for this process, %d MB per worker; recycling workers every %d jobs\n",
			memBudget>>20, perWorker>>20, *workerMaxJobs)
	}

	// A lower-only pass measures how far the front half gets, no go build, no
	// binary, no cache growth, so it is the cheap and disk-safe way to size a
	// front-door or lowerer change. It skips the module staging, the worker fan
	// out, and the expectations snapshot entirely: it makes no run claim, only a
	// lowering one. --jobs bounds the in-flight lowerings and with them the RAM,
	// since each holds a checker.
	if *lowerOnly {
		cases, err := tc39.Discover(*root, strings.Split(*filters, ","))
		if err != nil {
			return err
		}
		cases = tc39.Select(cases, *grep, *limit)
		cases, dropped := tc39.Quarantine(cases, deny)
		if len(dropped) > 0 {
			fmt.Printf("quarantined %d cases via %s\n", len(dropped), *denyPath)
		}
		jobs, precooked, err := tc39.Jobs(*ports, cases)
		if err != nil {
			return err
		}
		fmt.Printf("%d cases, %d jobs (%d waiting on harness ports), lower-only with %d in flight\n",
			len(cases), len(jobs)+len(precooked), len(precooked), *workers)
		rep := tc39.LowerOnly(jobs, precooked, *workers, os.Stdout)
		tc39.PrintLowerReport(os.Stdout, rep, 25)
		return nil
	}

	// A full run stages a writable bento copy and builds one native binary per
	// distinct program, so it can fill the disk. Check the free space on the
	// cache filesystem up front and refuse with a clear message rather than
	// dying with a cryptic write error deep in a go build. Skipped when the check
	// is disabled or the platform cannot report free space.
	if err := preflightDisk(*cacheDir, *minFreeMB); err != nil {
		return err
	}

	moduleRoot, bentoVersion, err := tc39.PrepareModuleRoot(*cacheDir)
	if err != nil {
		return err
	}
	fmt.Printf("bento %s, module root %s\n", bentoVersion, moduleRoot)

	prefixes := strings.Split(*filters, ",")
	cases, err := tc39.Discover(*root, prefixes)
	if err != nil {
		return err
	}
	cases = tc39.Select(cases, *grep, *limit)
	cases, dropped := tc39.Quarantine(cases, deny)
	if len(dropped) > 0 {
		fmt.Printf("quarantined %d cases via %s\n", len(dropped), *denyPath)
	}
	jobs, precooked, err := tc39.Jobs(*ports, cases)
	if err != nil {
		return err
	}
	fmt.Printf("%d cases, %d jobs (%d waiting on harness ports), %d workers\n",
		len(cases), len(jobs)+len(precooked), len(precooked), *workers)

	cache, err := tc39.LoadCache(filepath.Join(*cacheDir, "results-"+bentoVersion+".ndjson"))
	if err != nil {
		return err
	}

	// The tail cache is shared across bento versions: its file name carries no
	// version, and its key is the emitted Go plus a fingerprint of the runtime
	// the binary links. A version bump misses the results cache but hits the
	// tail cache for every test whose lowering output did not change, which
	// skips the go build and run that otherwise dominate a delta run.
	runtimeHash, err := tc39.RuntimeHash(moduleRoot)
	if err != nil {
		return err
	}
	tailPath := filepath.Join(*cacheDir, "tail.ndjson")
	tailCache, err := tc39.LoadCache(tailPath)
	if err != nil {
		return err
	}

	results, err := tc39.RunAll(jobs, tc39.RunOptions{
		Workers:          *workers,
		Timeout:          *jobTimeout,
		Progress:         os.Stderr,
		Cache:            cache,
		TailCache:        tailCache,
		BentoVersion:     bentoVersion,
		MaxJobsPerWorker: *workerMaxJobs,
		Env: append([]string{
			"BENTO_MODULE_ROOT=" + moduleRoot,
			"BENTO262_RUN_TIMEOUT=" + runTimeout.String(),
			"BENTO262_TAILCACHE=" + tailPath,
			"BENTO262_RUNTIME_HASH=" + runtimeHash,
		}, memEnv...),
	})
	if err != nil {
		return err
	}
	if err := cache.Save(); err != nil {
		return err
	}
	if err := tailCache.Save(); err != nil {
		return err
	}
	for _, r := range precooked {
		results[r.ID] = r
	}

	tc39.Summarize(os.Stdout, results)
	tc39.PrintReasons(os.Stdout, results, "handback", 25)
	tc39.PrintReasons(os.Stdout, results, "fail", 15)

	if *update {
		if err := os.MkdirAll(filepath.Dir(*expPath), 0o755); err != nil {
			return err
		}
		return tc39.WriteExpectations(*expPath, results)
	}

	exp, err := tc39.LoadExpectations(*expPath)
	if err != nil {
		return err
	}
	regressions, improvements := tc39.Diff(results, exp)
	if len(improvements) > 0 {
		fmt.Printf("\n%d improved (rerun with -update to record):\n", len(improvements))
		for _, c := range improvements {
			fmt.Printf("   %s: %s -> %s\n", c.ID, c.Want, c.Got)
		}
	}
	if len(regressions) > 0 {
		fmt.Printf("\n%d moved the wrong way:\n", len(regressions))
		for _, c := range regressions {
			fmt.Printf("   %s: %s -> %s\n", c.ID, c.Want, c.Got)
			if *verbose {
				fmt.Printf("     %s\n", results[c.ID].Error)
			}
		}
	}
	if len(regressions)+len(improvements) > 0 {
		return fmt.Errorf("%d jobs drifted from %s", len(regressions)+len(improvements), *expPath)
	}
	fmt.Println("\nsnapshot holds")
	return nil
}

// preflightDisk aborts the run when the filesystem backing the cache directory
// has less than minFreeMB free. A minFreeMB of zero disables the check, and a
// platform that cannot report free space (FreeDiskBytes returns zero) is treated
// as "unknown" and does not block the run. The cache directory is created first
// so the statfs targets the right filesystem even on a fresh checkout.
func preflightDisk(cacheDir string, minFreeMB int64) error {
	if minFreeMB <= 0 {
		return nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	free, err := tc39.FreeDiskBytes(cacheDir)
	if err != nil || free == 0 {
		return nil
	}
	need := uint64(minFreeMB) << 20
	if free < need {
		return fmt.Errorf("only %d MB free on the cache filesystem, need %d MB (lower it with -min-free-disk-mb, or 0 to skip)",
			free>>20, minFreeMB)
	}
	return nil
}
