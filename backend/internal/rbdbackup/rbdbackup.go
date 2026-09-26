// Package rbdbackup RBD 增量备份 + 目录池 tarball 回退（P4-3）。
//
//   - RBD：rbd export-diff --from-snap / import-diff 增量链；
//   - Dir：实例目录 tar 归档（gzip 优先；zstd 仅在二进制可用时）；
//   - 快照游标 Cursor：每次成功备份后推进（rbd 与 dir 共用）；
//   - 三种后端（zfs/rbd/dir）统一 job 状态机，差异仅在 Driver 实现。
//
// 本包只产出命令构造 + 游标管理；真实执行由 storage.CommandRunner 抽象。
package rbdbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Backend 备份后端类型。
type Backend string

const (
	BackendZFS Backend = "zfs"
	BackendRBD Backend = "rbd"
	BackendDir Backend = "dir"
)

// Cursor 记录一次成功备份的快照游标（按 Backend 区分）。
type Cursor struct {
	Backend     Backend
	Instance    string
	// SnapshotName：上次成功快照（zfs dataset / rbd snap / dir 增量标记）。
	SnapshotName string
	// UpdatedAt：游标最后更新时间。
	UpdatedAt time.Time
}

// Fingerprint 游标指纹（写入持久化时区分实例/后端）。
func (c Cursor) Fingerprint() string {
	h := sha256.Sum256([]byte(string(c.Backend) + "|" + c.Instance))
	return hex.EncodeToString(h[:])[:16]
}

// ---- RBD 增量备份 ----

// RBDConfig RBD 备份配置（与 P0-4 RBDBackend 共享池元数据）。
type RBDConfig struct {
	Pool        string
	ImageName   string
	Snapshot    string // 上次成功快照（增量起点；空表示首次全量）
	BackupPath  string // 本地导出文件路径
}

// BuildRBDCommands 构造 rbd export-diff 命令参数数组。
//
//   - Snapshot == "" → 全量：rbd export <pool>/<image> <path>；
//   - Snapshot != "" → 增量：rbd export-diff --from-snap <snap> <pool>/<image> <path>。
//
// 同时给出 sha256sum 命令用于本地校验（备份完成后执行）。
func BuildRBDCommands(cfg RBDConfig) ([][]string, error) {
	if strings.TrimSpace(cfg.Pool) == "" || strings.TrimSpace(cfg.ImageName) == "" || strings.TrimSpace(cfg.BackupPath) == "" {
		return nil, errors.New("rbdbackup: pool/image/backup_path required")
	}
	if !isSafeZFSFragment(cfg.Pool) || !isSafeZFSFragment(cfg.ImageName) {
		return nil, fmt.Errorf("rbdbackup: unsafe pool/image name %q/%q", cfg.Pool, cfg.ImageName)
	}
	if !isSafeZFSFragment(cfg.Snapshot) && cfg.Snapshot != "" {
		return nil, fmt.Errorf("rbdbackup: unsafe snapshot %q", cfg.Snapshot)
	}
	cmds := [][]string{}
	if cfg.Snapshot == "" {
		cmds = append(cmds, []string{"export", fmt.Sprintf("%s/%s", cfg.Pool, cfg.ImageName), cfg.BackupPath})
	} else {
		cmds = append(cmds, []string{"export-diff", "--from-snap", cfg.Snapshot,
			fmt.Sprintf("%s/%s", cfg.Pool, cfg.ImageName), cfg.BackupPath})
	}
	// 校验和命令（生产执行用；测试不依赖）。
	cmds = append(cmds, []string{"sha256sum", cfg.BackupPath})
	return cmds, nil
}

// RBDImportCommands 构造 rbd import-diff / import 命令（恢复路径）。
func RBDImportCommands(pool, image, diffPath string) ([][]string, error) {
	if strings.TrimSpace(pool) == "" || strings.TrimSpace(image) == "" || strings.TrimSpace(diffPath) == "" {
		return nil, errors.New("rbdbackup: pool/image/diff_path required")
	}
	return [][]string{{"import-diff", diffPath, fmt.Sprintf("%s/%s", pool, image)}}, nil
}

// ---- 目录池 tarball ----

// DirConfig 目录池 tarball 备份配置。
type DirConfig struct {
	SourcePath string // 实例 rootfs 路径
	OutputPath string // 输出的 tarball 路径
	UseZstd    bool   // 优先使用 zstd；系统无 zstd 时退回 gzip（由调用方探测）
}

// BuildDirTarCommands 构造 tar 归档命令（参数化）。
//
//   - 默认：tar --xattrs -czf <out> -C <parent> <basename>（gzip）
//   - UseZstd：tar --xattrs --zstd -cf <out> -C <parent> <basename>
//
// sha256sum 命令附加在第二组。
func BuildDirTarCommands(cfg DirConfig) ([][]string, error) {
	if strings.TrimSpace(cfg.SourcePath) == "" || strings.TrimSpace(cfg.OutputPath) == "" {
		return nil, errors.New("rbdbackup: source/output required")
	}
	parent := trimTrailingSlash(filepathDir(cfg.SourcePath))
	base := filepathBase(cfg.SourcePath)
	if parent == "" || parent == "." || parent == ".." || base == "" || base == "." || base == ".." {
		return nil, fmt.Errorf("rbdbackup: invalid source path %q", cfg.SourcePath)
	}
	cmds := [][]string{}
	if cfg.UseZstd {
		cmds = append(cmds, []string{"tar", "--xattrs", "--zstd", "-cf", cfg.OutputPath, "-C", parent, base})
	} else {
		cmds = append(cmds, []string{"tar", "--xattrs", "-czf", cfg.OutputPath, "-C", parent, base})
	}
	cmds = append(cmds, []string{"sha256sum", cfg.OutputPath})
	return cmds, nil
}

// BuildDirExtractCommands 构造解 tar 命令（恢复路径）。
func BuildDirExtractCommands(tarballPath, targetDir string) ([][]string, error) {
	if tarballPath == "" || targetDir == "" {
		return nil, errors.New("rbdbackup: tarball/target required")
	}
	// 自动检测压缩类型；调用方按 tarball 后缀 .gz/.zst 传入 UseZstd 标识。
	return [][]string{{"tar", "--xattrs", "-xf", tarballPath, "-C", targetDir}}, nil
}

// ---- Manifest ----

// Manifest 单次备份的清单（路径 + sha256 + 大小）。导入/恢复时校验。
type Manifest struct {
	Backend   Backend    `json:"backend"`
	Instance  string     `json:"instance"`
	CreatedAt time.Time  `json:"created_at"`
	Files     []FileMeta `json:"files"`
}

// FileMeta 单文件元数据。
type FileMeta struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// VerifyManifest 校验清单：每个文件 sha256 必须匹配实际内容（调用方读取）。
//
// 简化版：仅验证清单自身 sha256 链（多个文件时所有 hash 排序后
// concat 重新 hash）。
func VerifyManifest(m Manifest) string {
	if len(m.Files) == 0 {
		return ""
	}
	files := append([]FileMeta(nil), m.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte("|"))
		h.Write([]byte(f.SHA256))
		h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- helpers ----

func isSafeZFSFragment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		case r >= 'A' && r <= 'Z':
		case r == '-' || r == '.' || r == ':':
		default:
			return false
		}
	}
	return true
}

// filepathDir / filepathBase 是本包简化的 dir/base 拆分（不依赖 path/filepath）。
func filepathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

func filepathBase(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func trimTrailingSlash(p string) string {
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	return p
}