//go:build !windows

package registry

import (
	"syscall"
)

// getDiskSpace lấy tổng dung lượng và dung lượng còn trống trên Linux / macOS qua Statfs
func getDiskSpace(path string) (totalBytes, freeBytes uint64, err error) {
	var stat syscall.Statfs_t
	err = syscall.Statfs(path, &stat)
	if err != nil {
		return 0, 0, err
	}
	// Bsize là block size của filesystem
	totalBytes = uint64(stat.Blocks) * uint64(stat.Bsize)
	freeBytes = uint64(stat.Bavail) * uint64(stat.Bsize)
	return totalBytes, freeBytes, nil
}
