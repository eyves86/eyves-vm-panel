//go:build linux

package agent

import "syscall"

// diskUsage 返回 path 所在文件系统的总容量与可用容量（字节）。
func diskUsage(path string) (total, free int64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize), true
}
