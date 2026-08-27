package ui

import "syscall"

// diskUsage returns free and total bytes for the filesystem at path.
func diskUsage(path string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	free = int64(stat.Bavail) * stat.Bsize
	total = int64(stat.Blocks) * stat.Bsize
	return free, total, nil
}