package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestBackend 返回绑定临时池目录的 DirBackend 及池根路径。
func newTestBackend(t *testing.T) (*DirBackend, string) {
	t.Helper()
	poolPath := filepath.Join(t.TempDir(), "pool")
	return NewDirBackend("pool-test", poolPath), poolPath
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePoolIsIdempotent(t *testing.T) {
	b, poolPath := newTestBackend(t)
	for i := 0; i < 3; i++ {
		if err := b.EnsurePool(); err != nil {
			t.Fatalf("EnsurePool() attempt %d failed: %v", i+1, err)
		}
	}
	if info, err := os.Stat(poolPath); err != nil || !info.IsDir() {
		t.Fatalf("pool directory was not created at %s (err=%v)", poolPath, err)
	}
}

func TestEnsurePoolRejectsRelativePath(t *testing.T) {
	b := NewDirBackend("pool-rel", "relative/pool")
	if err := b.EnsurePool(); err == nil {
		t.Fatal("expected error for relative pool path")
	}
}

// TestVolumeLifecycleStateMachine 覆盖卷生命周期全流转：
// creating → available → attached → deleting，每个状态切换前后执行
// 真实文件操作并断言磁盘与 VolumeInfo 观测一致。
func TestVolumeLifecycleStateMachine(t *testing.T) {
	b, poolPath := newTestBackend(t)
	vol := Volume{
		ID:     "vol-lifecycle",
		PoolID: "pool-test",
		Kind:   VolumeKindDir,
		SizeMB: 5120,
		Status: VolumeStatusCreating,
	}

	// creating：落库后、建目录前，VolumeInfo 应观测 missing。
	if _, err := b.VolumeInfo(vol); err != nil {
		t.Fatalf("VolumeInfo() during creating failed: %v", err)
	}

	// creating → available：创建卷目录，目录真实存在。
	if err := b.CreateVolume(vol); err != nil {
		t.Fatalf("CreateVolume() failed: %v", err)
	}
	vol.Status = VolumeStatusAvailable
	info, err := b.VolumeInfo(vol)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "available" {
		t.Fatalf("volume status = %q, want available", info.Status)
	}
	wantPath := filepath.Join(poolPath, "lxc", vol.ID)
	if info.Path != wantPath {
		t.Fatalf("volume path = %q, want %q", info.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(info.Path)); err != nil {
		t.Fatalf("volume directory missing on disk: %v", err)
	}

	// available → attached：绑定容器（记录层语义，文件系统状态不变）。
	vol.AttachedToContainerID = 7
	vol.Status = VolumeStatusAttached
	info, err = b.VolumeInfo(vol)
	if err != nil || info.Status != "available" {
		t.Fatalf("attached volume observation = %+v, err=%v", info, err)
	}

	// attached → deleting：删除卷目录（幂等）。
	vol.Status = VolumeStatusDeleting
	if err := b.DeleteVolume(vol); err != nil {
		t.Fatalf("DeleteVolume() failed: %v", err)
	}
	if err := b.DeleteVolume(vol); err != nil {
		t.Fatalf("DeleteVolume() second call must be idempotent: %v", err)
	}
	info, err = b.VolumeInfo(vol)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "missing" {
		t.Fatalf("volume status after delete = %q, want missing", info.Status)
	}
}

func TestCreateVolumeRejectsDuplicate(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-dup", Kind: VolumeKindDir}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	err := b.CreateVolume(vol)
	if !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("duplicate CreateVolume() error = %v, want ErrVolumeExists", err)
	}
}

func TestCreateVolumeRejectsBlockKind(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-block", Kind: VolumeKindBlock}
	err := b.CreateVolume(vol)
	if !errors.Is(err, ErrBlockNotSupported) {
		t.Fatalf("block volume on dir backend error = %v, want ErrBlockNotSupported", err)
	}
}

func TestCreateVolumeRequiresID(t *testing.T) {
	b, _ := newTestBackend(t)
	if err := b.CreateVolume(Volume{ID: "  ", Kind: VolumeKindDir}); err == nil {
		t.Fatal("expected error for empty volume id")
	}
}

func TestResizeVolumeRejectsShrink(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-resize", Kind: VolumeKindDir, SizeMB: 10240}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}

	err := b.ResizeVolume(vol, 5120)
	if !errors.Is(err, ErrShrinkNotSupported) {
		t.Fatalf("shrink error = %v, want ErrShrinkNotSupported", err)
	}
	if !strings.Contains(err.Error(), "10240") || !strings.Contains(err.Error(), "5120") {
		t.Fatalf("shrink error should include current/requested sizes: %v", err)
	}

	if err := b.ResizeVolume(vol, 10240); err != nil {
		t.Fatalf("same-size resize should be a no-op, got %v", err)
	}
	if err := b.ResizeVolume(vol, 20480); err != nil {
		t.Fatalf("grow resize failed: %v", err)
	}
	if err := b.ResizeVolume(vol, 0); err == nil {
		t.Fatal("expected error for zero size")
	}
	if err := b.ResizeVolume(vol, -1); err == nil {
		t.Fatal("expected error for negative size")
	}
}

func TestResizeVolumeRequiresExistingVolume(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-missing", Kind: VolumeKindDir, SizeMB: 1024}
	err := b.ResizeVolume(vol, 2048)
	if !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("resize missing volume error = %v, want ErrVolumeNotFound", err)
	}
}

func TestSnapshotAndRestoreVolume(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-snap", Kind: VolumeKindDir}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(b.PoolPath(), "lxc", vol.ID, "rootfs", "marker.txt"), "before")

	if err := b.SnapshotVolume(vol, "snap-1"); err != nil {
		t.Fatalf("SnapshotVolume() failed: %v", err)
	}
	snapPath := filepath.Join(b.PoolPath(), "snapshots", vol.ID, "snap-1")
	if _, err := os.Stat(filepath.Join(snapPath, "rootfs", "marker.txt")); err != nil {
		t.Fatalf("snapshot content missing: %v", err)
	}

	if err := b.SnapshotVolume(vol, "snap-1"); !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("duplicate snapshot error = %v, want ErrVolumeExists", err)
	}

	// 修改卷内容后回滚，快照内容必须完好。
	mustWriteFile(t, filepath.Join(b.PoolPath(), "lxc", vol.ID, "rootfs", "marker.txt"), "after")
	if err := b.RestoreSnapshot(vol, "snap-1"); err != nil {
		t.Fatalf("RestoreSnapshot() failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(b.PoolPath(), "lxc", vol.ID, "rootfs", "marker.txt"))
	if err != nil || string(data) != "before" {
		t.Fatalf("restore did not roll back content: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("snapshot must survive restore: %v", err)
	}

	if err := b.RestoreSnapshot(vol, "snap-none"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("restore missing snapshot error = %v, want ErrVolumeNotFound", err)
	}
}

func TestCloneVolume(t *testing.T) {
	b, _ := newTestBackend(t)
	src := Volume{ID: "vol-src", Kind: VolumeKindDir}
	if err := b.CreateVolume(src); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(b.PoolPath(), "lxc", src.ID, "rootfs", "marker.txt"), "payload")

	dst := Volume{ID: "vol-dst", Kind: VolumeKindDir}
	if err := b.CloneVolume(src, dst, CloneModeFull); err != nil {
		t.Fatalf("CloneVolume(full) failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(b.PoolPath(), "lxc", dst.ID, "rootfs", "marker.txt"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("clone content mismatch: data=%q err=%v", data, err)
	}

	if err := b.CloneVolume(src, dst, CloneModeLinked); !errors.Is(err, ErrLinkedCloneNotSupported) {
		t.Fatalf("linked clone error = %v, want ErrLinkedCloneNotSupported", err)
	}
	if err := b.CloneVolume(src, Volume{ID: "vol-x"}, "bogus"); err == nil {
		t.Fatal("expected error for invalid clone mode")
	}
	missing := Volume{ID: "vol-nope", Kind: VolumeKindDir}
	if err := b.CloneVolume(missing, Volume{ID: "vol-dst2", Kind: VolumeKindDir}, CloneModeFull); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("clone missing source error = %v, want ErrVolumeNotFound", err)
	}
}

func TestVolumeInfoReportsActualSize(t *testing.T) {
	b, _ := newTestBackend(t)
	vol := Volume{ID: "vol-size", Kind: VolumeKindDir}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(b.PoolPath(), "lxc", vol.ID, "blob.bin"), strings.Repeat("a", 3*1024*1024))
	info, err := b.VolumeInfo(vol)
	if err != nil {
		t.Fatal(err)
	}
	if info.ActualSizeMB < 2 || info.ActualSizeMB > 4 {
		t.Fatalf("ActualSizeMB = %d, want ~3", info.ActualSizeMB)
	}
}

// TestPathTraversalRejected 覆盖 ../、绝对路径、分隔符、点号等
// 片段级逃逸：卷 ID / 快照 ID 直接来自外部输入，必须在片段层拒绝。
func TestPathTraversalRejected(t *testing.T) {
	b, poolPath := newTestBackend(t)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}

	unsafeIDs := []string{
		"../escape",
		"..\\escape",
		"..",
		".",
		"",
		"/etc/passwd",
		"sub/dir",
		"sub" + string(os.PathSeparator) + "dir",
	}
	for _, id := range unsafeIDs {
		vol := Volume{ID: id, Kind: VolumeKindDir}
		if err := b.CreateVolume(vol); err == nil {
			t.Fatalf("CreateVolume accepted unsafe volume id %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(poolPath), "escape")); !os.IsNotExist(err) {
		t.Fatalf("../ escape created a directory outside the pool: %v", err)
	}
	if _, err := os.Stat("/etc/passwd/lxc"); err == nil {
		t.Fatal("absolute path volume was created")
	}

	vol := Volume{ID: "vol-safe", Kind: VolumeKindDir}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	for _, snapID := range []string{"../evil", "/abs", "a/b", "..", ""} {
		if err := b.SnapshotVolume(vol, snapID); err == nil {
			t.Fatalf("SnapshotVolume accepted unsafe snapshot id %q", snapID)
		}
	}

	// safeJoinUnder 片段级直接断言。
	for _, parts := range [][]string{
		{"..", "x"},
		{"x", ".."},
		{"/etc"},
		{"a/b"},
		{"."},
		{""},
	} {
		if _, err := safeJoinUnder(poolPath, parts...); err == nil {
			t.Fatalf("safeJoinUnder accepted unsafe parts %v", parts)
		}
	}
}

// TestSymlinkEscapeRejected 覆盖符号链接逃逸三类场景：
//  1. 终点是符号链接 → 直接拒绝（防止预置链接重定向写入）；
//  2. 中间片段是符号链接且指向池外 → 拒绝；
//  3. 中间片段是符号链接但仍指向池内 → 放行（合法的池内重排）。
func TestSymlinkEscapeRejected(t *testing.T) {
	b, poolPath := newTestBackend(t)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	// 场景 1：终点是符号链接 → safeJoinUnder 直接拒绝（不许通过预置链接
	// 把卷写入重定向到池外），CreateVolume 与 VolumeInfo 都必须失败。
	volLink := Volume{ID: "vol-link", Kind: VolumeKindDir}
	if err := os.MkdirAll(filepath.Join(poolPath, "lxc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(poolPath, "lxc", "vol-link")); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateVolume(volLink); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked volume path CreateVolume() error = %v, want symlink refusal", err)
	}
	if _, err := b.VolumeInfo(volLink); err == nil {
		t.Fatal("VolumeInfo must refuse symlink at volume path")
	}

	// 场景 2：快照路径中间片段（卷 ID）是指向池外的符号链接。
	volEscape := Volume{ID: "vol-escape", Kind: VolumeKindDir}
	escapeDir := filepath.Join(poolPath, "snapshots", "vol-escape")
	if err := os.MkdirAll(escapeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(escapeDir, "target")); err != nil {
		t.Fatal(err)
	}
	// 让 vol-escape 成为指向池外的符号链接本身（中间片段）。
	if err := os.RemoveAll(escapeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(poolPath, "snapshots", "vol-escape")); err != nil {
		t.Fatal(err)
	}
	if err := b.SnapshotVolume(volEscape, "snap-1"); err == nil {
		t.Fatal("SnapshotVolume must refuse symlinked intermediate path escaping pool")
	}

	// 场景 3：池内符号链接（lxc → lxc-real）合法放行。
	realDir := filepath.Join(poolPath, "lxc-real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(poolPath, "lxc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(poolPath, "lxc")); err != nil {
		t.Fatal(err)
	}
	volOK := Volume{ID: "vol-ok", Kind: VolumeKindDir}
	if err := b.CreateVolume(volOK); err != nil {
		t.Fatalf("in-pool symlink must be accepted: %v", err)
	}
	info, err := b.VolumeInfo(volOK)
	if err != nil || info.Status != "available" {
		t.Fatalf("in-pool symlink volume observation = %+v, err=%v", info, err)
	}
}

func TestSafeJoinUnderRequiresExistingAbsoluteBase(t *testing.T) {
	if _, err := safeJoinUnder("relative/base", "x"); err == nil {
		t.Fatal("expected error for relative base")
	}
	if _, err := safeJoinUnder(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Fatal("expected error for non-existent base")
	}
}

func TestNormalizeBackendKind(t *testing.T) {
	cases := map[string]string{
		"":        BackendDir,
		"dir":     BackendDir,
		"  DIR  ": BackendDir,
		"zfs":     BackendZFS,
		"lvm":     BackendLVM,
		"rbd":     BackendRBD,
		"cephfs":  BackendCephFS,
		"nfs":     BackendNFS,
		"bogus":   BackendDir,
	}
	for in, want := range cases {
		if got := NormalizeBackendKind(in); got != want {
			t.Fatalf("NormalizeBackendKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCommandRunnerInterface 确保 P0-2 命令型后端可以注入 mock：
// 编译期断言 OSCommandRunner 实现接口，行为断言用 fake runner。
func TestCommandRunnerInterface(t *testing.T) {
	var _ CommandRunner = OSCommandRunner{}

	fake := &fakeRunner{output: "ok"}
	got, err := fake.Run("zfs", "list", "-t", "filesystem")
	if err != nil || got != "ok" {
		t.Fatalf("fake runner = %q, %v", got, err)
	}
	if fake.calls != 1 || len(fake.lastArgs) != 3 || fake.lastArgs[0] != "list" {
		t.Fatalf("fake runner args = %v (calls=%d)", fake.lastArgs, fake.calls)
	}

	out, err := OSCommandRunner{}.Run("true")
	if err != nil || out != "" {
		t.Fatalf("OSCommandRunner(true) = %q, %v", out, err)
	}
}

type fakeRunner struct {
	output   string
	calls    int
	lastArgs []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls++
	f.lastArgs = args
	return f.output, nil
}
