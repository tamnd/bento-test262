package tc39

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a writer safe for the watchdog goroutine to write while the test
// reads it.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestWatchStuckNamesLongRunningJob proves the watchdog names a job that has
// outrun the threshold, which is what turns a silent stall into a pointer at the
// wedging test.
func TestWatchStuckNamesLongRunningJob(t *testing.T) {
	var inflight sync.Map
	inflight.Store("test/built-ins/Array/slow.js#strict", time.Now().Add(-time.Hour))

	var out syncBuf
	stop := make(chan struct{})
	go watchStuck(&inflight, 10*time.Millisecond, &out, stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "slow: test/built-ins/Array/slow.js#strict") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)

	if !strings.Contains(out.String(), "slow: test/built-ins/Array/slow.js#strict") {
		t.Fatalf("watchdog did not name the stuck job, got: %q", out.String())
	}
}

// TestWatchStuckIgnoresFreshJob proves a job that has not yet outrun the
// threshold is left unlogged, so the watchdog does not cry wolf on every job.
func TestWatchStuckIgnoresFreshJob(t *testing.T) {
	var inflight sync.Map
	inflight.Store("fresh.js#strict", time.Now())

	var out syncBuf
	stop := make(chan struct{})
	go watchStuck(&inflight, time.Hour, &out, stop)
	time.Sleep(50 * time.Millisecond)
	close(stop)

	if strings.Contains(out.String(), "fresh.js") {
		t.Errorf("watchdog named a fresh job: %q", out.String())
	}
}

// TestStillRunningListsInflight proves the abort report lists exactly the jobs
// left in flight, so an interrupted run says what was running when it stopped.
func TestStillRunningListsInflight(t *testing.T) {
	var inflight sync.Map
	inflight.Store("a.js#strict", time.Now())
	inflight.Store("b.js#sloppy", time.Now())

	names := stillRunning(&inflight)
	if len(names) != 2 {
		t.Fatalf("want 2 in-flight jobs, got %d: %v", len(names), names)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "a.js#strict") || !strings.Contains(got, "b.js#sloppy") {
		t.Errorf("in-flight report missing a job: %v", names)
	}
}
