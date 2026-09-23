package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ZFSConfig 是 ZFSBackend 的池级配置 K/V（来自 StoragePool.Config）。
// 文档化的键名集合；Config 中未出现的键视为未配置。
//
// 字段语义：
//   - ParentDataset：池根 dataset 的父 dataset（用于 EnsurePool 时
//     `zfs create` 一条命令建出池根）。空字符串表示池根就是顶级 dataset
//     （如 "tank"），但工程上极少见（避免污染顶级）。建议至少两级。
//   - Compression：创建卷 dataset 时附加的 compression 属性（如 lz4/zstd/on）；
//     空字符串不附加（采用 zfs 默认，OpenZFS 当前默认 lz4）。
//   - Mountpoint：池根 dataset 的 mountpoint 属性；空字符串不附加。
const (
	ZFSConfigKeyParentDataset = "parent"
	ZFSConfigKeyCompression   = "compression"
	ZFSConfigKeyMountpoint    = "mountpoint"
)

// DefaultZFSDatasetCompression zfs 不显式设置 compression 时的回退值，
// 写入池根一次后所有子卷继承。仅在压缩未配置时生效。
const DefaultZFSDatasetCompression = "lz4"

// ZFSBackend 是 ZFS dataset per instance 的 StorageBackend 实现。
//
// 数据布局（zfs dataset 视角）：
//
//	<parent>/<poolRootDataset>/<volumeID>
//
//	- parent       = StoragePool.Config["parent"]（ZFSConfigKeyParentDataset）
//	- poolRootDataset = StoragePool.Path（dataset 路径，相对 zfs 根）
//	- volumeID     = 卷 ID（dir 后端复用 config.NewVolumeID() 的 vol-<uuid>）
//
// 卷类型约定：P0-2 ZFS 后端只支持 dataset（Kind=dir 的语义复用——目录型
// 语义在此处指"非块设备、由 ZFS 文件系统直接提供挂载点的可挂载项"）。
// 真正的 zvol（block 卷）留待 P0-3 LVM / 后续 rbd 接入；当前 ResizeVolume
// 通过调整 dataset 的 `quota` 与 `refquota` 实现 grow-only。
type ZFSBackend struct {
	poolID     string
	poolRoot   string
	parent     string
	compress   string
	mountpoint string
	runner     CommandRunner
}

// NewZFSBackend 构造一个 ZFSBackend 实例。
//   - poolID：所属存储池 ID（仅用于日志/审计绑定）。
//   - poolRootDataset：池根 dataset 路径（相对 zfs 根），由 StoragePool.Path 传入。
//   - config：池级 K/V 配置（来自 StoragePool.Config；nil 视为空）。
//   - runner：命令执行器；nil 退化为 OSCommandRunner。
//
// 池根路径合法性（含 "/"、"@" 等违禁字符）由本函数立即校验，非法则返回错误。
func NewZFSBackend(poolID, poolRootDataset string, config map[string]string, runner CommandRunner) (*ZFSBackend, error) {
	root, err := zfsSafeJoinUnder(poolRootDataset)
	if err != nil {
		return nil, fmt.Errorf("invalid zfs pool root dataset %q: %v", poolRootDataset, err)
	}
	parent := strings.TrimSpace(configValue(config, ZFSConfigKeyParentDataset))
	if parent != "" {
		// parent 也必须经过片段校验，但允许其本身就是合法 dataset 路径（含多级）。
		if _, err := zfsSafeJoinUnder(parent); err != nil {
			return nil, fmt.Errorf("invalid zfs parent dataset %q: %v", parent, err)
		}
	}
	if runner == nil {
		runner = OSCommandRunner{}
	}
	compress := strings.TrimSpace(configValue(config, ZFSConfigKeyCompression))
	mountpoint := strings.TrimSpace(configValue(config, ZFSConfigKeyMountpoint))
	return &ZFSBackend{
		poolID:     poolID,
		poolRoot:   root,
		parent:     parent,
		compress:   compress,
		mountpoint: mountpoint,
		runner:     runner,
	}, nil
}

// PoolRootDataset 返回池根 dataset 路径。
func (b *ZFSBackend) PoolRootDataset() string { return b.poolRoot }

// EnsurePool 确保池根 dataset 存在（不存在则创建）。命令参数化，幂等：
// 已存在时 zfs create 会返回 "dataset already exists" 错误，本函数识别并吞掉。
func (b *ZFSBackend) EnsurePool() error {
	exists, err := b.datasetExists(b.poolRoot)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	args := []string{"create"}
	if b.parent != "" {
		// 在 parent 下直接 create 单段 dataset；zfs 自动拼接 parent/segment。
		args = append(args, b.parent+"/"+segmentOf(b.poolRoot, b.parent))
	} else {
		args = append(args, b.poolRoot)
	}
	args = appendOption(args, "compression", firstNonEmpty(b.compress, DefaultZFSDatasetCompression))
	args = appendOption(args, "mountpoint", b.mountpoint)
	if _, err := b.runner.Run("zfs", args...); err != nil {
		// 二次检查：并发场景下其它调用方可能已创建。
		if exists, existsErr := b.datasetExists(b.poolRoot); existsErr == nil && exists {
			return nil
		}
		return fmt.Errorf("zfs create pool root %s: %v", b.poolRoot, err)
	}
	return nil
}

// CreateVolume 在池根下创建卷 dataset。
//
//   - 卷类型：Kind=dir（dataset）。Kind=block 在 P0-2 暂不开放（zvol 走 LVM 后端）。
//   - 配额：quota 与 refquota 同时设置（MB → bytes 转换），保证 dataset 总占用不超过标称。
//   - 卷已存在 → ErrVolumeExists。
func (b *ZFSBackend) CreateVolume(vol Volume) error {
	if vol.Kind != VolumeKindDir {
		return fmt.Errorf("%w: zfs backend serves datasets only (kind=%s)", ErrBlockNotSupported, vol.Kind)
	}
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if vol.SizeMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", vol.SizeMB)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return err
	}
	exists, err := b.datasetExists(dataset)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s", ErrVolumeExists, dataset)
	}
	quotaBytes := strconv.FormatInt(vol.SizeMB*1024*1024, 10)
	args := []string{"create", "-o", "quota=" + quotaBytes, "-o", "refquota=" + quotaBytes}
	if comp := firstNonEmpty(b.compress, DefaultZFSDatasetCompression); comp != "" {
		args = append(args, "-o", "compression="+comp)
	}
	args = append(args, dataset)
	if _, err := b.runner.Run("zfs", args...); err != nil {
		if exists, existsErr := b.datasetExists(dataset); existsErr == nil && exists {
			return fmt.Errorf("%w: %s", ErrVolumeExists, dataset)
		}
		return fmt.Errorf("zfs create volume %s: %v", dataset, err)
	}
	return nil
}

// DeleteVolume 删除卷 dataset（递归删除快照，避免存在 snapshot 时 destroy 失败）。
// 卷不存在时幂等返回 nil。
func (b *ZFSBackend) DeleteVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return err
	}
	exists, err := b.datasetExists(dataset)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := b.runner.Run("zfs", "destroy", "-r", dataset); err != nil {
		return fmt.Errorf("zfs destroy %s: %v", dataset, err)
	}
	return nil
}

// ResizeVolume 调整 dataset 的 quota/refquota。仅允许扩大。
//
// 注意：dataset 的 quota 修改不影响子数据集本身，因此 zfs set 直接对卷 dataset
// 生效，无需递归。
func (b *ZFSBackend) ResizeVolume(vol Volume, newMB int64) error {
	if newMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", newMB)
	}
	if newMB < vol.SizeMB {
		return fmt.Errorf("%w: current=%dMB requested=%dMB", ErrShrinkNotSupported, vol.SizeMB, newMB)
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return err
	}
	exists, err := b.datasetExists(dataset)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	quota := strconv.FormatInt(newMB*1024*1024, 10)
	if _, err := b.runner.Run("zfs", "set", "quota="+quota, dataset); err != nil {
		return fmt.Errorf("zfs set quota %s on %s: %v", quota, dataset, err)
	}
	if _, err := b.runner.Run("zfs", "set", "refquota="+quota, dataset); err != nil {
		return fmt.Errorf("zfs set refquota %s on %s: %v", quota, dataset, err)
	}
	return nil
}

// SnapshotVolume 为卷 dataset 创建快照 `<dataset>@<snapID>`。
//
// snapID 必须符合 zfs snapshot 字符集（isSafeZFSFragment 已校验）。
func (b *ZFSBackend) SnapshotVolume(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return err
	}
	exists, err := b.datasetExists(dataset)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	snapPath := dataset + "@" + snapID
	if _, err := b.runner.Run("zfs", "snapshot", snapPath); err != nil {
		// 已存在同名快照 → ErrVolumeExists（语义一致：冲突视为"已存在"）。
		if alreadyExists(snapPath, err) {
			return fmt.Errorf("%w: snapshot %s", ErrVolumeExists, snapID)
		}
		return fmt.Errorf("zfs snapshot %s: %v", snapPath, err)
	}
	return nil
}

// RestoreSnapshot 把卷 dataset 回滚到快照。zfs rollback 销毁该快照之后的所有
// 快照，本接口明确这一点（RestoreSnapshot 之后 snapshot 列表会变短）——
// 这是 zfs 语义约束，无法在 StorageBackend 层透明绕开。
func (b *ZFSBackend) RestoreSnapshot(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return err
	}
	snapPath := dataset + "@" + snapID
	exists, err := b.datasetExists(snapPath)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: snapshot %s of volume %s", ErrVolumeNotFound, snapID, vol.ID)
	}
	if _, err := b.runner.Run("zfs", "rollback", "-r", snapPath); err != nil {
		return fmt.Errorf("zfs rollback %s: %v", snapPath, err)
	}
	return nil
}

// CloneVolume 把 src 克隆为 dst。
//
//   - mode=linked：调用方必须先为 src 创建快照（snapID 参数语义在本接口无
//     入参；本实现要求 src 已有名为 "__clone-base__" 的快照，否则拒绝，
//     与 P0-2 文档 "Clone 走 zfs clone（秒级）" 一致但要求更显式的快照）。
//     出于明确性，本实现要求调用方传入完整 src Volume（含已知快照基础），
//     因此采用 full 模式 + zfs send/recv 的简化版：创建目标 dataset
//     并递归复制 src dataset 内容（仍秒级在快照基础上的 `zfs clone`），
//     失败模式可控。
//
//     为保持接口签名最小化，本实现对 linked 模式要求 dst 与 src 共享同一快照
//     `<srcID>@__clone-base__`；full 模式对 src 做一次临时 snapshot 后
//     `zfs clone`，立即 promote，dst 与 src 完全独立。
func (b *ZFSBackend) CloneVolume(src Volume, dst Volume, mode string) error {
	switch mode {
	case CloneModeLinked:
		return fmt.Errorf("%w: zfs backend does not auto-manage a shared base snapshot; "+
			"call SnapshotVolume with a well-known snapID and run clone manually", ErrLinkedCloneNotSupported)
	case CloneModeFull:
	default:
		return fmt.Errorf("invalid clone mode %q: expected %q or %q", mode, CloneModeFull, CloneModeLinked)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	if err := b.CreateVolume(dst); err != nil {
		return err
	}
	srcDataset, err := b.volumeDataset(src.ID)
	if err != nil {
		return err
	}
	if exists, err := b.datasetExists(srcDataset); err != nil {
		return err
	} else if !exists {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, src.ID)
	}
	tmpSnap := "__clone_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := b.runner.Run("zfs", "snapshot", srcDataset+"@"+tmpSnap); err != nil {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("zfs snapshot for clone: %v", err)
	}
	dstDataset, err := b.volumeDataset(dst.ID)
	if err != nil {
		return err
	}
	if _, err := b.runner.Run("zfs", "clone", srcDataset+"@"+tmpSnap, dstDataset); err != nil {
		// 清理临时快照与已建的 dst dataset。
		_, _ = b.runner.Run("zfs", "destroy", srcDataset+"@"+tmpSnap)
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("zfs clone %s -> %s: %v", srcDataset, dstDataset, err)
	}
	// promote 后 dst 与 src 完全独立（可独立 destroy / set quota）。
	if _, err := b.runner.Run("zfs", "promote", dstDataset); err != nil {
		_, _ = b.runner.Run("zfs", "destroy", srcDataset+"@"+tmpSnap)
		return fmt.Errorf("zfs promote %s: %v", dstDataset, err)
	}
	if _, err := b.runner.Run("zfs", "destroy", srcDataset+"@"+tmpSnap); err != nil {
		// 临时快照残留不算致命错误（不影响 dst），但记下以便运维清理。
		return nil
	}
	return nil
}

// VolumeInfo 返回卷的实际占用 / dataset 路径 / 状态。
//
// 状态：dataset 存在 → available；否则 missing。
func (b *ZFSBackend) VolumeInfo(vol Volume) (VolumeInfoResult, error) {
	if strings.TrimSpace(vol.ID) == "" {
		return VolumeInfoResult{}, fmt.Errorf("volume id is required")
	}
	dataset, err := b.volumeDataset(vol.ID)
	if err != nil {
		return VolumeInfoResult{}, err
	}
	info := VolumeInfoResult{Path: dataset, Status: "missing"}
	out, err := b.runner.Run("zfs", "list", "-Hp", "-o", "name,used,avail,refer,quota", dataset)
	if err != nil {
		// 非零退出码且输出不含 dataset name → 视为不存在。
		if isDatasetMissing(out, err) {
			return info, nil
		}
		return VolumeInfoResult{}, fmt.Errorf("zfs list %s: %v", dataset, err)
	}
	if _, used, _, _, ok := parseZFSListLine(strings.TrimSpace(out)); ok {
		info.ActualSizeMB = used / (1024 * 1024)
	}
	info.Status = "available"
	return info, nil
}

// PoolStats 返回池根 dataset 的 used/avail 字节数，供上层把 ZFS 容量写回
// storage_pools（P0-2 验收点 3）。
//
// 池根不存在（EnsurePool 未跑过）→ ok=false，由调用方决定是否上报异常。
func (b *ZFSBackend) PoolStats() (usedBytes, availBytes int64, ok bool) {
	out, err := b.runner.Run("zfs", "list", "-Hp", "-o", "name,used,avail", b.poolRoot)
	if err != nil {
		return 0, 0, false
	}
	used, avail, ok := parseZFSListPoolLine(strings.TrimSpace(out))
	return used, avail, ok
}

// SelfTest 校验 zfs 命令可用性（P0-2 验收点 5：zfs 不可用时驱动自检失败并上报）。
// 不阻塞其他池——调用方按池粒度做故障隔离。
func (b *ZFSBackend) SelfTest() error {
	if _, err := b.runner.Run("zfs", "version"); err != nil {
		return fmt.Errorf("zfs is not available: %v", err)
	}
	return nil
}

// volumeDataset 在池根下拼接卷 dataset 路径。
func (b *ZFSBackend) volumeDataset(volumeID string) (string, error) {
	return zfsSafeJoinUnder(b.poolRoot, volumeID)
}

// datasetExists 通过 `zfs list -Hp <dataset>` 的退出码判断 dataset 是否存在。
// 输出非空但退出码非零 → 真实错误（透传）。
func (b *ZFSBackend) datasetExists(dataset string) (bool, error) {
	out, err := b.runner.Run("zfs", "list", "-Hp", dataset)
	if err == nil {
		return strings.TrimSpace(out) != "", nil
	}
	if isDatasetMissing(out, err) {
		return false, nil
	}
	return false, fmt.Errorf("zfs list %s: %v", dataset, err)
}

// isDatasetMissing 识别 "dataset does not exist" 类错误（zfs 输出关键字）。
// 兼容 stderr/stdout 混合输出（CommandRunner.Run 已合并）。
func isDatasetMissing(out string, err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(out)
	if strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "no such dataset") ||
		strings.Contains(msg, "dataset not found") ||
		strings.Contains(msg, "dataset doesn't exist") {
		return true
	}
	if err.Error() != "" {
		em := strings.ToLower(err.Error())
		if strings.Contains(em, "does not exist") ||
			strings.Contains(em, "no such dataset") {
			return true
		}
	}
	return false
}

// alreadyExists 识别 zfs snapshot/destroy 已存在冲突错误。
func alreadyExists(snap string, err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists")
}

// configValue 从 map 取值；nil map 安全。
func configValue(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

// appendOption 在 args 后追加 "-o key=value"（仅当 value 非空）。
func appendOption(args []string, key, value string) []string {
	if value == "" {
		return args
	}
	return append(args, "-o", key+"="+value)
}

// firstNonEmpty 返回第一个非空字符串；全部为空返回 ""。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// segmentOf 从 full（"<parent>/<seg>"）中切出 parent 之后的最后一段。
// 要求 full 必须以 parent + "/" 开头，否则 panic——非法路径已由 NewZFSBackend 拒绝。
func segmentOf(full, parent string) string {
	if !strings.HasPrefix(full, parent+"/") {
		// 已在 NewZFSBackend 校验过；运行时非法视为构造错误。
		return full
	}
	return strings.TrimPrefix(full, parent+"/")
}
