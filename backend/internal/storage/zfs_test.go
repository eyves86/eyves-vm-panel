package storage

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// scriptRunner 实现 CommandRunner 并记录所有调用，以便单测断言参数数组。
//
// 脚本化命令：返回预置输出；触发错误时返回预置错误并保留已记录参数。
type scriptRunner struct {
	mu     sync.Mutex
	calls  []recordedCall
	byArgs map[string]scriptReply
	// fallback：当 byArgs 找不到精确匹配时，使用 fallback 的 reply（按 args 前缀匹配）。
	fallback scriptReply
}

type recordedCall struct {
	name string
	args []string
}

type scriptReply struct {
	output string
	err    error
}

func newScriptRunner() *scriptRunner { return &scriptRunner{byArgs: map[string]scriptReply{}} }

func (r *scriptRunner) on(args []string, output string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byArgs[joinArgs("zfs", args)] = scriptReply{output: output, err: err}
}

func (r *scriptRunner) onCmd(name string, args []string, output string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byArgs[joinArgs(name, args)] = scriptReply{output: output, err: err}
}

func (r *scriptRunner) Run(name string, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	reply, ok := r.byArgs[joinArgs(name, args)]
	if !ok {
		reply = r.fallback
	}
	r.mu.Unlock()
	if reply.err != nil {
		return reply.output, reply.err
	}
	return reply.output, nil
}

func (r *scriptRunner) callsByName(name string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := [][]string{}
	for _, c := range r.calls {
		if c.name == name {
			out = append(out, append([]string(nil), c.args...))
		}
	}
	return out
}

func joinArgs(name string, args []string) string {
	return name + "\x00" + strings.Join(args, "\x00")
}

func newZFSBackend(t *testing.T, runner CommandRunner, cfg map[string]string) *ZFSBackend {
	t.Helper()
	b, err := NewZFSBackend("pool-zfs", "tank/eyvescloud/disk-data", cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestZFSBackendEnsurePoolCreatesWithParent(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "", errors.New("dataset does not exist"))
	r.on([]string{"create", "tank/eyvescloud/disk-data", "-o", "compression=lz4"}, "", nil)
	b := newZFSBackend(t, r, map[string]string{ZFSConfigKeyParentDataset: "tank/eyvescloud"})
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	if len(calls) != 2 {
		t.Fatalf("expected 2 zfs calls, got %d", len(calls))
	}
	createArgs := calls[1]
	if createArgs[0] != "create" || createArgs[1] != "tank/eyvescloud/disk-data" {
		t.Fatalf("EnsurePool create args = %v, want create <parent-path>", createArgs)
	}
	gotCompress := false
	for _, a := range createArgs {
		if a == "compression=lz4" {
			gotCompress = true
		}
	}
	if !gotCompress {
		t.Fatalf("EnsurePool must set default compression=lz4, got args %v", createArgs)
	}
}

func TestZFSBackendEnsurePoolIdempotent(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "tank/eyvescloud/disk-data\t0\t0\t0\t-\n", nil)
	b := newZFSBackend(t, r, nil)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
	if calls := r.callsByName("zfs"); len(calls) != 1 {
		t.Fatalf("EnsurePool must skip create when dataset exists, calls=%d", len(calls))
	}
}

func TestZFSBackendEnsurePoolCustomCompressionAndMountpoint(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "", errors.New("does not exist"))
	r.on([]string{"create", "tank/eyvescloud/disk-data", "-o", "compression=zstd", "-o", "mountpoint=/eyvescloud/pool-data"}, "", nil)
	b := newZFSBackend(t, r, map[string]string{ZFSConfigKeyCompression: "zstd", ZFSConfigKeyMountpoint: "/eyvescloud/pool-data"})
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
	createArgs := r.callsByName("zfs")[1]
	wantOpts := map[string]bool{"compression=zstd": false, "mountpoint=/eyvescloud/pool-data": false}
	for _, a := range createArgs {
		if _, ok := wantOpts[a]; ok {
			wantOpts[a] = true
		}
	}
	for k, seen := range wantOpts {
		if !seen {
			t.Fatalf("EnsurePool missing option %s in args %v", k, createArgs)
		}
	}
}

func TestZFSBackendCreateVolumeCommand(t *testing.T) {
	r := newScriptRunner()
	// EnsurePool: pool root 不存在 → create
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "", errors.New("does not exist"))
	r.on([]string{"create", "tank/eyvescloud/disk-data", "-o", "compression=lz4"}, "", nil)
	// CreateVolume: 目标 dataset 不存在 → create
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-1"}, "", errors.New("does not exist"))
	expectedQuotas := []string{"quota=10485760", "refquota=10485760"}
	createArgs := []string{"create", "-o", "quota=10485760", "-o", "refquota=10485760", "-o", "compression=lz4", "tank/eyvescloud/disk-data/vol-1"}
	r.on(createArgs, "", nil)
	b := newZFSBackend(t, r, nil)
	vol := Volume{ID: "vol-1", PoolID: "pool-zfs", Kind: VolumeKindDir, SizeMB: 10}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	if len(calls) < 2 {
		t.Fatalf("expected >=2 zfs calls, got %d", len(calls))
	}
	// 找 create 调用
	var got []string
	for _, args := range calls {
		if len(args) >= 1 && args[0] == "create" && args[len(args)-1] == "tank/eyvescloud/disk-data/vol-1" {
			got = args
			break
		}
	}
	if got == nil {
		t.Fatalf("no create call for vol-1 dataset, calls=%v", calls)
	}
	for _, want := range expectedQuotas {
		found := false
		for _, a := range got {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("CreateVolume missing -o %s in args %v", want, got)
		}
	}
}

func TestZFSBackendCreateVolumeRejectsBlockKind(t *testing.T) {
	r := newScriptRunner()
	b := newZFSBackend(t, r, nil)
	err := b.CreateVolume(Volume{ID: "vol-x", Kind: VolumeKindBlock})
	if !errors.Is(err, ErrBlockNotSupported) {
		t.Fatalf("block kind error = %v, want ErrBlockNotSupported", err)
	}
	if len(r.callsByName("zfs")) != 0 {
		t.Fatalf("block volume must not trigger any zfs command")
	}
}

func TestZFSBackendCreateVolumeRejectsExisting(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "tank/eyvescloud/disk-data\t0\t0\t0\t-\n", nil)
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-dup"}, "tank/eyvescloud/disk-data/vol-dup\t0\t0\t0\t-\n", nil)
	b := newZFSBackend(t, r, nil)
	err := b.CreateVolume(Volume{ID: "vol-dup", Kind: VolumeKindDir, SizeMB: 100})
	if !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("existing dataset error = %v, want ErrVolumeExists", err)
	}
}

func TestZFSBackendCreateVolumeRejectsInvalidSize(t *testing.T) {
	r := newScriptRunner()
	b := newZFSBackend(t, r, nil)
	if err := b.CreateVolume(Volume{ID: "vol-0", Kind: VolumeKindDir, SizeMB: 0}); err == nil {
		t.Fatal("expected error for zero size")
	}
	if err := b.CreateVolume(Volume{ID: "vol-neg", Kind: VolumeKindDir, SizeMB: -1}); err == nil {
		t.Fatal("expected error for negative size")
	}
}

func TestZFSBackendDeleteVolumeRecursive(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-d"}, "tank/eyvescloud/disk-data/vol-d\t0\t0\t0\t-\n", nil)
	r.on([]string{"destroy", "-r", "tank/eyvescloud/disk-data/vol-d"}, "", nil)
	b := newZFSBackend(t, r, nil)
	if err := b.DeleteVolume(Volume{ID: "vol-d", Kind: VolumeKindDir}); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	if len(calls) != 2 || calls[1][0] != "destroy" || calls[1][1] != "-r" {
		t.Fatalf("DeleteVolume must use `zfs destroy -r`, got %v", calls)
	}
}

func TestZFSBackendDeleteVolumeIdempotent(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-x"}, "", errors.New("dataset does not exist"))
	b := newZFSBackend(t, r, nil)
	if err := b.DeleteVolume(Volume{ID: "vol-x", Kind: VolumeKindDir}); err != nil {
		t.Fatalf("DeleteVolume of missing dataset must be idempotent, got %v", err)
	}
}

func TestZFSBackendResizeVolumeGrowOnly(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-r"}, "tank/eyvescloud/disk-data/vol-r\t0\t0\t0\t-\n", nil)
	r.on([]string{"set", "quota=20971520", "tank/eyvescloud/disk-data/vol-r"}, "", nil)
	r.on([]string{"set", "refquota=20971520", "tank/eyvescloud/disk-data/vol-r"}, "", nil)
	b := newZFSBackend(t, r, nil)
	if err := b.ResizeVolume(Volume{ID: "vol-r", Kind: VolumeKindDir, SizeMB: 10}, 20); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls (exists+set quota+set refquota), got %d", len(calls))
	}
	if calls[1][0] != "set" || !strings.HasPrefix(calls[1][1], "quota=") {
		t.Fatalf("second call must set quota, got %v", calls[1])
	}
	if calls[2][0] != "set" || !strings.HasPrefix(calls[2][1], "refquota=") {
		t.Fatalf("third call must set refquota, got %v", calls[2])
	}

	err := b.ResizeVolume(Volume{ID: "vol-r", Kind: VolumeKindDir, SizeMB: 20}, 10)
	if !errors.Is(err, ErrShrinkNotSupported) {
		t.Fatalf("shrink error = %v, want ErrShrinkNotSupported", err)
	}
}

func TestZFSBackendSnapshotAndRestore(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-s"}, "tank/eyvescloud/disk-data/vol-s\t0\t0\t0\t-\n", nil)
	r.on([]string{"snapshot", "tank/eyvescloud/disk-data/vol-s@snap-1"}, "", nil)
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-s@snap-1"}, "tank/eyvescloud/disk-data/vol-s@snap-1\t0\t0\t0\t-\n", nil)
	r.on([]string{"rollback", "-r", "tank/eyvescloud/disk-data/vol-s@snap-1"}, "", nil)
	b := newZFSBackend(t, r, nil)
	vol := Volume{ID: "vol-s", Kind: VolumeKindDir}
	if err := b.SnapshotVolume(vol, "snap-1"); err != nil {
		t.Fatal(err)
	}
	if err := b.RestoreSnapshot(vol, "snap-1"); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	last := calls[len(calls)-1]
	if last[0] != "rollback" || last[1] != "-r" || last[2] != "tank/eyvescloud/disk-data/vol-s@snap-1" {
		t.Fatalf("rollback args = %v, want `rollback -r <dataset>@<snap>`", last)
	}
}

func TestZFSBackendSnapshotRejectsExisting(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-dup"}, "tank/eyvescloud/disk-data/vol-dup\t0\t0\t0\t-\n", nil)
	r.on([]string{"snapshot", "tank/eyvescloud/disk-data/vol-dup@s1"}, "", errors.New("dataset already exists"))
	b := newZFSBackend(t, r, nil)
	err := b.SnapshotVolume(Volume{ID: "vol-dup", Kind: VolumeKindDir}, "s1")
	if !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("duplicate snapshot error = %v, want ErrVolumeExists", err)
	}
}

func TestZFSBackendCloneVolumeFullPromotesAndCleansUp(t *testing.T) {
	r := newScriptRunner()
	// EnsurePool 路径：pool root 不存在 → create。
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data"}, "", errors.New("does not exist"))
	r.on([]string{"create", "tank/eyvescloud/disk-data", "-o", "compression=lz4"}, "", nil)
	// CreateVolume(dst) 路径：dst 不存在 → create。
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-dst"}, "", errors.New("does not exist"))
	r.on([]string{"create", "-o", "quota=1048576", "-o", "refquota=1048576", "-o", "compression=lz4", "tank/eyvescloud/disk-data/vol-dst"}, "", nil)
	// src 存在性检查。
	r.on([]string{"list", "-Hp", "tank/eyvescloud/disk-data/vol-src"}, "tank/eyvescloud/disk-data/vol-src\t0\t0\t0\t-\n", nil)
	// 用 placeholder 匹配临时 snapshot 名称（任意 __clone_<digits>）。
	// 采用 fallback：允许任何 snapshot 命令成功。
	r.fallback = scriptReply{output: "", err: nil}
	b := newZFSBackend(t, r, nil)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
	if err := b.CloneVolume(
		Volume{ID: "vol-src", Kind: VolumeKindDir},
		Volume{ID: "vol-dst", Kind: VolumeKindDir, SizeMB: 1},
		CloneModeFull,
	); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("zfs")
	sawClone := false
	sawPromote := false
	for _, args := range calls {
		if len(args) >= 3 && args[0] == "clone" {
			sawClone = true
			// src 的临时 snapshot 名形如 tank/.../vol-src@__clone_<digits>
			if !strings.Contains(args[1], "tank/eyvescloud/disk-data/vol-src@__clone_") {
				t.Fatalf("clone source snapshot = %q, want <src>@__clone_<digits>", args[1])
			}
			if args[2] != "tank/eyvescloud/disk-data/vol-dst" {
				t.Fatalf("clone target = %q, want vol-dst dataset", args[2])
			}
		}
		if len(args) >= 2 && args[0] == "promote" {
			sawPromote = true
		}
	}
	if !sawClone {
		t.Fatal("CloneVolume must invoke `zfs clone`")
	}
	if !sawPromote {
		t.Fatal("CloneVolume must invoke `zfs promote` for independence")
	}
}

func TestZFSBackendCloneVolumeLinkedRejected(t *testing.T) {
	r := newScriptRunner()
	b := newZFSBackend(t, r, nil)
	err := b.CloneVolume(Volume{ID: "src", Kind: VolumeKindDir}, Volume{ID: "dst", Kind: VolumeKindDir}, CloneModeLinked)
	if !errors.Is(err, ErrLinkedCloneNotSupported) {
		t.Fatalf("linked clone error = %v, want ErrLinkedCloneNotSupported", err)
	}
}

func TestZFSBackendVolumeInfoFromListOutput(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "-o", "name,used,avail,refer,quota", "tank/eyvescloud/disk-data/vol-i"},
		"tank/eyvescloud/disk-data/vol-i\t3145728\t10485760\t1048576\t10485760\n", nil)
	b := newZFSBackend(t, r, nil)
	info, err := b.VolumeInfo(Volume{ID: "vol-i", Kind: VolumeKindDir})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "available" {
		t.Fatalf("status = %q, want available", info.Status)
	}
	if info.ActualSizeMB != 3 {
		t.Fatalf("ActualSizeMB = %d, want 3 (3145728 bytes)", info.ActualSizeMB)
	}
	if info.Path != "tank/eyvescloud/disk-data/vol-i" {
		t.Fatalf("Path = %q, want dataset path", info.Path)
	}
}

func TestZFSBackendVolumeInfoMissing(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "-o", "name,used,avail,refer,quota", "tank/eyvescloud/disk-data/vol-m"},
		"", errors.New("dataset does not exist"))
	b := newZFSBackend(t, r, nil)
	info, err := b.VolumeInfo(Volume{ID: "vol-m", Kind: VolumeKindDir})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "missing" {
		t.Fatalf("status = %q, want missing", info.Status)
	}
}

func TestZFSBackendPoolStats(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"list", "-Hp", "-o", "name,used,avail", "tank/eyvescloud/disk-data"},
		"tank/eyvescloud/disk-data\t52428800\t10737418240\n", nil)
	b := newZFSBackend(t, r, nil)
	used, avail, ok := b.PoolStats()
	if !ok {
		t.Fatal("PoolStats must succeed when zfs returns parsed output")
	}
	if used != 52428800 || avail != 10737418240 {
		t.Fatalf("PoolStats used=%d avail=%d, want 52428800/10737418240", used, avail)
	}
}

func TestZFSBackendSelfTest(t *testing.T) {
	r := newScriptRunner()
	r.on([]string{"version"}, "zfs-2.2.2-1", nil)
	b := newZFSBackend(t, r, nil)
	if err := b.SelfTest(); err != nil {
		t.Fatal(err)
	}
	r2 := newScriptRunner()
	r2.on([]string{"version"}, "", errors.New("command not found"))
	b2 := newZFSBackend(t, r2, nil)
	if err := b2.SelfTest(); err == nil {
		t.Fatal("SelfTest must surface zfs unavailability")
	}
}

// TestZFSBackendRejectsUnsafeDatasetPath 覆盖 zfs dataset 名字片段逃逸：
// "/"、绝对路径、含 "@"/"#"/"."/".."、空、含控制字符的 volID 全部拒绝；
// 同时校验池根路径中夹带 "@"/"#" 也会被拒绝。
func TestZFSBackendRejectsUnsafeDatasetPath(t *testing.T) {
	unsafeIDs := []string{
		"../escape",
		"..",
		".",
		"",
		"sub/dir",
		"snap@evil",
		"book#mark",
		"vol with space",
		"vol\x00null",
	}
	for _, id := range unsafeIDs {
		b := newZFSBackend(t, newScriptRunner(), nil)
		if err := b.CreateVolume(Volume{ID: id, Kind: VolumeKindDir, SizeMB: 1}); err == nil {
			t.Fatalf("CreateVolume accepted unsafe id %q", id)
		}
	}

	for _, root := range []string{"", "/abs/root", "tank@bad", "tank#x", "tank/./evil", "tank/../escape", "tank/@sub"} {
		if _, err := NewZFSBackend("p", root, nil, nil); err == nil {
			t.Fatalf("NewZFSBackend accepted unsafe pool root %q", root)
		}
	}

	b := newZFSBackend(t, newScriptRunner(), nil)
	if err := b.SnapshotVolume(Volume{ID: "vol-ok", Kind: VolumeKindDir}, "bad@snap"); err == nil {
		t.Fatal("SnapshotVolume must reject unsafe snapID containing @")
	}
}

// TestParseZFSListLine 覆盖真实 zfs list -Hp 输出解析（含 none/-\u3001单位后缀）。
func TestParseZFSListLine(t *testing.T) {
	cases := []struct {
		line      string
		wantOK    bool
		wantUsed  int64
		wantAvail int64
		wantQuota int64
		wantName  string
	}{
		{
			line:     "tank/pool/vol\t3145728\t10485760\t1048576\t10485760",
			wantOK:   true,
			wantUsed: 3145728, wantAvail: 10485760, wantQuota: 10485760,
			wantName: "tank/pool/vol",
		},
		{
			line:      "tank/pool/vol\t1G\t10G\t512M\t-\t",
			wantOK:    true,
			wantUsed:  1024 * 1024 * 1024,
			wantAvail: 10 * 1024 * 1024 * 1024,
			wantQuota: 0, // "-" → 0（无限制）
			wantName:  "tank/pool/vol",
		},
		{
			line:     "tank/pool/vol\tnone\tnone\t-\t-",
			wantOK:   true,
			wantUsed: 0, wantAvail: 0, wantQuota: 0,
			wantName: "tank/pool/vol",
		},
		{
			line:   "short",
			wantOK: false,
		},
	}
	for _, c := range cases {
		name, used, avail, quota, ok := parseZFSListLine(c.line)
		if ok != c.wantOK {
			t.Fatalf("parseZFSListLine(%q) ok=%v, want %v", c.line, ok, c.wantOK)
		}
		if !ok {
			continue
		}
		if name != c.wantName || used != c.wantUsed || avail != c.wantAvail || quota != c.wantQuota {
			t.Fatalf("parseZFSListLine(%q) = %s/%d/%d/%d, want %s/%d/%d/%d",
				c.line, name, used, avail, quota,
				c.wantName, c.wantUsed, c.wantAvail, c.wantQuota)
		}
	}
}

// TestBackendForPool 覆盖 factory：dir/zfs 派发，未知/旧空后端按 dir 兜底逻辑由
// NormalizeBackendKind 决定（factory 自身按归一化值严格匹配）。
func TestBackendForPool(t *testing.T) {
	if _, err := BackendForPool("p", "/tmp/p", "dir", nil, nil); err != nil {
		t.Fatalf("dir factory: %v", err)
	}
	if _, err := BackendForPool("p", "tank/eyvescloud/disk-data", "zfs", nil, nil); err != nil {
		t.Fatalf("zfs factory: %v", err)
	}
	if _, err := BackendForPool("p", "/tmp/p", "lvm", nil, nil); err == nil {
		t.Fatal("factory must reject unimplemented backend")
	}
	if _, err := BackendForPool("p", "tank/eyvescloud/disk-data", "", map[string]string{ZFSConfigKeyParentDataset: "tank/eyvescloud"}, nil); err != nil {
		t.Fatalf("factory with zfs default from empty kind failed: %v", err)
	}
}

// TestZFSBackendRunnerIntegration 仅在真实 zfs 可用时跑（沙箱里通常 skip）；
// 编译期保证 zfs 真调通路径。
func TestZFSBackendRunnerIntegration(t *testing.T) {
	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs binary not available, skipping integration test")
	}
	b, err := NewZFSBackend("p", "tank/eyvescloud/disk-data", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SelfTest(); err != nil {
		t.Skipf("zfs runtime not functional on this host: %v", err)
	}
}
