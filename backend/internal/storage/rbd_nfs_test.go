package storage

import (
	"errors"
	"os/exec"
	"testing"
)

func newRBDBackend(t *testing.T, cfg map[string]string, runner CommandRunner) *RBDBackend {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{}
	}
	if _, ok := cfg[RBDConfigKeyPool]; !ok {
		cfg[RBDConfigKeyPool] = "rbd"
	}
	b, err := NewRBDBackend("pool-rbd", cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRBDBackendRequiresPool(t *testing.T) {
	if _, err := NewRBDBackend("p", map[string]string{}, newScriptRunner()); err == nil {
		t.Fatal("missing pool must error")
	}
	if _, err := NewRBDBackend("p", map[string]string{RBDConfigKeyPool: "rbd/bad"}, newScriptRunner()); err == nil {
		t.Fatal("pool name with slash must error")
	}
}

func TestRBDBackendMonitorsParse(t *testing.T) {
	b := newRBDBackend(t, map[string]string{RBDConfigKeyMonitors: "10.0.0.1:6789,10.0.0.2:6789"}, newScriptRunner())
	if got := b.Monitors(); len(got) != 2 || got[0] != "10.0.0.1:6789" {
		t.Fatalf("Monitors = %v", got)
	}
}

func TestRBDBackendEnsurePool(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("rbd", []string{"pool", "stats", "rbd", "--format", "json"}, `{"images":0}`, nil)
	b := newRBDBackend(t, nil, r)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
}

func TestRBDBackendCreateVolumeCommand(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("rbd", []string{"pool", "stats", "rbd", "--format", "json"}, "", nil)
	r.onCmd("rbd", []string{"info", "rbd/eyves-vol-1"}, "", errors.New("image not found"))
	r.onCmd("rbd", []string{"create", "--pool", "rbd", "--size", "10240", "eyves-vol-1"}, "", nil)
	b := newRBDBackend(t, nil, r)
	vol := Volume{ID: "vol-1", PoolID: "pool-rbd", Kind: VolumeKindBlock, SizeMB: 10240}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("rbd")
	var create []string
	for _, args := range calls {
		if len(args) > 0 && args[0] == "create" {
			create = args
		}
	}
	if create == nil {
		t.Fatal("expected rbd create call")
	}
	if create[len(create)-1] != "eyves-vol-1" {
		t.Fatalf("create target = %v, want eyves-vol-1", create)
	}
}

func TestRBDBackendCreateVolumeRejectsDirKind(t *testing.T) {
	b := newRBDBackend(t, nil, newScriptRunner())
	err := b.CreateVolume(Volume{ID: "v", Kind: VolumeKindDir, SizeMB: 100})
	if !errors.Is(err, ErrBlockNotSupported) {
		t.Fatalf("dir kind on rbd must error, got %v", err)
	}
}

func TestRBDBackendResizeVolumeGrowOnly(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("rbd", []string{"info", "rbd/eyves-vol-1"}, "", nil)
	r.onCmd("rbd", []string{"resize", "--pool", "rbd", "--size", "20", "eyves-vol-1"}, "", nil)
	b := newRBDBackend(t, nil, r)
	if err := b.ResizeVolume(Volume{ID: "vol-1", Kind: VolumeKindBlock, SizeMB: 10}, 20); err != nil {
		t.Fatal(err)
	}
	err := b.ResizeVolume(Volume{ID: "vol-1", Kind: VolumeKindBlock, SizeMB: 20}, 10)
	if !errors.Is(err, ErrShrinkNotSupported) {
		t.Fatalf("shrink error = %v, want ErrShrinkNotSupported", err)
	}
}

func TestRBDBackendSnapshotAndRestore(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("rbd", []string{"info", "rbd/eyves-vol-1"}, "", nil)
	r.onCmd("rbd", []string{"snap", "create", "--pool", "rbd", "--snap", "snap-1", "eyves-vol-1"}, "", nil)
	r.onCmd("rbd", []string{"info", "rbd/eyves-vol-1@snap-1"}, "", nil)
	r.onCmd("rbd", []string{"snap", "rollback", "rbd/eyves-vol-1@snap-1"}, "", nil)
	b := newRBDBackend(t, nil, r)
	vol := Volume{ID: "vol-1", Kind: VolumeKindBlock}
	if err := b.SnapshotVolume(vol, "snap-1"); err != nil {
		t.Fatal(err)
	}
	if err := b.RestoreSnapshot(vol, "snap-1"); err != nil {
		t.Fatal(err)
	}
}

func TestRBDBackendCloneVolumeLinkedRejected(t *testing.T) {
	b := newRBDBackend(t, nil, newScriptRunner())
	err := b.CloneVolume(Volume{ID: "src"}, Volume{ID: "dst"}, CloneModeLinked)
	if !errors.Is(err, ErrLinkedCloneNotSupported) {
		t.Fatalf("linked clone error = %v", err)
	}
}

func TestRBDBackendImageRef(t *testing.T) {
	b := newRBDBackend(t, nil, newScriptRunner())
	if got := b.ImageRef(Volume{ID: "vol-1"}); got != "rbd/eyves-vol-1" {
		t.Fatalf("ImageRef = %q, want rbd/eyves-vol-1", got)
	}
}

func TestParseRBDInfoSize(t *testing.T) {
	cases := []struct {
		out     string
		wantOK  bool
		wantMB  int64
	}{
		{`{"name":"v","size":10485760}`, true, 10},
		{`{"name":"v","size":1073741824,"foo":1}`, true, 1024},
		{"garbage", false, 0},
	}
	for _, c := range cases {
		size, ok := parseRBDInfoSize(c.out)
		if ok != c.wantOK {
			t.Fatalf("parseRBDInfoSize(%q) ok=%v", c.out, ok)
		}
		if ok && size != c.wantMB {
			t.Fatalf("parseRBDInfoSize(%q) = %d MB, want %d", c.out, size, c.wantMB)
		}
	}
}

func TestRBDBackendRunnerIntegration(t *testing.T) {
	if _, err := exec.LookPath("rbd"); err != nil {
		t.Skip("rbd binary not available")
	}
	b := newRBDBackend(t, nil, nil)
	if err := b.EnsurePool(); err == nil {
		t.Skip("ceph cluster not available")
	}
}

func newNFSBackend(t *testing.T, cfg map[string]string, runner CommandRunner) *NFSBackend {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{}
	}
	if _, ok := cfg[NFSConfigKeyServer]; !ok {
		cfg[NFSConfigKeyServer] = "10.0.0.1"
	}
	if _, ok := cfg[NFSConfigKeyExport]; !ok {
		cfg[NFSConfigKeyExport] = "/data/eyvescloud"
	}
	poolPath := t.TempDir()
	b, err := NewNFSBackend("pool-nfs", poolPath, cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNFSBackendRequiresServerExport(t *testing.T) {
	if _, err := NewNFSBackend("p", t.TempDir(), map[string]string{}, newScriptRunner()); err == nil {
		t.Fatal("missing server/export must error")
	}
	if _, err := NewNFSBackend("p", t.TempDir(),
		map[string]string{NFSConfigKeyServer: "with space", NFSConfigKeyExport: "/x"}, newScriptRunner()); err == nil {
		t.Fatal("server with space must error")
	}
}

func TestNFSBackendEnsurePoolMountsOnDemand(t *testing.T) {
	// 已挂载：findmnt 返回路径 → EnsurePool 通过（幂等）。
	r := newScriptRunner()
	r.onCmd("findmnt", []string{"-T", "", "-o", "TARGET", "-n"}, "/tmp/nfs-pool\n", nil)
	b := newNFSBackend(t, nil, r)
	if err := b.EnsurePool(); err != nil {
		t.Fatalf("EnsurePool must succeed when already mounted: %v", err)
	}
	// 未挂载：findmnt 返回空 + mount 命令 mock。
	r2 := newScriptRunner()
	r2.onCmd("findmnt", []string{"-T", "", "-o", "TARGET", "-n"}, "", nil)
	r2.onCmd("mount", []string{"-t", "nfs", "10.0.0.1:/data/eyvescloud", ""}, "", nil)
	b2 := newNFSBackend(t, nil, r2)
	if err := b2.EnsurePool(); err != nil {
		t.Fatalf("EnsurePool must invoke mount when not mounted: %v", err)
	}
}

func TestNFSBackendCreateVolumeRejectsBlockKind(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("findmnt", []string{"-T", "", "-o", "TARGET", "-n"}, "", nil)
	b := newNFSBackend(t, nil, r)
	err := b.CreateVolume(Volume{ID: "vol", Kind: VolumeKindBlock})
	if !errors.Is(err, ErrBlockNotSupported) {
		t.Fatalf("block kind on nfs must error, got %v", err)
	}
}

func TestNFSBackendResizeRejected(t *testing.T) {
	b := newNFSBackend(t, nil, newScriptRunner())
	err := b.ResizeVolume(Volume{ID: "v", Kind: VolumeKindDir}, 100)
	if !errors.Is(err, ErrShrinkNotSupported) {
		t.Fatalf("NFS resize must error, got %v", err)
	}
}

func TestNFSBackendSnapshotRejected(t *testing.T) {
	b := newNFSBackend(t, nil, newScriptRunner())
	if err := b.SnapshotVolume(Volume{ID: "v"}, "s"); err == nil {
		t.Fatal("NFS snapshot must error")
	}
	if err := b.RestoreSnapshot(Volume{ID: "v"}, "s"); err == nil {
		t.Fatal("NFS restore must error")
	}
}

func TestNFSBackendCloneVolumeLinkedRejected(t *testing.T) {
	b := newNFSBackend(t, nil, newScriptRunner())
	err := b.CloneVolume(Volume{ID: "src"}, Volume{ID: "dst"}, CloneModeLinked)
	if !errors.Is(err, ErrLinkedCloneNotSupported) {
		t.Fatalf("linked clone on NFS must error, got %v", err)
	}
}

func TestBackendForPoolDispatchesAll(t *testing.T) {
	_, err := BackendForPool("p", "/tmp/p", "rbd", map[string]string{RBDConfigKeyPool: "rbd"}, nil)
	if err != nil {
		t.Fatalf("rbd factory: %v", err)
	}
	_, err = BackendForPool("p", t.TempDir(), "nfs",
		map[string]string{NFSConfigKeyServer: "x", NFSConfigKeyExport: "/y"}, nil)
	if err != nil {
		t.Fatalf("nfs factory: %v", err)
	}
	_, err = BackendForPool("p", "/tmp/p", "cephfs", nil, nil)
	if err == nil {
		t.Fatal("cephfs factory must error (not yet implemented)")
	}
}