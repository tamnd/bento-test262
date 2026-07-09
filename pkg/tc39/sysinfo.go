package tc39

import (
	"fmt"
	"os"
	"strconv"
)

// The harness builds and runs tens of thousands of native programs, so a run
// can exhaust the disk (one linked binary per distinct program) or the RAM
// (one typescript-go checker per worker, each holding the bundled lib types).
// These helpers turn both into a preflight decision the run makes before it
// commits, so the failure mode is a clear message up front rather than a filled
// disk or an OOM-killed machine partway through.

// perWorkerMemBytes estimates the peak resident memory one worker holds during a
// full run: a typescript-go checker over the bundled lib.d.ts set, plus the
// concurrent go build and link and the test binary the same slot drives. The
// estimate is deliberately generous, calibrated so a 24 GB machine caps at the
// two workers that proved safe locally (higher counts OOM-killed it), a 16 GB
// box lands at one, and a 64 GB box gets five. A lower-only pass holds only the
// checker, so bound it higher with BENTO262_MEM_PER_WORKER_MB when measuring.
func perWorkerMemBytes() uint64 {
	const defaultMB = 6144
	mb := uint64(defaultMB)
	if v := os.Getenv("BENTO262_MEM_PER_WORKER_MB"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			mb = n
		}
	}
	return mb << 20
}

// memFraction is the share of total RAM the run is allowed to commit to workers,
// leaving the rest for the go toolchain, the OS, and the test binaries. It is
// overridable with BENTO262_MEM_FRACTION (a value in (0,1]).
func memFraction() float64 {
	const def = 0.5
	if v := os.Getenv("BENTO262_MEM_FRACTION"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return def
}

// MemSafeWorkers returns the largest worker count whose combined checker
// footprint fits in the memory budget, and the total RAM it based that on. A
// zero total means the platform did not report memory, so the caller keeps the
// requested count unchanged. The floor is one worker: a run always makes
// progress even on a tiny box.
func MemSafeWorkers() (safe int, totalRAM uint64) {
	total := totalMem()
	if total == 0 {
		return 0, 0
	}
	budget := uint64(float64(total) * memFraction())
	per := perWorkerMemBytes()
	n := max(int(budget/per), 1)
	return n, total
}

// CapWorkers bounds requested by the memory-safe worker count and reports
// whether it had to reduce it, so the caller can log the reason. When the
// platform does not report memory it returns the request unchanged.
func CapWorkers(requested int) (capped int, reason string) {
	safe, total := MemSafeWorkers()
	if safe == 0 || requested <= safe {
		return requested, ""
	}
	return safe, fmt.Sprintf("capping workers %d -> %d to fit ~%s per worker in %.0f%% of %s RAM (override with BENTO262_MEM_PER_WORKER_MB / BENTO262_MEM_FRACTION)",
		requested, safe, humanBytes(perWorkerMemBytes()), memFraction()*100, humanBytes(total))
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
