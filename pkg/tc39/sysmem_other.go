//go:build !darwin && !linux

package tc39

// totalMem returns zero on platforms without a memory probe, which the caller
// reads as "unknown" and leaves the requested worker count unchanged.
func totalMem() uint64 { return 0 }
