package tc39

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RunOptions configures a suite run.
type RunOptions struct {
	// Workers is the number of subprocess workers. Zero picks a default from
	// the machine.
	Workers int
	// Timeout is the per-job wall clock budget before the worker is killed
	// and the job recorded as a timeout. It has to cover a cold go build,
	// so it is much larger than the run timeout the worker applies to the
	// test binary itself.
	Timeout time.Duration
	// Progress receives a line every few thousand jobs; nil silences it.
	Progress io.Writer
	// Cache short-circuits jobs whose outcome is already recorded for this
	// bento version; nil disables caching.
	Cache *Cache
	// TailCache records outcomes keyed on the emitted Go rather than the bento
	// version, so a delta run can replay a job whose lowering output did not
	// change without a go build. Workers consult their own read-only copy; this
	// instance is the single writer, populated from the results as they arrive.
	TailCache *Cache
	// BentoVersion keys the cache.
	BentoVersion string
	// Env is appended to each worker's environment.
	Env []string
	// MaxJobsPerWorker recycles a worker subprocess after it has served this
	// many jobs, killing it so a fresh one takes over. The typescript-go checker
	// retains per-program memory across the jobs a worker serves, so a worker's
	// resident set climbs the longer it lives; recycling bounds that peak the way
	// a request cap bounds a leaky application server. Zero disables recycling and
	// a worker lives until a job kills it.
	MaxJobsPerWorker int
	// StuckAfter names any job that has been running longer than this to Progress,
	// so a run that stalls points at the exact test wedging a worker rather than
	// going silent. It covers a cold go build with margin; zero disables the
	// watchdog.
	StuckAfter time.Duration
	// Abort stops feeding new jobs when it is closed and lets the in-flight ones
	// drain, so a caller handling an interrupt returns the partial results it has
	// instead of losing the run. Nil never aborts.
	Abort <-chan struct{}
	// GoCacheDir is the go build cache the per-test builds write to. When set
	// together with GoCacheMaxBytes a janitor keeps it under that ceiling for the
	// life of the run, so the write-once test binaries the builds accumulate can
	// never fill the disk. Empty leaves the cache unmanaged.
	GoCacheDir string
	// GoCacheMaxBytes is the ceiling the fallback janitor holds GoCacheDir under.
	// Zero disables that fallback. It is only consulted when GoCacheBaseline is
	// empty; with a baseline the pinned janitor holds the cache at the dependency
	// floor and the ceiling is moot.
	GoCacheMaxBytes uint64
	// GoCacheBaseline is the set of build-cache entry paths present after the
	// dependency archives are warmed into a freshly wiped cache and before the
	// first test build. When set, the janitor pins the cache to exactly these
	// entries, reclaiming every per-test binary a build leaves behind so the
	// footprint stays flat at the dependency floor and can never grow across a run
	// of any length. Empty falls back to the ceiling janitor.
	GoCacheBaseline map[string]struct{}
}

// RunAll executes every job across a pool of worker subprocesses and returns
// the results keyed by job ID. A worker that times out or dies is replaced
// and the run continues; only spawning failures abort.
func RunAll(jobs []Job, opts RunOptions) (map[string]Result, error) {
	if opts.Workers <= 0 {
		opts.Workers = 8
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Minute
	}

	results := make(map[string]Result, len(jobs))
	keys := map[string]string{}
	var todo []Job
	for _, j := range jobs {
		if opts.Cache == nil {
			todo = append(todo, j)
			continue
		}
		key := JobKey(j, opts.BentoVersion)
		keys[j.ID] = key
		if r, ok := opts.Cache.Get(key, j.ID); ok {
			results[j.ID] = r
			continue
		}
		todo = append(todo, j)
	}
	if opts.Progress != nil && len(results) > 0 {
		fmt.Fprintf(opts.Progress, "%d/%d jobs from cache\n", len(results), len(jobs))
	}
	if len(todo) == 0 {
		return results, nil
	}

	jobCh := make(chan Job)
	resCh := make(chan Result)
	// inflight maps a running job's ID to the time it was handed to a worker, so
	// the watchdog can name a job that has been running too long and an abort can
	// report exactly what was still running when it stopped.
	var inflight sync.Map
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerLoop(jobCh, resCh, opts.Timeout, opts.Env, opts.MaxJobsPerWorker, &inflight)
		}()
	}
	go func() {
		for _, j := range todo {
			if opts.Abort != nil {
				select {
				case <-opts.Abort:
					// Stop feeding on an interrupt and let the in-flight jobs drain, so
					// the caller returns the partial results it has already recorded.
					close(jobCh)
					wg.Wait()
					close(resCh)
					return
				case jobCh <- j:
				}
				continue
			}
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()

	start := time.Now()
	// The watchdog names any job that has outrun StuckAfter, so a stall points at
	// the test wedging a worker instead of the run going quiet. It stops when the
	// collector closes stopWatch below.
	stopWatch := make(chan struct{})
	if opts.Progress != nil && opts.StuckAfter > 0 {
		go watchStuck(&inflight, opts.StuckAfter, opts.Progress, stopWatch)
	}

	// The build cache grows by one write-once binary per test built, so a long
	// run left unattended would fill the disk. With a baseline the pinned janitor
	// reclaims every per-test binary the tick after its build finishes, holding the
	// cache flat at the warmed dependency floor so it never grows; without one it
	// falls back to trimming under the ceiling. Either shares stopWatch so it winds
	// down with the collector below.
	if opts.GoCacheDir != "" {
		switch {
		case len(opts.GoCacheBaseline) > 0:
			go watchCachePinned(opts.GoCacheDir, opts.GoCacheBaseline,
				func() time.Time { return oldestInflight(&inflight) }, 0, opts.Progress, stopWatch)
		case opts.GoCacheMaxBytes > 0:
			go watchCache(opts.GoCacheDir, opts.GoCacheMaxBytes, 0, opts.Progress, stopWatch)
		}
	}

	done := 0
	for r := range resCh {
		results[r.ID] = r
		if opts.Cache != nil {
			opts.Cache.Put(keys[r.ID], r)
		}
		if opts.TailCache != nil && r.TailKey != "" {
			opts.TailCache.Put(r.TailKey, r)
		}
		done++
		if opts.Progress != nil && done%1000 == 0 {
			fmt.Fprintf(opts.Progress, "%d/%d executed, %s elapsed\n", done, len(todo), time.Since(start).Round(time.Second))
		}
	}
	close(stopWatch)
	if opts.Progress != nil {
		if names := stillRunning(&inflight); len(names) > 0 {
			fmt.Fprintf(opts.Progress, "stopped with %d job(s) still running: %s\n", len(names), strings.Join(names, ", "))
		}
	}
	return results, nil
}

// watchStuck logs any job that has been running longer than after, on a tick,
// until stop is closed. It is the run's answer to a silent stall: a job that
// wedges a worker in a runaway build or a hang is named with its elapsed time
// rather than leaving the run looking merely slow.
func watchStuck(inflight *sync.Map, after time.Duration, w io.Writer, stop <-chan struct{}) {
	tick := time.NewTicker(after)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-tick.C:
			inflight.Range(func(k, v any) bool {
				if started, ok := v.(time.Time); ok {
					if elapsed := now.Sub(started); elapsed >= after {
						fmt.Fprintf(w, "slow: %s running %s (build or hang wedging a worker)\n", k, elapsed.Round(time.Second))
					}
				}
				return true
			})
		}
	}
}

// oldestInflight returns the start time of the job that has been running the
// longest, or the zero time when nothing is in flight. The pinned janitor uses it
// as the cutoff below which a cache entry cannot belong to a running build, so it
// never reclaims an entry a build is still reading: any entry older than the
// oldest running build was written by a build that has already finished.
func oldestInflight(inflight *sync.Map) time.Time {
	var oldest time.Time
	inflight.Range(func(_, v any) bool {
		if started, ok := v.(time.Time); ok {
			if oldest.IsZero() || started.Before(oldest) {
				oldest = started
			}
		}
		return true
	})
	return oldest
}

// stillRunning returns the IDs of the jobs left in flight, for the abort report
// that tells the caller what was running when the run stopped.
func stillRunning(inflight *sync.Map) []string {
	var names []string
	inflight.Range(func(k, _ any) bool {
		if id, ok := k.(string); ok {
			names = append(names, id)
		}
		return true
	})
	return names
}

// worker owns one subprocess and its pipes.
type worker struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner
}

func spawnWorker(env []string) (*worker, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self, "worker")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	return &worker{cmd: cmd, in: in, out: sc}, nil
}

func (w *worker) kill() {
	_ = w.in.Close()
	_ = w.cmd.Process.Kill()
	_, _ = w.cmd.Process.Wait()
}

// send runs one job on the subprocess with a deadline. ok=false means the
// worker is no longer usable and the caller must respawn it.
func (w *worker) send(j Job, timeout time.Duration) (Result, bool) {
	line, err := json.Marshal(j)
	if err != nil {
		return Result{ID: j.ID, Status: "crash", Error: err.Error()}, true
	}
	if _, err := w.in.Write(append(line, '\n')); err != nil {
		return Result{ID: j.ID, Status: "crash", Error: "worker gone: " + err.Error()}, false
	}

	type scanned struct {
		res Result
		err error
	}
	ch := make(chan scanned, 1)
	go func() {
		if !w.out.Scan() {
			err := w.out.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- scanned{err: err}
			return
		}
		var r Result
		if err := json.Unmarshal(w.out.Bytes(), &r); err != nil {
			ch <- scanned{err: err}
			return
		}
		ch <- scanned{res: r}
	}()

	select {
	case s := <-ch:
		if s.err != nil {
			return Result{ID: j.ID, Status: "crash", Error: "worker died: " + s.err.Error()}, false
		}
		return s.res, true
	case <-time.After(timeout):
		w.kill()
		return Result{ID: j.ID, Status: "timeout"}, false
	}
}

// workerLoop drains the job channel through a subprocess, replacing it whenever
// a job kills it and recycling it after maxJobs jobs so its resident set does
// not climb without bound. A worker retains checker memory across the programs
// it compiles, so a long-lived one grows until it OOM-kills the machine;
// recycling it caps that peak, at the cost of re-parsing the bundled lib once
// per fresh worker. A maxJobs of zero keeps a worker until a job kills it.
func workerLoop(jobs <-chan Job, results chan<- Result, timeout time.Duration, env []string, maxJobs int, inflight *sync.Map) {
	var w *worker
	served := 0
	defer func() {
		if w != nil {
			w.kill()
		}
	}()
	for j := range jobs {
		if w == nil {
			var err error
			w, err = spawnWorker(env)
			if err != nil {
				results <- Result{ID: j.ID, Status: "crash", Error: "spawn: " + err.Error()}
				continue
			}
			served = 0
		}
		inflight.Store(j.ID, time.Now())
		res, ok := w.send(j, timeout)
		inflight.Delete(j.ID)
		if !ok {
			w.kill()
			w = nil
			results <- res
			continue
		}
		served++
		if maxJobs > 0 && served >= maxJobs {
			w.kill()
			w = nil
		}
		results <- res
	}
}
