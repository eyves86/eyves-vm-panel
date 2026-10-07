package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fsStat 是 statfs 结果的最小可移植投影：只需要块大小与块数量来算容量。
// 具体平台的取数在 statfs_linux.go / statfs_other.go 里，避免把 Linux 专有的
// syscall.Statfs_t 泄漏到公共结构体上。
type fsStat struct {
	Bsize  int64
	Blocks uint64
	Bavail uint64
}

// NFS 池配置 K/V（来自 StoragePool.Config）。
//
// 字段语义：
//   - Server：NFS 服务器主机名/IP（必填）；
//   - Export：NFS 服务端 export 路径（必填，如 /data/eyvescloud）；
//   - MountOptions：mount 命令 -o 额外选项（如 "vers=4.1,noatime"），空字符串走 mount 默认。
//
// 密码 / 安全选项：本后端绝不允许在 Config 中携带凭据，mount 通过 NFSv4
// Kerberos 或 IP 白名单鉴权（由 StoragePool.Shared=true 决定是否纳入共享存储池）。
const (
	NFSConfigKeyServer      = "server"
	NFSConfigKeyExport      = "export"
	NFSConfigKeyMountOptions = "mount_options"
)

// NFSBackend 是 NFS 挂载型目录后端：把 NFS export 挂到本地 mountPoint，
// 卷 = mountPoint 下子目录（per instance）。
//
// 数据布局（文件系统视角）：
//
//   <poolPath>/<volumeID>      = mountPoint/<volumeID> 的目录卷
//
// poolPath 即 StoragePool.Path，是本地挂载点（应用层负责事先创建空目录，
// EnsurePool 时检查是否已挂载；不挂载则报错，由运维处理挂载）。
//
// 容量控制：NFS 卷的 statfs 直接透传服务端配额；本后端 ResizeVolume 不支持
// 客户端缩放配额（配额在服务端 /etc/exports 配置），统一返回 ErrShrinkNotSupported
// 对增长也报错（NFS 配额调整不属于本接口职责，留给 P2-3 或运维通道）。
type NFSBackend struct {
	poolID   string
	poolPath string
	server   string
	export   string
	options  []string
	runner   CommandRunner
	statfsSys func(path string) (fsStat, error)
}

// NewNFSBackend 构造 NFS 后端实例。
//   - config 必须包含 server + export；mount_options 可空；
//   - statfsSys 是注入点便于测试，nil 走平台 statfs（见 statfs_linux.go）；
//   - runner 为 nil 退化为 OSCommandRunner。
func NewNFSBackend(poolID, poolPath string, config map[string]string, runner CommandRunner) (*NFSBackend, error) {
	server := strings.TrimSpace(configValue(config, NFSConfigKeyServer))
	export := strings.TrimSpace(configValue(config, NFSConfigKeyExport))
	if server == "" || export == "" {
		return nil, fmt.Errorf("nfs pool %s: missing config server/export", poolID)
	}
	if strings.ContainsAny(server, " \t\n@#") || strings.ContainsAny(export, " \t\n") {
		return nil, fmt.Errorf("nfs pool %s: invalid server/export characters", poolID)
	}
	var options []string
	if raw := strings.TrimSpace(configValue(config, NFSConfigKeyMountOptions)); raw != "" {
		for _, opt := range strings.Split(raw, ",") {
			opt = strings.TrimSpace(opt)
			if opt != "" {
				options = append(options, opt)
			}
		}
	}
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return &NFSBackend{
		poolID:   poolID,
		poolPath: poolPath,
		server:   server,
		export:   export,
		options:  options,
		runner:   runner,
	}, nil
}

// Server 返回 NFS 服务器标识。
func (b *NFSBackend) Server() string { return b.server }

// Export 返回 NFS export 路径。
func (b *NFSBackend) Export() string { return b.export }

// EnsurePool 校验 NFS export 已挂载到 poolPath；未挂载时尝试 `mount` 一次，
// 失败时明确报错（运维需检查服务端可达性 + 防火墙）。
func (b *NFSBackend) EnsurePool() error {
	if _, err := b.statfs(b.poolPath); err != nil {
		return fmt.Errorf("nfs pool %s: poolPath %s not accessible: %v", b.poolID, b.poolPath, err)
	}
	mounted, err := b.isMounted()
	if err != nil {
		return err
	}
	if !mounted {
		if err := b.mount(); err != nil {
			return fmt.Errorf("nfs pool %s: mount %s:%s -> %s: %v", b.poolID, b.server, b.export, b.poolPath, err)
		}
	}
	return nil
}

// CreateVolume 在 NFS 挂载点下建子目录。
func (b *NFSBackend) CreateVolume(vol Volume) error {
	if vol.Kind != VolumeKindDir {
		return fmt.Errorf("%w: nfs backend serves directory volumes only", ErrBlockNotSupported)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	path := b.volumePath(vol.ID)
	if _, err := b.statfs(path); err == nil {
		return fmt.Errorf("%w: %s", ErrVolumeExists, path)
	}
	return osMkdirAll(path, 0755)
}

// DeleteVolume 删子目录（幂等）。
func (b *NFSBackend) DeleteVolume(vol Volume) error {
	path := b.volumePath(vol.ID)
	if _, err := b.statfs(path); err != nil {
		return nil
	}
	return osRemoveAll(path)
}

// ResizeVolume NFS 卷配额调整不在本接口职责范围（服务端配置）；
// 任何请求均拒绝（grow 也拒绝，避免误传）。
func (b *NFSBackend) ResizeVolume(vol Volume, newMB int64) error {
	return fmt.Errorf("%w: NFS volume size is controlled by server-side export quota", ErrShrinkNotSupported)
}

// SnapshotVolume / RestoreSnapshot NFS 文件系统层不支持原生快照（需依赖
// 服务端能力，如 ZFS-on-NFS）；本后端统一拒绝，调用方走 storage pool 切换到
// ZFS 后端做快照。
func (b *NFSBackend) SnapshotVolume(vol Volume, snapID string) error {
	return fmt.Errorf("nfs backend does not support snapshots; use zfs backend")
}

func (b *NFSBackend) RestoreSnapshot(vol Volume, snapID string) error {
	return fmt.Errorf("nfs backend does not support snapshots; use zfs backend")
}

// CloneVolume NFS 目录下复制（用本包内 osCopyTree 实现）。
//
// mode=linked 不支持。
func (b *NFSBackend) CloneVolume(src Volume, dst Volume, mode string) error {
	switch mode {
	case CloneModeFull:
	case CloneModeLinked:
		return ErrLinkedCloneNotSupported
	default:
		return fmt.Errorf("invalid clone mode %q", mode)
	}
	if err := b.EnsurePool(); err != nil {
		return err
	}
	if err := b.CreateVolume(dst); err != nil {
		return err
	}
	srcPath := b.volumePath(src.ID)
	if _, err := b.statfs(srcPath); err != nil {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("%w: %s", ErrVolumeNotFound, src.ID)
	}
	dstPath := b.volumePath(dst.ID)
	if err := osCopyTree(srcPath, dstPath); err != nil {
		_ = b.DeleteVolume(dst)
		return fmt.Errorf("nfs clone %s -> %s: %v", srcPath, dstPath, err)
	}
	return nil
}

// VolumeInfo 返回挂载路径（不创建子目录查询；statfs 子目录即返回 NFS 服务端统计）。
func (b *NFSBackend) VolumeInfo(vol Volume) (VolumeInfoResult, error) {
	path := b.volumePath(vol.ID)
	info := VolumeInfoResult{Path: path, Status: "missing"}
	st, err := b.statfs(path)
	if err != nil {
		return info, nil
	}
	info.Status = "available"
	info.ActualSizeMB = st.Bsize * int64(st.Blocks) / (1024 * 1024)
	return info, nil
}

// PoolStats 返回池级容量统计（statfs）。
func (b *NFSBackend) PoolStats() (usedBytes, availBytes int64, ok bool) {
	st, err := b.statfs(b.poolPath)
	if err != nil {
		return 0, 0, false
	}
	total := st.Bsize * int64(st.Blocks)
	free := st.Bsize * int64(st.Bavail)
	return total - free, free, true
}

// isMounted 通过 `mountpoint` 或 statfs 类型字段（理想）判断；简化版：
// `findmnt -T <path>` 退出码 0 即视为挂载。
func (b *NFSBackend) isMounted() (bool, error) {
	out, err := b.runner.Run("findmnt", "-T", b.poolPath, "-o", "TARGET", "-n")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}

// mount 走 `mount -t nfs [-o opts] server:export poolPath`。
func (b *NFSBackend) mount() error {
	args := []string{"-t", "nfs"}
	if len(b.options) > 0 {
		args = append(args, "-o", strings.Join(b.options, ","))
	}
	args = append(args, fmt.Sprintf("%s:%s", b.server, b.export), b.poolPath)
	if _, err := b.runner.Run("mount", args...); err != nil {
		return err
	}
	return nil
}

// volumePath 拼接池根 + 卷 ID（与 dir 后端 safeJoinUnder 等价的简化版）。
// poolPath 由 EnsurePool 阶段验证过存在且为目录；卷 ID 由 ConfigValue 体系约束。
func (b *NFSBackend) volumePath(volID string) string {
	return filepath.Join(b.poolPath, volID)
}

// statfs 包裹平台 statfs（注入点便于测试）。
func (b *NFSBackend) statfs(path string) (fsStat, error) {
	if b.statfsSys != nil {
		return b.statfsSys(path)
	}
	return sysStatfs(path)
}

// osMkdirAll / osRemoveAll / osCopyTree 复刻 dir.go 内的本地实现，避免跨后端共享
// 大段代码；NFS 后端逻辑简单，独立实现。
func osMkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func osRemoveAll(path string) error {
	return os.RemoveAll(path)
}

func osCopyTree(src string, dst string) error {
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
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		default:
			return nil
		}
	})
}

// SanityCheck：NFSBackend 实现 StorageBackend。
var _ StorageBackend = (*NFSBackend)(nil)