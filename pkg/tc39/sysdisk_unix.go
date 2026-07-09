//go:build darwin || linux

package tc39

import "golang.org/x/sys/unix"

// FreeDiskBytes returns the bytes available to a non-root user on the filesystem
// backing path. It reports zero and no error when the platform cannot answer, so
// a caller treats "unknown" as "do not block".
func FreeDiskBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
