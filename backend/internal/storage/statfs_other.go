//go:build !linux

package storage

import "errors"

// ErrStatfsUnsupported 表示当前平台没有 statfs 实现。面板只在 Linux 上运行，
// 这个分支存在的唯一目的是让 internal/storage 及其依赖方能在非 Linux 开发机上
// 编译并跑单元测试。
var ErrStatfsUnsupported = errors.New("storage: statfs unsupported on this platform")

func sysStatfs(path string) (fsStat, error) {
	return fsStat{}, ErrStatfsUnsupported
}
