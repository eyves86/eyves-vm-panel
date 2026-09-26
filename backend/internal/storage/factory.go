package storage

import "fmt"

// BackendForPool 根据池描述返回对应的 StorageBackend 实例。
//
// 池路径由调用方保证非空（StoragePool.NormalizeStoragePoolDefaults 与
// /api/storage 加载归一化路径已强制）。runner 为 nil 时所有后端退化为
// OSCommandRunner（dir 后端不使用）。
//
// 当前支持的池后端与对应实现：
//   - "dir"    → DirBackend（PoolPath = 文件系统绝对路径）
//   - "zfs"    → ZFSBackend（PoolPath = zfs dataset 路径）
//   - "lvm"    → LVMBackend（poolPath 忽略；池元数据来自 Config[vg]+[thinpool]）
//   - "rbd"    → RBDBackend（poolPath 忽略；池元数据来自 Config[pool]）
//   - "cephfs" → 暂未实现（共享语义近 NFS；后续 Phase 处理）
//   - "nfs"    → NFSBackend（PoolPath = 本地挂载点；池元数据来自 Config[server]+[export]）
//   - 其他    → 返回错误（未实现或识别失败不静默）
//
// 此函数不持有 runner / backend 引用，调用方负责复用与并发安全。
func BackendForPool(poolID, poolPath string, backendKind string, config map[string]string, runner CommandRunner) (StorageBackend, error) {
	switch NormalizeBackendKind(backendKind) {
	case BackendDir:
		return NewDirBackend(poolID, poolPath), nil
	case BackendZFS:
		return NewZFSBackend(poolID, poolPath, config, runner)
	case BackendLVM:
		return NewLVMBackend(poolID, config, runner)
	case BackendRBD:
		return NewRBDBackend(poolID, config, runner)
	case BackendNFS:
		return NewNFSBackend(poolID, poolPath, config, runner)
	default:
		return nil, fmt.Errorf("storage backend %q is not implemented", backendKind)
	}
}
