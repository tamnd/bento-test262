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
	if err := fs.Parse(args); err != nil {
		return err
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
		Workers:      *workers,
		Timeout:      *jobTimeout,
		Progress:     os.Stderr,
		Cache:        cache,
		TailCache:    tailCache,
		BentoVersion: bentoVersion,
		Env: []string{
			"BENTO_MODULE_ROOT=" + moduleRoot,
			"BENTO262_RUN_TIMEOUT=" + runTimeout.String(),
			"BENTO262_TAILCACHE=" + tailPath,
			"BENTO262_RUNTIME_HASH=" + runtimeHash,
		},
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
