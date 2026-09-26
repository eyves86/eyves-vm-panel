// Package zfsreplicate ZFS 增量异地复制（P4-2）：
//
//   - 仓库模型：Repo(type=zfs-ssh, address, encryptedCredentials, lastSnapshot)；
//   - 增量算法：找上次成功快照 → 决定 full(@initial) 或 incremental(@last..@new)；
//   - 重试安全：远端先 recv 到临时 dataset，完成校验后原子 rename；
//   - 凭证：加密字符串，运行时解密（实现细节由调用方注入 CryptoBox）；
//   - job 进度由 P1-4 taskqueue.Queue.ReportProgress 接入。
//
// 本包只产出 send/recv 命令构造 + 增量选择 + 仓库模型；真实执行由
// storage.CommandRunner 抽象（沙箱无 zfs 二进制）。
package zfsreplicate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// RepoType 仓库类型（预留 RBD/NFS/agent-relay 扩展）。
type RepoType string

const (
	RepoZFSSSH RepoType = "zfs-ssh"
)

// Repo 备份仓库定义。
type Repo struct {
	ID          string    `json:"id"`
	Type        RepoType  `json:"type"`
	Name        string    `json:"name"`
	// Address：远端地址（"user@host:dataset" 或 "host:/path"）。
	Address string `json:"address"`
	// EncryptedCredentials 加密后的凭证（密钥管理交由调用方，本包只透传）。
	EncryptedCredentials string `json:"encrypted_credentials"`
	// LocalPool / LocalDataset：本端 zfs 池 / 父 dataset。
	LocalPool     string `json:"local_pool"`
	LocalDataset  string `json:"local_dataset"`
	// RemotePool / RemoteDataset：远端 zfs 池 / 父 dataset（远端数据集）。
	RemotePool    string `json:"remote_pool"`
	RemoteDataset string `json:"remote_dataset"`
	// CompressStream 启用 zstd|gzip|off 流压缩（zfs send 默认 lz4；显式 zstd
	// 仅当远端能解压时使用）。
	CompressStream string `json:"compress_stream"`
}

// CryptoBox 凭证加/解密抽象（生产用 KMS 或本地密钥文件；测试用 NoopBox）。
type CryptoBox interface {
	Seal(plaintext string) (string, error)
	Open(ciphertext string) (string, error)
}

// NoopBox 测试桩（明文存）。
type NoopBox struct{}

// Seal implements CryptoBox。
func (NoopBox) Seal(plaintext string) (string, error) { return plaintext, nil }

// Open implements CryptoBox。
func (NoopBox) Open(ciphertext string) (string, error) { return ciphertext, nil }

// SnapshotRef 单个快照引用。
type SnapshotRef struct {
	Pool     string // "tank"
	Dataset  string // "eyvescloud/disk-data"
	Name     string // "snap-1"
}

// FullName 返回 zfs 全名（pool/dataset@snap）。
func (s SnapshotRef) FullName() string {
	return fmt.Sprintf("%s/%s@%s", s.Pool, s.Dataset, s.Name)
}

// Fingerprint 快照指纹（用于审计/比对）。当前取 full name 哈希。
func (s SnapshotRef) Fingerprint() string {
	h := sha256.Sum256([]byte(s.FullName()))
	return hex.EncodeToString(h[:])[:16]
}

// ---- 增量选择算法 ----

// PlanMode send 模式（full 或 incremental）。
type PlanMode string

const (
	ModeFull        PlanMode = "full"
	ModeIncremental PlanMode = "incremental"
)

// TransferPlan 是单次发送计划（命令 + 参数 + 远端接收）。
type TransferPlan struct {
	Mode      PlanMode
	// LocalSnap：源快照（full 模式下 @initial；incremental 模式从 SourceSnap 到 TargetSnap）。
	LocalSnap  SnapshotRef
	SourceSnap SnapshotRef // 仅 incremental：@last
	TargetSnap SnapshotRef // 目标快照
	// RemoteTempDataset：远端临时数据集（首次 recv 到此，完成后 rename 到最终）。
	RemoteTempDataset string
	// RemoteFinalDataset：rename 后的最终数据集。
	RemoteFinalDataset string
}

// BuildIncrementalPlan 根据 lastSyncedSnapshot + 新快照生成计划。
//
//   - lastSyncedSnapshot == "" → full（@initial）；
//   - 否则 incremental（@last..@new）。
//
// 校验：
//   - lastSnapshot 必须在 sourceSnap 之前；(lastSnapshot.FullName 字典序？——不，
//     ZFS 时间排序；调用方传入时间序，本包只校验 last 不等于 new）。
//   - sourceSnap 和 targetSnap 必须属于同一 pool/dataset。
func BuildIncrementalPlan(repo Repo, lastSyncedSnapshot, newSnapshot SnapshotRef) (TransferPlan, error) {
	if err := validateSnapshotName(newSnapshot); err != nil {
		return TransferPlan{}, err
	}
	if newSnapshot.Dataset != repo.LocalDataset {
		return TransferPlan{}, fmt.Errorf("zfsreplicate: snapshot dataset %q does not match repo %q", newSnapshot.Dataset, repo.LocalDataset)
	}
	if lastSyncedSnapshot == (SnapshotRef{}) {
		// 首次同步：full。
		return TransferPlan{
			Mode:               ModeFull,
			LocalSnap:          newSnapshot,
			TargetSnap:         newSnapshot,
			RemoteTempDataset:  repo.RemoteDataset + "/tmp-" + safeSnapName(newSnapshot.Name),
			RemoteFinalDataset: repo.RemoteDataset + "/" + safeSnapName(newSnapshot.Name),
		}, nil
	}
	if err := validateSnapshotName(lastSyncedSnapshot); err != nil {
		return TransferPlan{}, err
	}
	if lastSyncedSnapshot.Dataset != newSnapshot.Dataset {
		return TransferPlan{}, fmt.Errorf("zfsreplicate: snapshot datasets mismatch (%q vs %q)", lastSyncedSnapshot.Dataset, newSnapshot.Dataset)
	}
	if lastSyncedSnapshot.Pool != newSnapshot.Pool {
		return TransferPlan{}, fmt.Errorf("zfsreplicate: snapshot pools mismatch (%q vs %q)", lastSyncedSnapshot.Pool, newSnapshot.Pool)
	}
	if lastSyncedSnapshot.Name == newSnapshot.Name {
		return TransferPlan{}, errors.New("zfsreplicate: last == new; nothing to send")
	}
	return TransferPlan{
		Mode:               ModeIncremental,
		SourceSnap:         lastSyncedSnapshot,
		TargetSnap:         newSnapshot,
		RemoteTempDataset:  repo.RemoteDataset + "/tmp-" + safeSnapName(newSnapshot.Name),
		RemoteFinalDataset: repo.RemoteDataset + "/" + safeSnapName(newSnapshot.Name),
	}, nil
}

// safeSnapName 把 snapshot name 限定在 [A-Za-z0-9_-]，避免 shell 注入。
func safeSnapName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "snap"
	}
	return b.String()
}

func validateSnapshotName(s SnapshotRef) error {
	if s.Pool == "" || s.Dataset == "" || s.Name == "" {
		return errors.New("zfsreplicate: snapshot ref requires pool/dataset/name")
	}
	if !isSafeZFSSegment(s.Pool) || strings.Contains(s.Dataset, "@") {
		return fmt.Errorf("zfsreplicate: unsafe snapshot name %+v", s)
	}
	return nil
}

// isSafeZFSSegment 校验 pool 名片段（与 storage.zfsSafeJoinUnder 互补）。
func isSafeZFSSegment(s string) bool {
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

// ---- 命令构造 ----

// BuildLocalSendCommands 构造本端 zfs send 命令（参数化，无 shell 拼接）：
//
//   - full:        zfs send -p <snap>
//   - incremental: zfs send -p -i <from> <to>
//   - 可选: -c/-e 控制 stream size（ZFS 特性差异；本接口不强制）
//   - 可选: 压缩（-c 不传 — 由 Repo.CompressStream 决定在外层 pipe 加 gzip/zstd）
func BuildLocalSendCommands(plan TransferPlan) ([][]string, error) {
	if plan.Mode == ModeFull {
		if plan.LocalSnap == (SnapshotRef{}) {
			return nil, errors.New("zfsreplicate: full plan requires local snap")
		}
		return [][]string{{"send", "-p", plan.LocalSnap.FullName()}}, nil
	}
	if plan.SourceSnap == (SnapshotRef{}) || plan.TargetSnap == (SnapshotRef{}) {
		return nil, errors.New("zfsreplicate: incremental plan requires source + target")
	}
	return [][]string{{"send", "-p", "-i", plan.SourceSnap.FullName(), plan.TargetSnap.FullName()}}, nil
}

// BuildRemoteRecvCommands 构造远端接收命令（ssh + zfs recv）：
//
//   zfs recv <RemoteTempDataset>
//
// 接收完成后由调用方执行 zfs rename temp → final；失败时清理 temp。
func BuildRemoteRecvCommands(plan TransferPlan, sshAddr string) ([][]string, error) {
	if plan.RemoteTempDataset == "" || sshAddr == "" {
		return nil, errors.New("zfsreplicate: remote recv requires temp dataset + ssh address")
	}
	return [][]string{
		{"ssh", "-T", sshAddr, "zfs", "recv", plan.RemoteTempDataset},
	}, nil
}

// BuildRemoteRenameCommands 构造 rename 命令（原子化远端数据集切换）。
func BuildRemoteRenameCommands(plan TransferPlan, sshAddr string) ([][]string, error) {
	if plan.RemoteFinalDataset == "" || plan.RemoteTempDataset == "" || sshAddr == "" {
		return nil, errors.New("zfsreplicate: rename requires temp + final + ssh address")
	}
	return [][]string{
		{"ssh", "-T", sshAddr, "zfs", "rename", plan.RemoteTempDataset, plan.RemoteFinalDataset},
	}, nil
}

// BuildRemoteCleanupCommands 构造失败清理命令（删除临时数据集）。
func BuildRemoteCleanupCommands(plan TransferPlan, sshAddr string) ([][]string, error) {
	if plan.RemoteTempDataset == "" || sshAddr == "" {
		return nil, errors.New("zfsreplicate: cleanup requires temp dataset + ssh")
	}
	return [][]string{
		{"ssh", "-T", sshAddr, "zfs", "destroy", "-r", plan.RemoteTempDataset},
	}, nil
}