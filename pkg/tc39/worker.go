package tc39

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Result is one job's outcome. Status is pass, fail, handback, crash, or
// timeout; aot.go documents what each means. TailKey travels back with the
// result so the orchestrator can record the outcome in the version-independent
// tail cache; it is set for any job that made it as far as emitting Go.
type Result struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	TailKey string `json:"tk,omitempty"`
}

// asyncDone is the exact line doneprintHandle.js prints when an async test
// completes without error.
const asyncDone = "Test262:AsyncTestComplete"

// WorkerMain is the subprocess loop: one JSON job per input line, one JSON
// result per output line. Running jobs in a subprocess keeps a compiler panic
// or a runaway build from taking down the whole run; the orchestrator just
// replaces the worker. The module root and run timeout arrive by environment
// because the orchestrator sets them once for every worker it spawns.
func WorkerMain(in io.Reader, out io.Writer) error {
	root := os.Getenv("BENTO_MODULE_ROOT")
	if root == "" {
		return fmt.Errorf("worker: BENTO_MODULE_ROOT is not set")
	}
	timeout := 10 * time.Second
	if v := os.Getenv("BENTO262_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("worker: BENTO262_RUN_TIMEOUT: %w", err)
		}
		timeout = d
	}

	// The tail cache is a read-only snapshot from previous runs; the worker
	// consults it after emitting Go to skip a go build the outcome of which is
	// already known. New entries flow back to the orchestrator on each Result,
	// which owns the single writer, so no worker writes the file.
	runtimeHash := os.Getenv("BENTO262_RUNTIME_HASH")
	var tail *Cache
	if path := os.Getenv("BENTO262_TAILCACHE"); path != "" && runtimeHash != "" {
		t, err := LoadCache(path)
		if err != nil {
			return fmt.Errorf("worker: load tail cache: %w", err)
		}
		tail = t
	}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var j Job
		if err := json.Unmarshal(sc.Bytes(), &j); err != nil {
			return fmt.Errorf("worker: bad job line: %w", err)
		}
		r := ExecuteAOT(j, root, timeout, tail, runtimeHash)
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return sc.Err()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
