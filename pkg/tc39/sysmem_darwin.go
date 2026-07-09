//go:build darwin

package tc39

import "golang.org/x/sys/unix"

// totalMem reports the machine's physical RAM in bytes via the hw.memsize
// sysctl. A zero return means the probe failed and the caller should not cap.
func totalMem() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
