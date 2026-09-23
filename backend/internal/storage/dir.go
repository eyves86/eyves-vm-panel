package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirBackend 目录型存储池后端：卷 = 池内真实目录。
//
// 路径布局（P0-1 有意沿用现有规则，保证行为完全不变）：
//   - 卷目录：<poolPath>/lxc/<volumeID> —— 与存量容器目录 <pool>/lxc/<lxcName>
//     同区存放，因此 /api/storage 的 lxc 内容用量统计（du <pool>/lxc）和
//     容器清理白名单（lxc 包的 lxcStorageTargetAllowed 只信任 <pool>/lxc/*）零改动；
//   - 卷快照：<poolPath>/snapshots/<volumeID>/<snapID> —— 与现有容器快照
//     （<pool>/snapshots/<containerID>/<snapID>）同区但目录名不冲突
//     （卷 ID 为 vol-* 前缀，容器目录为纯数字 ID）。
//
// P0-1 阶段卷仅承载 LXC 容器根目录；后续接入其他内容类型时再按内容分目录。
type DirBackend struct {
	poolID   string
	poolPath string
}

// NewDirBackend 创建目录后端实例。poolPath 必须是绝对路径。
func NewDirBackend(poolID, poolPath string) *DirBackend {
	return &DirBackend{poolID: poolID, poolPath: filepath.Clean(poolPath)}
}

// PoolPath 返回池根目录（绝对路径，未解析符号链接）。
func (b *DirBackend) PoolPath() string { return b.poolPath }

// EnsurePool 确保池根目录存在（幂等）。
func (b *DirBackend) EnsurePool() error {
	if !filepath.IsAbs(b.poolPath) {
		return fmt.Errorf("storage pool path must be absolute: %s", b.poolPath)
	}
	if err := os.MkdirAll(b.poolPath, 0755); err != nil {
		return fmt.Errorf("failed to ensure storage pool %s: %v", b.poolPath, err)
	}
	info, err := os.Stat(b.poolPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("storage pool path is not a directory: %s", b.poolPath)
	}
	return nil
}

// CreateVolume 创建目录卷；目录已存在（含符号链接占位）时拒绝。
func (b *DirBackend) CreateVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if vol.Kind != VolumeKindDir {
		// dir 池只能做目录卷；块设备卷需要 ZFS/LVM/RBD 等后端（P0-2+）。
		return fmt.Errorf("%w: volume %s kind=%s", ErrBlockNotSupported, vol.ID, vol.Kind)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	path, err := b.volumePath(vol.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrVolumeExists, path)
	}
	// 父目录（<pool>/lxc）允许缺失，由后端自行补齐；路径已经过 safeJoinUnder
	// 校验，池内中间目录不会构成逃逸。卷目录本身不存在（上面 Lstat 已保证），
	// 用 Mkdir 保持竞态时的 EEXIST 兜底。
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to ensure volume parent directory %s: %v", filepath.Dir(path), err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrVolumeExists, path)
		}
		return fmt.Errorf("failed to create volume directory %s: %v", path, err)
	}
	return nil
}

// DeleteVolume 删除卷目录（幂等）。调用方负责审计日志。
func (b *DirBackend) DeleteVolume(vol Volume) error {
	if strings.TrimSpace(vol.ID) == "" {
		return fmt.Errorf("volume id is required")
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	path, err := b.volumePath(vol.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to delete volume directory %s: %v", path, err)
	}
	return nil
}

// ResizeVolume dir 卷为精置备软配额，扩容只需记录新配额（实际占用由文件系统
// 按需分配，LXC 的硬限额由 rootfs.img 机制另行控制）；缩小一律显式拒绝。
func (b *DirBackend) ResizeVolume(vol Volume, newMB int64) error {
	if newMB <= 0 {
		return fmt.Errorf("invalid volume size: %d MB", newMB)
	}
	if newMB < vol.SizeMB {
		return fmt.Errorf("%w: current=%dMB requested=%dMB", ErrShrinkNotSupported, vol.SizeMB, newMB)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	path, err := b.volumePath(vol.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrVolumeNotFound, vol.ID)
		}
		return err
	}
	// 扩容无需文件系统操作（dir 卷不占预分配空间），成功返回。
	return nil
}

// SnapshotVolume 把卷目录完整复制到 <pool>/snapshots/<volID>/<snapID>/。
func (b *DirBackend) SnapshotVolume(vol Volume, snapID string) error {
	snapPath, err := b.snapshotPath(vol, snapID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(snapPath); err == nil {
		return fmt.Errorf("%w: snapshot %s of volume %s", ErrVolumeExists, snapID, vol.ID)
	}
	if err := os.MkdirAll(snapPath, 0700); err != nil {
		return fmt.Errorf("failed to create snapshot directory %s: %v", snapPath, err)
	}
	srcPath, err := b.volumePath(vol.ID)
	if err != nil {
		return err
	}
	if err := copyTree(srcPath, snapPath); err != nil {
		_ = os.RemoveAll(snapPath)
		return fmt.Errorf("failed to snapshot volume %s: %v", vol.ID, err)
	}
	return nil
}

// RestoreSnapshot 将卷内容回滚到快照。采用“先备后换”顺序，失败时可回滚，
// 快照本体在任何失败分支都保持完好。
func (b *DirBackend) RestoreSnapshot(vol Volume, snapID string) error {
	snapPath, err := b.snapshotPath(vol, snapID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(snapPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: snapshot %s of volume %s", ErrVolumeNotFound, snapID, vol.ID)
		}
		return err
	}
	volPath, err := b.volumePath(vol.ID)
	if err != nil {
		return err
	}
	// 1. 快照内容先复制到池内临时目录（与卷同池，rename 原子且同文件系统）。
	tmpPath := snapPath + ".restore-tmp"
	if err := os.RemoveAll(tmpPath); err != nil {
		return err
	}
	if err := copyTree(snapPath, tmpPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return fmt.Errorf("failed to stage snapshot restore: %v", err)
	}
	// 2. 当前卷目录挪到备份位（不存在则跳过）。
	trashPath := snapPath + ".restore-old"
	_ = os.RemoveAll(trashPath)
	hadVolume := false
	if _, err := os.Lstat(volPath); err == nil {
		if err := os.Rename(volPath, trashPath); err != nil {
			_ = os.RemoveAll(tmpPath)
			return fmt.Errorf("failed to move current volume aside: %v", err)
		}
		hadVolume = true
	}
	// 3. 临时目录顶替卷目录；失败则把原卷目录放回。
	if err := os.Rename(tmpPath, volPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		if hadVolume {
			if backErr := os.Rename(trashPath, volPath); backErr != nil {
				return fmt.Errorf("restore failed (%v) and rollback failed too (%v); volume data preserved at %s", err, backErr, trashPath)
			}
		}
		return fmt.Errorf("failed to restore snapshot %s: %v", snapID, err)
	}
	_ = os.RemoveAll(trashPath)
	return nil
}

// CloneVolume 把 src 完整克隆为 dst。dir 后端只支持 full 模式。
func (b *DirBackend) CloneVolume(src Volume, dst Volume, mode string) error {
	switch mode {
	case CloneModeFull:
	case CloneModeLinked:
		// 链接克隆依赖写时复制（ZFS clone / LVM thin），dir 后端不支持。
		return fmt.Errorf("%w: volume %s", ErrLinkedCloneNotSupported, src.ID)
	default:
		return fmt.Errorf("invalid clone mode %q: expected %q or %q", mode, CloneModeFull, CloneModeLinked)
	}
	if err := b.CreateVolume(dst); err != nil {
		return err
	}
	srcPath, err := b.volumePath(src.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrVolumeNotFound, src.ID)
		}
		return err
	}
	dstPath, err := b.volumePath(dst.ID)
	if err != nil {
		return err
	}
	if err := copyTree(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to clone volume %s to %s: %v", src.ID, dst.ID, err)
	}
	return nil
}

// VolumeInfo 返回卷的实际大小 / 路径 / 状态。
func (b *DirBackend) VolumeInfo(vol Volume) (VolumeInfoResult, error) {
	if strings.TrimSpace(vol.ID) == "" {
		return VolumeInfoResult{}, fmt.Errorf("volume id is required")
	}
	if err := b.EnsurePool(); err != nil {
		return VolumeInfoResult{}, err
	}
	path, err := b.volumePath(vol.ID)
	if err != nil {
		return VolumeInfoResult{}, err
	}
	info := VolumeInfoResult{Path: path, Status: "missing"}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return info, nil
		}
		return VolumeInfoResult{}, err
	}
	info.Status = "available"
	info.ActualSizeMB = dirSizeMB(path)
	return info, nil
}

// volumePath 计算并校验卷目录路径（必须在池根目录内）。
func (b *DirBackend) volumePath(volumeID string) (string, error) {
	return safeJoinUnder(b.poolPath, "lxc", volumeID)
}

// snapshotPath 计算并校验卷快照目录路径。
func (b *DirBackend) snapshotPath(vol Volume, snapID string) (string, error) {
	if strings.TrimSpace(snapID) == "" {
		return "", fmt.Errorf("snapshot id is required")
	}
	if err := b.EnsurePool(); err != nil {
		return "", err
	}
	return safeJoinUnder(b.poolPath, "snapshots", vol.ID, snapID)
}

// safeJoinUnder 在 base 下逐级拼接 parts 并做严格安全校验：
//  1. base 必须是已存在的绝对路径（调用前先 EnsurePool）；
//  2. 片段必须是非空、非 "."/".."、不含路径分隔符的普通名字——
//     因此 "../" 逃逸与绝对路径拼接在片段层即被拒绝；
//  3. 已存在的中间片段若是符号链接，解析后必须仍在 base 内，否则拒绝；
//  4. 终点片段若已是符号链接则直接拒绝（卷目录必须是真实目录，
//     防止预置链接把写入重定向到池外）。
func safeJoinUnder(base string, parts ...string) (string, error) {
	base = filepath.Clean(strings.TrimSpace(base))
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("storage pool path must be absolute: %s", base)
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("storage pool path is not accessible: %s: %v", base, err)
	}
	current := resolvedBase
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("refusing unsafe path element %q", part)
		}
		if filepath.IsAbs(part) || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("refusing unsafe path element %q", part)
		}
		next := filepath.Join(current, part)
		info, err := os.Lstat(next)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if i == len(parts)-1 {
					return "", fmt.Errorf("refusing symlink at volume path %s", next)
				}
				resolved, err := filepath.EvalSymlinks(next)
				if err != nil {
					return "", fmt.Errorf("failed to resolve symlink %s: %v", next, err)
				}
				if !pathStrictlyUnder(resolved, resolvedBase) {
					return "", fmt.Errorf("refusing symlink %s escaping storage pool root", next)
				}
				current = resolved
				continue
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		current = next
	}
	if !pathStrictlyUnder(current, resolvedBase) {
		return "", fmt.Errorf("refusing path %s escaping storage pool root %s", current, resolvedBase)
	}
	return current, nil
}

// pathStrictlyUnder 判断 path 是否严格位于 base 之下（不允许等于 base）。
func pathStrictlyUnder(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	return true
}

// dirSizeMB 按文件大小求和近似卷占用（MB）。不走 shell（du），
// 保持本包零外部命令依赖；大小仅用于信息上报，不参与计费。
func dirSizeMB(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total / (1024 * 1024)
}

// copyTree 递归复制目录树（纯 Go 实现，无 shell 拼接，符合工程红线）：
// 符号链接按原样重建；设备节点等特殊文件跳过（容器 /dev 在运行时由
// LXC 重新挂载，无需复制）。
func copyTree(src string, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, readErr := os.Readlink(p)
			if readErr != nil {
				return readErr
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			if err := copyFile(p, target, info.Mode().Perm()); err != nil {
				return err
			}
			return nil
		default:
			// 特殊文件（socket/设备/fifo）跳过。
			return nil
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
