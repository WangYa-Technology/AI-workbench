//go:build unix

package media

import (
	"golang.org/x/sys/unix"
	"os"
)

func stageFreeBytes(file *os.File) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
