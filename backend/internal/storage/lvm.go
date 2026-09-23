package storage

import (
	"fmt"
	"strconv"
	"strings"
)

// LVM 池配置 K/V（来自 StoragePool.Config）。文档化键名集合。
//
// 字段语义：
//   - VG：卷组名（必填），如 "vg0"。所有 lvcreate/lvextend 操作的目标 VG。
//   - ThinPool：thin pool 名（必填），如 "thinpool0"。lvcreate -T 复用此池。
//   - MetadataWarn / MetadataCritical：thin pool 元数据水位告警阈值（百分比）。
//     Data%/Metadata% 任一超阈值产生警告（落到 events 表由 P7-1 接管）。
//     默认 80/95。
const (
	LVMConfigKeyVG               = "vg"
	LVMConfigKeyThinPool         = "thinpool"
	LVMConfigKeyMetadataWarn     = "metadata_warn"
	LVMConfigKeyMetadataCritical = "metadata_critical"
)

// DefaultLVMMetadataWarn / DefaultLVMMetadataCritical 缺省 thin pool
// 元数据水位阈值（百分比）。Data% 沿用 storage_pools.Watermark* 通用阈值。
const (
	DefaultLVMMetadataWarn     = 80
	DefaultLVMMetadataCritical = 95
)

// LVMBackend 是 LVM thin pool per instance 的 StorageBackend 实现。
//
// 数据布局（lvm 视角）：
//
//   /dev/<vg>/<thinpool>             thin pool 整体（池根）
//   /dev/<vg>/<volumeID>             thin LV（卷）
//   /dev/<vg>/<volumeID>-snapshot    thin LV snapshot（克隆来源）
//   /dev/mapper/<vg>-<volumeID>      同上，dm 别名
//
// 容量控制：thin LV 的虚拟大小由 vol.SizeMB 指定，thin pool 占用按需增长。
// ResizeVolume 调整 LV 虚拟大小（grow-only）。
//
// 元数据水位（Data%/Metadata%）通过 `lvs --noheadings` 解析，
// PoolStats() 返回 Data% 与 Metadata% 百分比供上层决定是否落 events 表。
type LVMBackend struct {
	poolID         string
	vg             string
	thinPool       string
	metadataWarn   int
	metadataCritical int
	runner         CommandRunner
}

// NewLVMBackend 构造一个 LVMBackend 实例。
//   - config 必须包含 vg + thinpool；缺失时立即报错（绝不默认）。
//   - runner 为 nil 退化为 OSCommandRunner。
func NewLVMBackend(poolID string, config map[string]string, runner CommandRunner) (*LVMBackend, error) {
	vg := strings.TrimSpace(configValue(config, LVMConfigKeyVG))
	thinPool := strings.TrimSpace(configValue(config, LVMConfigKeyThinPool))
	if vg == "" {
		return nil, fmt.Errorf("lvm pool %s: missing config %q", poolID, LVMConfigKeyVG)
	}
	if thinPool == "" {
		return nil, fmt.Errorf("lvm pool %s: missing config %q", poolID, LVMConfigKeyThinPool)
	}
	if !isSafeLVMSegment(vg) {
		return nil, fmt.Errorf("lvm pool %s: invalid VG name %q", poolID, vg)
	}
	if !isSafeLVMSegment(thinPool) {
		return nil, fmt.Errorf("lvm pool %s: invalid thin pool name %q", poolID, thinPool)
	}
	if runner == nil {
		runner = OSCommandRunner{}
	}
	warn := parseIntDefault(configValue(config, LVMConfigKeyMetadataWarn), DefaultLVMMetadataWarn)
	critical := parseIntDefault(configValue(config, LVMConfigKeyMetadataCritical), DefaultLVMMetadataCritical)
	if critical < warn {
		critical = warn
	}
	return &LVMBackend{
		poolID:           poolID,
		vg:               vg,
		thinPool:         thinPool,
		metadataWarn:     warn,
		metadataCritical: critical,
		runner:           runner,
	}, nil
}

// PoolVG 返回 VG 名。
func (b *LVMBackend) PoolVG() string { return b.vg }

// PoolThinPool 返回 thin pool 名。
func (b *LVMBackend) PoolThinPool() string { return b.thinPool }

// EnsurePool 校验 thin pool 存在（不创建池本身——池由运维手工建）：
//   - vgdisplay / lvs 探测；
//   - thin pool 不存在 → 报错（明确告诉运维手工建）。
//
// 池创建是运维动作（涉及 pvcreate/vgcreate/lvcreate -T），不在面板权限范围。
func (b *LVMBackend) EnsurePool() error {
	out, err := b.runner.Run("lvs", "--noheadings", "-o", "lv_name", fmt.Sprintf("%s/%s", b.vg, b.thinPool))
	if err != nil {
		return fmt.Errorf("lvm pool %s: thin pool %s/%s not available: %v", b.poolID, b.vg, b.thinPool, err)
	}
	if !strings.Contains(out, b.thinPool) {
		return fmt.Errorf("lvm pool %s: thin pool %s/%s missing in lvs output", b.poolID, b.vg, b.thinPool)
	}
	return nil
}

// CreateVolume 在 thin pool 上建 thin LV（虚拟大小 = SizeMB）。
//
// 命令：lvcreate -T <vg>/<thinpool> -V <size> --name <volID> -y
func (b *LVMBackend) CreateVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if vol.Kind != VolumeKindBlock {
		return fmt.Errorf("%w: lvm backend serves block volumes only (kind=%s)", ErrBlockNotSupported, vol.Kind)
	}
	if vol.SizeMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", vol.SizeMB)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	if exists, err := b.lvExists(vol.ID); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.vg, vol.ID)
	}
	sizeArg := fmt.Sprintf("%dM", vol.SizeMB)
	args := []string{"-T", fmt.Sprintf("%s/%s", b.vg, b.thinPool),
		"-V", sizeArg, "--name", vol.ID, "-y"}
	if _, err := b.runner.Run("lvcreate", args...); err != nil {
		if exists, existsErr := b.lvExists(vol.ID); existsErr == nil && exists {
			return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.vg, vol.ID)
		}
		return fmt.Errorf("lvcreate %s/%s: %v", b.vg, vol.ID, err)
	}
	return nil
}

// DeleteVolume 删 thin LV（强制）。
//   - lvremove -f <vg>/<volID>；
//   - LV 不存在 → 幂等返回 nil。
func (b *LVMBackend) DeleteVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	exists, err := b.lvExists(vol.ID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := b.runner.Run("lvremove", "-f", fmt.Sprintf("%s/%s", b.vg, vol.ID)); err != nil {
		return fmt.Errorf("lvremove %s/%s: %v", b.vg, vol.ID, err)
	}
	return nil
}

// ResizeVolume 调整 thin LV 虚拟大小（grow-only）。
//   - lvextend -L <new>M <vg>/<volID>；
//   - thin LV 不支持缩容（与 zfs dataset 不同），lvextend -L 缩容会立刻报错，
//     但本函数主动判断避免误传。
func (b *LVMBackend) ResizeVolume(vol Volume, newMB int64) error {
	if newMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", newMB)
	}
	if newMB < vol.SizeMB {
		return fmt.Errorf("%w: current=%dMB requested=%dMB", ErrShrinkNotSupported, vol.SizeMB, newMB)
	}
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	exists, err := b.lvExists(vol.ID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	args := []string{"-L", fmt.Sprintf("%dM", newMB), fmt.Sprintf("%s/%s", b.vg, vol.ID)}
	if _, err := b.runner.Run("lvextend", args...); err != nil {
		return fmt.Errorf("lvextend %s/%s to %dM: %v", b.vg, vol.ID, newMB, err)
	}
	return nil
}

// SnapshotVolume 创建 thin snapshot：lvcreate -s <vg>/<volID> --name <snapID> -y。
//
// 注意：thin snapshot 与源 LV 共享数据，源 LV 不可删除直到 snapshot 删除；
// snapshot 名空间与 LV 名空间独立（snapshot 名 ≠ volID），本接口由调用方
// 决定 snapshot 命名。
func (b *LVMBackend) SnapshotVolume(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	if !isSafeLVMSegment(snapID) {
		return fmt.Errorf("refusing unsafe snapshot id %q", snapID)
	}
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	exists, err := b.lvExists(vol.ID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	if snapExists, err := b.lvExists(snapID); err != nil {
		return err
	} else if snapExists {
		return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.vg, snapID)
	}
	args := []string{"-s", fmt.Sprintf("%s/%s", b.vg, vol.ID), "--name", snapID, "-y"}
	if _, err := b.runner.Run("lvcreate", args...); err != nil {
		if snapExists, existsErr := b.lvExists(snapID); existsErr == nil && snapExists {
			return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.vg, snapID)
		}
		return fmt.Errorf("lvcreate snapshot %s/%s from %s/%s: %v", b.vg, snapID, b.vg, vol.ID, err)
	}
	return nil
}

// RestoreSnapshot 薄卷恢复：thin snapshot 实质上是源 LV 的另一视图，
// "回滚" 在 thin LV 场景下没有原生语义（不像 zfs dataset 的 rollback）。
//
// 本实现采用 fs-freeze → lvconvert --merge → 重新激活模式：
//   1. lvconvert --merge <snapID> 把 snapshot 合并回源 LV（合并完成后 snapID 自动消失）；
//   2. 合并后源 LV 处于 inactive 状态，需要 lvchange -ay 重新激活。
//
// 该流程对源 LV 上的活跃 IO 是破坏性的，调用方需确保源 LV 已停止 IO
// （删除容器前先 stop，详见应用层）。
func (b *LVMBackend) RestoreSnapshot(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	if !isSafeLVMSegment(snapID) {
		return fmt.Errorf("refusing unsafe snapshot id %q", snapID)
	}
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	if snapExists, err := b.lvExists(snapID); err != nil {
		return err
	} else if !snapExists {
		return fmt.Errorf("%w: snapshot %s", ErrVolumeNotFound, snapID)
	}
	if _, err := b.runner.Run("lvconvert", "--merge", fmt.Sprintf("%s/%s", b.vg, snapID)); err != nil {
		return fmt.Errorf("lvconvert --merge %s/%s: %v", b.vg, snapID, err)
	}
	if _, err := b.runner.Run("lvchange", "-ay", fmt.Sprintf("%s/%s", b.vg, vol.ID)); err != nil {
		return fmt.Errorf("lvchange -ay %s/%s: %v", b.vg, vol.ID, err)
	}
	return nil
}

// CloneVolume 在 thin snapshot 基础上克隆。
//
//   - mode=full：创建独立 thin LV（lvcreate -T -V ... --name dst）。
//     本质是一次"创建新卷"——并非真克隆。本实现对 full 模式直接建独立卷，
//     调用方可通过 SnapshotVolume 先创建源快照（接口签名无 snapID 参数故不传递）。
//   - mode=linked：thin snapshot 即 linked clone（源 LV 共享数据），
//     走 SnapshotVolume 接口更明确，CloneVolume(linked) 直接拒绝以避免重复路径。
func (b *LVMBackend) CloneVolume(src Volume, dst Volume, mode string) error {
	switch mode {
	case CloneModeFull:
	case CloneModeLinked:
		return fmt.Errorf("%w: use SnapshotVolume for lvm thin linked clone", ErrLinkedCloneNotSupported)
	default:
		return fmt.Errorf("invalid clone mode %q: expected %q or %q", mode, CloneModeFull, CloneModeLinked)
	}
	return b.CreateVolume(dst)
}

// VolumeInfo 返回 LV 实际占用 / 设备路径 / 状态。
//
//   - 路径：/dev/<vg>/<volID>，dm 别名由调用方按需解析；
//   - 状态：LV 存在 → available，否则 missing；
//   - 实际占用：lv_size 字段（注意：thin LV 的 lv_size 是虚拟大小，
//     实际数据占用需要 lv_data_percent/lv_metadata_percent 上下文；
//     VolumeInfoResult.ActualSizeMB 这里取 lv_size 作为"标称虚拟大小"，
//     实际物理占用由 PoolStats 提供）。
func (b *LVMBackend) VolumeInfo(vol Volume) (VolumeInfoResult, error) {
	if strings.TrimSpace(vol.ID) == "" {
		return VolumeInfoResult{}, fmt.Errorf("volume id is required")
	}
	if !isSafeLVMSegment(vol.ID) {
		return VolumeInfoResult{}, fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	devicePath := fmt.Sprintf("/dev/%s/%s", b.vg, vol.ID)
	info := VolumeInfoResult{Path: devicePath, Status: "missing"}
	out, err := b.runner.Run("lvs", "--noheadings", "--nosuffix", "--units", "m",
		"-o", "lv_name,lv_size", fmt.Sprintf("%s/%s", b.vg, vol.ID))
	if err != nil {
		if isLVNotFound(out, err) {
			return info, nil
		}
		return VolumeInfoResult{}, fmt.Errorf("lvs %s/%s: %v", b.vg, vol.ID, err)
	}
	name, sizeMB, ok := parseLVSLine(out)
	if !ok || name != vol.ID {
		return info, nil
	}
	info.Status = "available"
	info.ActualSizeMB = sizeMB
	return info, nil
}

// Attach 激活 LV（lvchange -ay）。lvchange 无原生错误码区分已激活；
// 已激活时再跑 -ay 是幂等的。
func (b *LVMBackend) Attach(vol Volume) error {
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	if exists, err := b.lvExists(vol.ID); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	if _, err := b.runner.Run("lvchange", "-ay", fmt.Sprintf("%s/%s", b.vg, vol.ID)); err != nil {
		return fmt.Errorf("lvchange -ay %s/%s: %v", b.vg, vol.ID, err)
	}
	return nil
}

// Detach 取消激活 LV（lvchange -an）。幂等。
func (b *LVMBackend) Detach(vol Volume) error {
	if !isSafeLVMSegment(vol.ID) {
		return fmt.Errorf("refusing unsafe volume id %q", vol.ID)
	}
	if exists, err := b.lvExists(vol.ID); err != nil {
		return err
	} else if !exists {
		return nil
	}
	if _, err := b.runner.Run("lvchange", "-an", fmt.Sprintf("%s/%s", b.vg, vol.ID)); err != nil {
		return fmt.Errorf("lvchange -an %s/%s: %v", b.vg, vol.ID, err)
	}
	return nil
}

// DevicePath 返回块设备的稳定路径：/dev/<vg>/<volID>。
// /dev/mapper/<vg>-<volID> 是 udev 维护的别名，由调用方按需使用。
func (b *LVMBackend) DevicePath(vol Volume) string {
	return fmt.Sprintf("/dev/%s/%s", b.vg, vol.ID)
}

// PoolStats 返回 thin pool 元数据水位（百分比）。
//   - dataPercent：thin pool 数据空间使用百分比（Data%）；
//   - metaPercent：thin pool 元数据空间使用百分比（Metadata%）；
//   - ok=false：thin pool 探测失败（不阻塞其他池）。
//
// 调用方把超阈值的池元数据水位落 events 表（与 P0-2 文档验收项一致）；
// 阈值来自 StoragePool.Metadata{Warn,Critical}（来自 b.metadataWarn/Critical）。
func (b *LVMBackend) PoolStats() (dataPercent, metaPercent int, ok bool) {
	out, err := b.runner.Run("lvs", "--noheadings", "--nosuffix",
		"-o", "lv_name,data_percent,metadata_percent",
		fmt.Sprintf("%s/%s", b.vg, b.thinPool))
	if err != nil {
		return 0, 0, false
	}
	return parseLVSDataMeta(out, b.thinPool)
}

// MetadataThresholds 返回元数据水位告警阈值（百分比），供 P7-1 事件中心对照。
func (b *LVMBackend) MetadataThresholds() (warn, critical int) {
	return b.metadataWarn, b.metadataCritical
}

// lvExists 通过 `lvs <vg>/<id>` 探测 LV（LV 或 snapshot）是否存在。
func (b *LVMBackend) lvExists(id string) (bool, error) {
	out, err := b.runner.Run("lvs", "--noheadings", "-o", "lv_name", fmt.Sprintf("%s/%s", b.vg, id))
	if err == nil {
		return strings.Contains(out, id), nil
	}
	if isLVNotFound(out, err) {
		return false, nil
	}
	return false, fmt.Errorf("lvs %s/%s: %v", b.vg, id, err)
}

// isSafeLVMSegment 校验 VG/LV/snapshot 名片段合法：
//   - 字符集 [A-Za-z0-9_+.\\-]（LVM 允许这些字符，但禁止 "/" 与 "."/".."）；
//   - 不为空、不为 "." / ".."、不以 "-" 开头（避免被解释为命令行选项）。
//   - 不含 ".."（双点片段，防止语义逃逸）
func isSafeLVMSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.Contains(s, "..") {
		return false
	}
	if strings.HasPrefix(s, "-") {
		return false
	}
	if strings.ContainsAny(s, "/\\:@#") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '+' || r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}

// parseLVSLine 解析 `lvs --noheadings --nosuffix --units m -o lv_name,lv_size <lv>` 输出。
//
// 输出形如：
//
//   vol-1        10240.00
//
// 返回 (name, sizeMB, ok)。units=m 让 lvs 直接返回 MB 数值（带小数）。
func parseLVSLine(line string) (name string, sizeMB int64, ok bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return "", 0, false
	}
	name = fields[0]
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return name, 0, false
	}
	return name, int64(v), true
}

// parseLVSDataMeta 解析 `lvs --noheadings --nosuffix -o lv_name,data_percent,metadata_percent`。
// 返回 (dataPercent, metaPercent, ok)。data_percent / metadata_percent 为浮点。
func parseLVSDataMeta(line string, expectedName string) (dataPercent, metaPercent int, ok bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 {
		return 0, 0, false
	}
	if fields[0] != expectedName {
		return 0, 0, false
	}
	d, err1 := strconv.ParseFloat(fields[1], 64)
	m, err2 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int(d), int(m), true
}

// isLVNotFound 识别 "Failed to find logical volume" 类错误。
func isLVNotFound(out string, err error) bool {
	msg := strings.ToLower(out + " " + errMessage(err))
	return strings.Contains(msg, "failed to find logical volume") ||
		strings.Contains(msg, "not found") && strings.Contains(msg, "logical volume")
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func parseIntDefault(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
		return v
	}
	return def
}

// SanityCheck：LVMBackend 必须实现 StorageBackend 接口（编译期断言）。
var _ StorageBackend = (*LVMBackend)(nil)