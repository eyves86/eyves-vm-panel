//go:build !linux

package agent

// diskUsage 面板只在 Linux 上运行，这个分支存在的唯一目的是让本包能在非 Linux
// 开发机上编译并跑单元测试。
func diskUsage(path string) (total, free int64, ok bool) {
	return 0, 0, false
}
