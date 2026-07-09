//go:build linux

package tc39

import "golang.org/x/sys/unix"

// totalMem reports the machine's physical RAM in bytes via sysinfo(2). A zero
// return means the probe failed and the caller should not cap.
func totalMem() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	unit := uint64(si.Unit)
	if unit == 0 {
		unit = 1
	}
	return uint64(si.Totalram) * unit
}
