//go:build !darwin && !linux

package tc39

// FreeDiskBytes returns zero on platforms without a statfs probe, which the
// caller reads as "unknown" and does not block the run on.
func FreeDiskBytes(path string) (uint64, error) { return 0, nil }
