//go:build !linux

package api

func getRootDiskInfo() (DiskInfo, bool) {
	return DiskInfo{}, false
}
