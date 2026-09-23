//go:build linux

package api

import "golang.org/x/sys/unix"

func getRootDiskInfo() (DiskInfo, bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return DiskInfo{}, false
	}

	total := float64(int64(stat.Blocks)*int64(stat.Bsize)) / (1024 * 1024 * 1024)
	free := float64(int64(stat.Bavail)*int64(stat.Bsize)) / (1024 * 1024 * 1024)

	return DiskInfo{
		TotalGB: total,
		UsedGB:  total - free,
		FreeGB:  free,
	}, true
}
