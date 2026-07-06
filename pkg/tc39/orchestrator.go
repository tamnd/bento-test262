package tc39

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// BentoVersion keys the cache.
	BentoVersion string
	// Env is appended to each worker's environment.
	Env []string
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
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerLoop(jobCh, resCh, opts.Timeout, opts.Env)
		}()
	}
	go func() {
		for _, j := range todo {
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()

	start := time.Now()
	done := 0
	for r := range resCh {
		results[r.ID] = r
		if opts.Cache != nil {
			opts.Cache.Put(keys[r.ID], r)
		}
		done++
		if opts.Progress != nil && done%1000 == 0 {
			fmt.Fprintf(opts.Progress, "%d/%d executed, %s elapsed\n", done, len(todo), time.Since(start).Round(time.Second))
		}
	}
	return results, nil
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

// workerLoop drains the job channel through a subprocess, replacing it
// whenever a job kills it.
func workerLoop(jobs <-chan Job, results chan<- Result, timeout time.Duration, env []string) {
	var w *worker
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
		}
		res, ok := w.send(j, timeout)
		if !ok {
			w.kill()
			w = nil
		}
		results <- res
	}
}
