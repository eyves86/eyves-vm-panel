package storage

import (
	"fmt"
	"strconv"
	"strings"
)

// RBD 池配置 K/V（来自 StoragePool.Config）。
//
// 字段语义：
//   - Pool：Ceph pool 名（如 rbd / cephfs_data），所有 rbd 命令的 --pool；
//   - Monitors：Ceph monitor 列表（host:port 形式），多个用逗号分隔；
//     命令参数化通过 CEPH_ARGS 环境变量注入 runner；本结构体只记录便于审计。
//   - ImagePrefix：实例 image 名前缀，避免与手工 image 冲突；最终名 = prefix + volID。
const (
	RBDConfigKeyPool        = "pool"
	RBDConfigKeyMonitors    = "monitors"
	RBDConfigKeyImagePrefix = "image_prefix"
)

// DefaultRBDImagePrefix RBD image 名前缀默认 eyves-（与手工 image 命名空间隔离）。
const DefaultRBDImagePrefix = "eyves-"

// RBDBackend 是 Ceph RBD 块设备后端。
//
// 数据布局（Ceph 视角）：
//   <pool>/<imagePrefix><volID>             RBD image（卷）
//   <pool>/<imagePrefix><volID>@<snapID>    RBD snapshot
//
// 容量控制：rbd create --size <MB>M；ResizeVolume 调 rbd resize。
// 克隆：rbd clone <src>@<snap> <dst>（依赖源快照；full 模式临时建 snapshot 并 promote）。
// 附加（Attach）：输出 libvirt <disk type='network'> XML 由调用方使用，本接口
//   暴露 ImageRef() 返回完整 RBD image spec（"pool/image"）供调用方生成 XML。
//
// 本机无 rbd 二进制；测试全 mock（runner=nil → OSCommandRunner 真实跑会失败，
// 但本接口 SelfTest 会先验证）。
type RBDBackend struct {
	poolID    string
	pool      string
	monitors  []string
	imagePrefix string
	runner    CommandRunner
}

// NewRBDBackend 构造 RBD 后端。
//   - config 必须包含 pool；image_prefix 缺省 "eyves-"；
//   - monitors 可空（Ceph 集群配置通过 CEPH_ARGS / ceph.conf 注入）；
//   - runner 为 nil 退化为 OSCommandRunner。
func NewRBDBackend(poolID string, config map[string]string, runner CommandRunner) (*RBDBackend, error) {
	pool := strings.TrimSpace(configValue(config, RBDConfigKeyPool))
	if pool == "" {
		return nil, fmt.Errorf("rbd pool %s: missing config %q", poolID, RBDConfigKeyPool)
	}
	if !isSafeZFSFragment(pool) {
		return nil, fmt.Errorf("rbd pool %s: invalid pool name %q", poolID, pool)
	}
	prefix := strings.TrimSpace(configValue(config, RBDConfigKeyImagePrefix))
	if prefix == "" {
		prefix = DefaultRBDImagePrefix
	}
	monitors := []string{}
	if raw := strings.TrimSpace(configValue(config, RBDConfigKeyMonitors)); raw != "" {
		for _, m := range strings.Split(raw, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				monitors = append(monitors, m)
			}
		}
	}
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return &RBDBackend{
		poolID:    poolID,
		pool:      pool,
		monitors:  monitors,
		imagePrefix: prefix,
		runner:    runner,
	}, nil
}

// PoolName 返回 Ceph pool 名。
func (b *RBDBackend) PoolName() string { return b.pool }

// Monitors 返回 monitor 列表。
func (b *RBDBackend) Monitors() []string { return append([]string(nil), b.monitors...) }

// EnsurePool 探测 Ceph pool 可达性（rbd pool stats）。
func (b *RBDBackend) EnsurePool() error {
	if _, err := b.runner.Run("rbd", "pool", "stats", b.pool, "--format", "json"); err != nil {
		return fmt.Errorf("rbd pool %s/%s unreachable: %v", b.poolID, b.pool, err)
	}
	return nil
}

// CreateVolume 调 rbd create --size <MB>M。
//
// Kind 必须为 block（rbd 不支持目录语义）。
func (b *RBDBackend) CreateVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if vol.Kind != VolumeKindBlock {
		return fmt.Errorf("%w: rbd backend serves block volumes only", ErrBlockNotSupported)
	}
	if vol.SizeMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", vol.SizeMB)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	imageName := b.imageName(vol.ID)
	if exists, err := b.imageExists(imageName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.pool, imageName)
	}
	args := []string{"create", "--pool", b.pool, "--size", fmt.Sprintf("%d", vol.SizeMB), imageName}
	if _, err := b.runner.Run("rbd", args...); err != nil {
		if exists, existsErr := b.imageExists(imageName); existsErr == nil && exists {
			return fmt.Errorf("%w: %s/%s", ErrVolumeExists, b.pool, imageName)
		}
		return fmt.Errorf("rbd create %s/%s: %v", b.pool, imageName, err)
	}
	return nil
}

// DeleteVolume 调 rbd rm（强制）。
func (b *RBDBackend) DeleteVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	imageName := b.imageName(vol.ID)
	if exists, err := b.imageExists(imageName); err != nil {
		return err
	} else if !exists {
		return nil
	}
	if _, err := b.runner.Run("rbd", "rm", "--pool", b.pool, imageName); err != nil {
		return fmt.Errorf("rbd rm %s/%s: %v", b.pool, imageName, err)
	}
	return nil
}

// ResizeVolume 调 rbd resize（grow-only）。
func (b *RBDBackend) ResizeVolume(vol Volume, newMB int64) error {
	if newMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", newMB)
	}
	if newMB < vol.SizeMB {
		return fmt.Errorf("%w: current=%dMB requested=%dMB", ErrShrinkNotSupported, vol.SizeMB, newMB)
	}
	imageName := b.imageName(vol.ID)
	if exists, err := b.imageExists(imageName); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	if _, err := b.runner.Run("rbd", "resize", "--pool", b.pool, "--size", fmt.Sprintf("%d", newMB), imageName); err != nil {
		return fmt.Errorf("rbd resize %s/%s: %v", b.pool, imageName, err)
	}
	return nil
}

// SnapshotVolume 调 rbd snap create。
func (b *RBDBackend) SnapshotVolume(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	imageName := b.imageName(vol.ID)
	if exists, err := b.imageExists(imageName); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
	}
	args := []string{"snap", "create", "--pool", b.pool, "--snap", snapID, imageName}
	if _, err := b.runner.Run("rbd", args...); err != nil {
		if containsAlreadyExists(err) {
			return fmt.Errorf("%w: %s/%s@%s", ErrVolumeExists, b.pool, imageName, snapID)
		}
		return fmt.Errorf("rbd snap create %s/%s@%s: %v", b.pool, imageName, snapID, err)
	}
	return nil
}

// RestoreSnapshot 调 rbd snap rollback（卷回到快照状态）。
//
// 注意：rbd snap rollback 把 image 数据回滚到快照时刻，期间 IO 必须停止
// （调用方保证：应用层先 stop container / unmount）。
func (b *RBDBackend) RestoreSnapshot(vol Volume, snapID string) error {
	if strings.TrimSpace(snapID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	imageName := b.imageName(vol.ID)
	snapFull := fmt.Sprintf("%s/%s@%s", b.pool, imageName, snapID)
	if exists, err := b.imageExists(snapFull); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: snapshot %s of %s", ErrVolumeNotFound, snapID, vol.ID)
	}
	if _, err := b.runner.Run("rbd", "snap", "rollback", snapFull); err != nil {
		return fmt.Errorf("rbd snap rollback %s: %v", snapFull, err)
	}
	return nil
}

// CloneVolume 把 src 克隆为 dst。
//
//   - mode=linked：rbd clone <src>@<snapID> <dst>；
//     本实现采用 caller-provided snapshot 语义：调用方必须先对 src 建快照（snapID 由
//     调用方决定并显式 SnapshotVolume 后调用），本接口直接拒绝依赖隐式快照。
//   - mode=full：在 src 上建临时 snapshot → clone → flatten（dst 与 src 完全独立）。
func (b *RBDBackend) CloneVolume(src Volume, dst Volume, mode string) error {
	switch mode {
	case CloneModeFull:
	case CloneModeLinked:
		return fmt.Errorf("%w: call SnapshotVolume then rbd clone manually", ErrLinkedCloneNotSupported)
	default:
		return fmt.Errorf("invalid clone mode %q", mode)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	if err := b.CreateVolume(dst); err != nil {
		return err
	}
	srcName := b.imageName(src.ID)
	if exists, err := b.imageExists(srcName); err != nil {
		return err
	} else if !exists {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, src.ID)
	}
	tmpSnap := "__clone_" + strconv.FormatInt(unixNano(), 10)
	if _, err := b.runner.Run("rbd", "snap", "create", "--pool", b.pool, "--snap", tmpSnap, srcName); err != nil {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("rbd snap create for clone: %v", err)
	}
	dstName := b.imageName(dst.ID)
	if _, err := b.runner.Run("rbd", "clone", "--pool", b.pool,
		fmt.Sprintf("%s/%s@%s", b.pool, srcName, tmpSnap), fmt.Sprintf("%s/%s", b.pool, dstName)); err != nil {
		_, _ = b.runner.Run("rbd", "snap", "rm", "--pool", b.pool,
			fmt.Sprintf("%s/%s@%s", b.pool, srcName, tmpSnap))
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("rbd clone %s -> %s: %v", srcName, dstName, err)
	}
	if _, err := b.runner.Run("rbd", "flatten", "--pool", b.pool, fmt.Sprintf("%s/%s", b.pool, dstName)); err != nil {
		_, _ = b.runner.Run("rbd", "snap", "rm", "--pool", b.pool,
			fmt.Sprintf("%s/%s@%s", b.pool, srcName, tmpSnap))
		return fmt.Errorf("rbd flatten %s/%s: %v", b.pool, dstName, err)
	}
	if _, err := b.runner.Run("rbd", "snap", "rm", "--pool", b.pool,
		fmt.Sprintf("%s/%s@%s", b.pool, srcName, tmpSnap)); err != nil {
		return nil
	}
	return nil
}

// VolumeInfo 调 rbd info 解析 size（bytes）。
func (b *RBDBackend) VolumeInfo(vol Volume) (VolumeInfoResult, error) {
	if strings.TrimSpace(vol.ID) == "" {
		return VolumeInfoResult{}, fmt.Errorf("volume id is required")
	}
	imageName := b.imageName(vol.ID)
	full := fmt.Sprintf("%s/%s", b.pool, imageName)
	info := VolumeInfoResult{Path: full, Status: "missing"}
	out, err := b.runner.Run("rbd", "info", full, "--format", "json")
	if err != nil {
		if containsImageNotFound(out, err) {
			return info, nil
		}
		return VolumeInfoResult{}, fmt.Errorf("rbd info %s: %v", full, err)
	}
	sizeMB, ok := parseRBDInfoSize(out)
	if ok {
		info.ActualSizeMB = sizeMB
	}
	info.Status = "available"
	return info, nil
}

// ImageRef 返回完整 RBD image spec（"pool/image"），供调用方生成 libvirt XML
// （<disk type='network'><source protocol='rbd' name='pool/image'>）。
func (b *RBDBackend) ImageRef(vol Volume) string {
	return fmt.Sprintf("%s/%s", b.pool, b.imageName(vol.ID))
}

// imageName 拼接 image 名前缀与卷 ID。
func (b *RBDBackend) imageName(volID string) string {
	return b.imagePrefix + volID
}

// imageExists 通过 rbd info 探测 image / image@snap 是否存在。
func (b *RBDBackend) imageExists(imageOrSnap string) (bool, error) {
	args := []string{"info"}
	if strings.Contains(imageOrSnap, "@") {
		// snapshot spec
		args = append(args, imageOrSnap)
	} else {
		args = append(args, fmt.Sprintf("%s/%s", b.pool, imageOrSnap))
	}
	out, err := b.runner.Run("rbd", args...)
	if err == nil {
		return true, nil
	}
	if containsImageNotFound(out, err) {
		return false, nil
	}
	return false, fmt.Errorf("rbd info %s: %v", imageOrSnap, err)
}

// containsAlreadyExists / containsImageNotFound 识别 rbd 错误关键字。
func containsAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists")
}

func containsImageNotFound(out string, err error) bool {
	msg := strings.ToLower(out + " " + errMessage(err))
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "image not found") ||
		strings.Contains(msg, "not found") && strings.Contains(msg, "image")
}

// parseRBDInfoSize 解析 rbd info --format json 中的 size。
// JSON 形如：{"name":"vol-1","size":1073741824,...}
//
// 简化解析：不做完整 JSON decode（避免引入依赖），仅匹配 "size":<int>。
// 多字段行内末尾的逗号/右花括号需要先剥除。
func parseRBDInfoSize(out string) (int64, bool) {
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, `"size"`)
		if idx < 0 {
			continue
		}
		rest := line[idx:]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			continue
		}
		num := rest[colon+1:]
		num = strings.TrimSpace(num)
		// 截断到第一个非数字字符（兼容尾部 ","、"}"、小数点等）。
		end := 0
		for end < len(num) && (num[end] >= '0' && num[end] <= '9') {
			end++
		}
		num = num[:end]
		if num == "" {
			continue
		}
		v, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			continue
		}
		return v / (1024 * 1024), true
	}
	return 0, false
}

// SanityCheck: RBDBackend 实现 StorageBackend。
var _ StorageBackend = (*RBDBackend)(nil)

// unixNano 返回当前纳秒时间戳，用于临时 snapshot 命名。抽到独立函数方便测试覆盖。
func unixNano() int64 {
	return strconvInt64NanoNow()
}