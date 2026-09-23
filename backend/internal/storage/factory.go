package storage

import "fmt"

// BackendForPool 根据池描述返回对应的 StorageBackend 实例。
//
// 池路径由调用方保证非空（StoragePool.NormalizeStoragePoolDefaults 与
// /api/storage 加载归一化路径已强制）。runner 为 nil 时所有后端退化为
// OSCommandRunner（dir 后端不使用）。
//
// 当前支持的池后端与对应实现：
//   - "dir" → DirBackend（PoolPath = 文件系统绝对路径）
//   - "zfs" → ZFSBackend（PoolPath = zfs dataset 路径）
//   - 其他 → 返回错误（未实现或识别失败不静默）
//
// 此函数不持有 runner / backend 引用，调用方负责复用与并发安全。
func BackendForPool(poolID, poolPath string, backendKind string, config map[string]string, runner CommandRunner) (StorageBackend, error) {
	switch NormalizeBackendKind(backendKind) {
	case BackendDir:
		return NewDirBackend(poolID, poolPath), nil
	case BackendZFS:
		return NewZFSBackend(poolID, poolPath, config, runner)
	default:
		return nil, fmt.Errorf("storage backend %q is not implemented", backendKind)
	}
}
