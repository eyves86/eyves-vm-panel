// Package storage 定义统一的存储抽象层（P0-1）：
// Volume 卷模型 + StorageBackend 后端接口。目录型存储池是第一个后端实现
// （DirBackend），ZFS/LVM/RBD/CephFS/NFS 由后续阶段（P0-2/P0-4）按同一接口接入。
// 本包不依赖项目内其他包（config/api/lxc），避免与 config 包形成导入环。
package storage

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// 存储池后端类型（对应 config.StoragePool.Backend 字段的合法取值）。
const (
	BackendDir    = "dir"    // 本地目录（P0-1 唯一实现）
	BackendZFS    = "zfs"    // P0-2
	BackendLVM    = "lvm"    // P0-3
	BackendRBD    = "rbd"    // P0-4
	BackendCephFS = "cephfs" // P0-4
	BackendNFS    = "nfs"    // P0-4
)

// 卷类型。
const (
	VolumeKindDir   = "dir"   // 目录卷（dir 后端）
	VolumeKindBlock = "block" // 块设备卷（ZFS/LVM/RBD 等后端）
)

// 卷生命周期状态：creating → available → attached → deleting。
const (
	VolumeStatusCreating  = "creating"
	VolumeStatusAvailable = "available"
	VolumeStatusAttached  = "attached"
	VolumeStatusDeleting  = "deleting"
)

// 克隆模式。
const (
	CloneModeFull   = "full"   // 完整克隆（全量复制）
	CloneModeLinked = "linked" // 链接克隆（依赖后端写时复制能力）
)

// Volume 表示存储池上的一个卷。P0-1 阶段卷用于 LXC 容器根目录；
// VolumeID 同时是 dir 后端下卷目录的名字。
type Volume struct {
	// ID 卷唯一标识（建议格式 vol-<uuid>，见 config.NewVolumeID）。
	ID string `json:"id"`
	// PoolID 所属存储池 ID（config.StoragePool.ID）。
	PoolID string `json:"pool_id"`
	// Kind 卷类型：dir（目录卷）| block（块设备卷）。
	Kind string `json:"kind"`
	// SizeMB 卷的标称容量（MB）。dir 卷为软配额；block 卷为硬配额。
	SizeMB int64 `json:"size_mb"`
	// AttachedToContainerID 挂载到的容器 ID；0 表示未挂载（可空字段）。
	AttachedToContainerID int `json:"attached_to_container_id,omitempty"`
	// Status 生命周期：creating | available | attached | deleting。
	Status string `json:"status"`
	// CreatedAt 创建时间，格式与全库统一：2006-01-02 15:04:05。
	CreatedAt string `json:"created_at"`
}

// VolumeInfoResult 是 VolumeInfo 的返回结构，描述卷在磁盘上的实际状态。
type VolumeInfoResult struct {
	// ActualSizeMB 卷的实际占用（MB，按文件大小求和近似）。
	ActualSizeMB int64 `json:"actual_size_mb"`
	// Path 卷的绝对路径（block 卷为设备路径）。
	Path string `json:"path"`
	// Status 文件系统视角的状态：available（目录存在）| missing（不存在）。
	Status string `json:"status"`
}

// 后端统一错误。调用方用 errors.Is 判断，禁止字符串匹配。
var (
	// ErrShrinkNotSupported 卷只允许扩容；缩容请求必须显式报错，禁止静默失败。
	ErrShrinkNotSupported = errors.New("volume shrink is not supported: only growing volumes is allowed")
	// ErrBlockNotSupported dir 后端只能提供目录卷。
	ErrBlockNotSupported = errors.New("block volumes are not supported by the dir backend")
	// ErrLinkedCloneNotSupported dir 后端无写时复制能力，不支持链接克隆。
	ErrLinkedCloneNotSupported = errors.New("linked clones are not supported by the dir backend")
	// ErrVolumeExists 卷/快照已存在。
	ErrVolumeExists = errors.New("volume already exists")
	// ErrVolumeNotFound 卷/快照不存在。
	ErrVolumeNotFound = errors.New("volume not found")
)

// StorageBackend 是所有存储后端必须实现的统一接口。
// 每个实例绑定一个存储池（构造函数注入池参数），接口方法不再传池信息。
type StorageBackend interface {
	// EnsurePool 确保池的基础结构就绪（dir 后端 = 建池根目录），幂等。
	EnsurePool() error
	// CreateVolume 创建卷。dir 后端创建卷目录；卷已存在返回 ErrVolumeExists。
	CreateVolume(vol Volume) error
	// DeleteVolume 删除卷（破坏性操作，调用方负责写审计日志）。卷不存在时幂等返回 nil。
	DeleteVolume(vol Volume) error
	// ResizeVolume 调整卷容量，仅允许扩大；缩小必须返回 ErrShrinkNotSupported。
	ResizeVolume(vol Volume, newMB int64) error
	// SnapshotVolume 为卷创建快照 snapID。
	SnapshotVolume(vol Volume, snapID string) error
	// RestoreSnapshot 把卷内容回滚到快照 snapID。
	RestoreSnapshot(vol Volume, snapID string) error
	// CloneVolume 把 src 克隆为 dst（mode: full | linked）。
	CloneVolume(src Volume, dst Volume, mode string) error
	// VolumeInfo 返回卷的实际大小/路径/状态。
	VolumeInfo(vol Volume) (VolumeInfoResult, error)
}

// NormalizeBackendKind 规范化后端类型：空白或未知值回退 dir
// （旧数据 Backend 为空字符串；P0-1 只有 dir 后端实现，未知值无法服务）。
func NormalizeBackendKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BackendZFS:
		return BackendZFS
	case BackendLVM:
		return BackendLVM
	case BackendRBD:
		return BackendRBD
	case BackendCephFS:
		return BackendCephFS
	case BackendNFS:
		return BackendNFS
	default:
		return BackendDir
	}
}

// CommandRunner 抽象外部命令执行，供 P0-2/P0-4 的命令型后端
// （zfs/lvm/rbd/mount）注入依赖；测试注入 mock 实现即可单测命令构造，
// 不需要真实二进制。dir 后端纯文件操作，不使用 Runner。
type CommandRunner interface {
	// Run 执行命令并返回合并后的输出；name 与 args 必须逐个传参，禁止 shell 拼接。
	Run(name string, args ...string) (string, error)
}

// OSCommandRunner 是 CommandRunner 的真实实现：直接 exec，无 shell。
type OSCommandRunner struct{}

// Run implements CommandRunner.
func (OSCommandRunner) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s failed: %v, output: %s", name, err, string(out))
	}
	return string(out), nil
}
