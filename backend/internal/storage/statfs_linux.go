//go:build linux

package storage

import "syscall"

// sysStatfs 取路径所在文件系统的统计信息。面板只在 Linux 上运行，
// 非 Linux 构建仅用于开发机上的单元测试（见 statfs_other.go）。
func sysStatfs(path string) (fsStat, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fsStat{}, err
	}
	return fsStat{Bsize: int64(st.Bsize), Blocks: st.Blocks, Bavail: st.Bavail}, nil
}
