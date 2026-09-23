package storage

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func newLVMBackend(t *testing.T, cfg map[string]string, runner CommandRunner) *LVMBackend {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{}
	}
	if _, ok := cfg[LVMConfigKeyVG]; !ok {
		cfg[LVMConfigKeyVG] = "vg0"
	}
	if _, ok := cfg[LVMConfigKeyThinPool]; !ok {
		cfg[LVMConfigKeyThinPool] = "thinpool0"
	}
	b, err := NewLVMBackend("pool-lvm", cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLVMBackendRequiresVGAndThinPool(t *testing.T) {
	if _, err := NewLVMBackend("p", map[string]string{}, newScriptRunner()); err == nil {
		t.Fatal("missing vg must error")
	}
	cfg := map[string]string{LVMConfigKeyVG: "vg0"}
	if _, err := NewLVMBackend("p", cfg, newScriptRunner()); err == nil {
		t.Fatal("missing thinpool must error")
	}
	for _, badName := range []string{"vg/with/slash", "vg..", "-starts-with-dash", "vg\\back"} {
		if _, err := NewLVMBackend("p", map[string]string{LVMConfigKeyVG: badName, LVMConfigKeyThinPool: "tp"}, newScriptRunner()); err == nil {
			t.Fatalf("unsafe VG name %q must be rejected", badName)
		}
	}
}

func TestLVMBackendEnsurePool(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/thinpool0"}, "  thinpool0\n", nil)
	b := newLVMBackend(t, nil, r)
	if err := b.EnsurePool(); err != nil {
		t.Fatal(err)
	}
}

func TestLVMBackendEnsurePoolMissingThinPool(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/thinpool0"}, "",
		errors.New("Failed to find logical volume vg0/thinpool0"))
	b := newLVMBackend(t, nil, r)
	if err := b.EnsurePool(); err == nil {
		t.Fatal("EnsurePool must surface thin pool unavailability")
	}
}

func TestLVMBackendCreateVolumeCommand(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/thinpool0"}, "  thinpool0\n", nil)
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-1"},
		"", errors.New("Failed to find logical volume vg0/vol-1"))
	r.onCmd("lvcreate", []string{"-T", "vg0/thinpool0", "-V", "5120M", "--name", "vol-1", "-y"}, "", nil)
	b := newLVMBackend(t, nil, r)
	vol := Volume{ID: "vol-1", PoolID: "pool-lvm", Kind: VolumeKindBlock, SizeMB: 5120}
	if err := b.CreateVolume(vol); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("lvcreate")
	if len(calls) != 1 {
		t.Fatalf("expected 1 lvcreate call, got %d", len(calls))
	}
	got := calls[0]
	if got[len(got)-1] != "-y" || got[len(got)-2] != "vol-1" {
		t.Fatalf("lvcreate args tail = %v, want vol-1 -y", got[len(got)-2:])
	}
	if got[0] != "-T" || got[1] != "vg0/thinpool0" || got[2] != "-V" || got[3] != "5120M" {
		t.Fatalf("lvcreate args head = %v, want -T vg0/thinpool0 -V 5120M", got[:4])
	}
}

func TestLVMBackendCreateVolumeRejectsDirKind(t *testing.T) {
	r := newScriptRunner()
	b := newLVMBackend(t, nil, r)
	err := b.CreateVolume(Volume{ID: "vol-1", Kind: VolumeKindDir, SizeMB: 100})
	if !errors.Is(err, ErrBlockNotSupported) {
		t.Fatalf("dir kind on lvm backend must be ErrBlockNotSupported, got %v", err)
	}
}

func TestLVMBackendCreateVolumeRejectsExisting(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/thinpool0"}, "  thinpool0\n", nil)
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-dup"}, "  vol-dup\n", nil)
	b := newLVMBackend(t, nil, r)
	err := b.CreateVolume(Volume{ID: "vol-dup", Kind: VolumeKindBlock, SizeMB: 100})
	if !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("existing LV error = %v, want ErrVolumeExists", err)
	}
}

func TestLVMBackendCreateVolumeRejectsUnsafeID(t *testing.T) {
	r := newScriptRunner()
	b := newLVMBackend(t, nil, r)
	for _, id := range []string{"../escape", "-starts-with-dash", "vol/sub", "vol@bad", "vol#bad"} {
		if err := b.CreateVolume(Volume{ID: id, Kind: VolumeKindBlock, SizeMB: 100}); err == nil {
			t.Fatalf("unsafe LV id %q must be rejected", id)
		}
	}
}

func TestLVMBackendDeleteVolumeCommand(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-d"}, "  vol-d\n", nil)
	r.onCmd("lvremove", []string{"-f", "vg0/vol-d"}, "", nil)
	b := newLVMBackend(t, nil, r)
	if err := b.DeleteVolume(Volume{ID: "vol-d", Kind: VolumeKindBlock}); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("lvremove")
	if len(calls) != 1 || calls[0][0] != "-f" || calls[0][1] != "vg0/vol-d" {
		t.Fatalf("lvremove args = %v, want -f vg0/vol-d", calls)
	}
}

func TestLVMBackendDeleteVolumeIdempotent(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-m"}, "",
		errors.New("Failed to find logical volume vg0/vol-m"))
	b := newLVMBackend(t, nil, r)
	if err := b.DeleteVolume(Volume{ID: "vol-m", Kind: VolumeKindBlock}); err != nil {
		t.Fatalf("DeleteVolume of missing LV must be idempotent: %v", err)
	}
}

func TestLVMBackendResizeVolumeGrowOnly(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-r"}, "  vol-r\n", nil)
	r.onCmd("lvextend", []string{"-L", "20M", "vg0/vol-r"}, "", nil)
	b := newLVMBackend(t, nil, r)
	if err := b.ResizeVolume(Volume{ID: "vol-r", Kind: VolumeKindBlock, SizeMB: 10}, 20); err != nil {
		t.Fatalf("ResizeVolume failed: %v", err)
	}
	calls := r.callsByName("lvextend")
	if len(calls) != 1 || calls[0][1] != "20M" {
		t.Fatalf("lvextend args = %v, want -L 20M", calls)
	}
	err := b.ResizeVolume(Volume{ID: "vol-r", Kind: VolumeKindBlock, SizeMB: 20}, 10)
	if !errors.Is(err, ErrShrinkNotSupported) {
		t.Fatalf("shrink must error, got %v", err)
	}
}

func TestLVMBackendSnapshotVolume(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-s"}, "  vol-s\n", nil)
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/snap-1"}, "",
		errors.New("Failed to find logical volume vg0/snap-1"))
	r.onCmd("lvcreate", []string{"-s", "vg0/vol-s", "--name", "snap-1", "-y"}, "", nil)
	b := newLVMBackend(t, nil, r)
	vol := Volume{ID: "vol-s", Kind: VolumeKindBlock}
	if err := b.SnapshotVolume(vol, "snap-1"); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("lvcreate")
	if len(calls) != 1 || calls[0][0] != "-s" || calls[0][1] != "vg0/vol-s" {
		t.Fatalf("lvcreate snapshot args = %v", calls)
	}
}

func TestLVMBackendRestoreSnapshotMergesAndReactivates(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/snap-1"}, "  snap-1\n", nil)
	r.onCmd("lvconvert", []string{"--merge", "vg0/snap-1"}, "", nil)
	r.onCmd("lvchange", []string{"-ay", "vg0/vol-s"}, "", nil)
	b := newLVMBackend(t, nil, r)
	if err := b.RestoreSnapshot(Volume{ID: "vol-s", Kind: VolumeKindBlock}, "snap-1"); err != nil {
		t.Fatal(err)
	}
	if calls := r.callsByName("lvconvert"); len(calls) != 1 || calls[0][0] != "--merge" {
		t.Fatalf("lvconvert calls = %v", calls)
	}
	if calls := r.callsByName("lvchange"); len(calls) != 1 || calls[0][0] != "-ay" || calls[0][1] != "vg0/vol-s" {
		t.Fatalf("lvchange calls = %v, want -ay vg0/vol-s", calls)
	}
}

func TestLVMBackendRestoreSnapshotMissing(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/no-snap"}, "",
		errors.New("Failed to find logical volume vg0/no-snap"))
	b := newLVMBackend(t, nil, r)
	err := b.RestoreSnapshot(Volume{ID: "vol-s", Kind: VolumeKindBlock}, "no-snap")
	if !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("restore missing snapshot error = %v, want ErrVolumeNotFound", err)
	}
}

func TestLVMBackendCloneVolumeLinkedRejected(t *testing.T) {
	r := newScriptRunner()
	b := newLVMBackend(t, nil, r)
	err := b.CloneVolume(Volume{ID: "src"}, Volume{ID: "dst"}, CloneModeLinked)
	if !errors.Is(err, ErrLinkedCloneNotSupported) {
		t.Fatalf("linked clone error = %v, want ErrLinkedCloneNotSupported", err)
	}
}

func TestLVMBackendAttachDetach(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "-o", "lv_name", "vg0/vol-a"}, "  vol-a\n", nil)
	r.onCmd("lvchange", []string{"-ay", "vg0/vol-a"}, "", nil)
	r.onCmd("lvchange", []string{"-an", "vg0/vol-a"}, "", nil)
	b := newLVMBackend(t, nil, r)
	if err := b.Attach(Volume{ID: "vol-a"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Detach(Volume{ID: "vol-a"}); err != nil {
		t.Fatal(err)
	}
	calls := r.callsByName("lvchange")
	if len(calls) != 2 || calls[0][0] != "-ay" || calls[1][0] != "-an" {
		t.Fatalf("lvchange calls = %v, want [-ay/-an]", calls)
	}
}

func TestLVMBackendDevicePath(t *testing.T) {
	b := newLVMBackend(t, nil, newScriptRunner())
	if got := b.DevicePath(Volume{ID: "vol-x"}); got != "/dev/vg0/vol-x" {
		t.Fatalf("DevicePath = %q, want /dev/vg0/vol-x", got)
	}
}

func TestLVMBackendPoolStatsParse(t *testing.T) {
	r := newScriptRunner()
	r.onCmd("lvs", []string{"--noheadings", "--nosuffix", "-o", "lv_name,data_percent,metadata_percent", "vg0/thinpool0"},
		"  thinpool0  42  78\n", nil)
	b := newLVMBackend(t, nil, r)
	data, meta, ok := b.PoolStats()
	if !ok {
		t.Fatal("PoolStats must parse 42/78")
	}
	if data != 42 || meta != 78 {
		t.Fatalf("data=%d meta=%d, want 42/78", data, meta)
	}
}

func TestLVMBackendMetadataThresholdsDefaults(t *testing.T) {
	b := newLVMBackend(t, nil, newScriptRunner())
	warn, critical := b.MetadataThresholds()
	if warn != DefaultLVMMetadataWarn || critical != DefaultLVMMetadataCritical {
		t.Fatalf("thresholds = %d/%d, want %d/%d", warn, critical, DefaultLVMMetadataWarn, DefaultLVMMetadataCritical)
	}
}

func TestLVMBackendMetadataThresholdsCustom(t *testing.T) {
	cfg := map[string]string{
		LVMConfigKeyVG: "vg0", LVMConfigKeyThinPool: "tp",
		LVMConfigKeyMetadataWarn: "70", LVMConfigKeyMetadataCritical: "90",
	}
	b, err := NewLVMBackend("p", cfg, newScriptRunner())
	if err != nil {
		t.Fatal(err)
	}
	warn, critical := b.MetadataThresholds()
	if warn != 70 || critical != 90 {
		t.Fatalf("custom thresholds = %d/%d, want 70/90", warn, critical)
	}
}

func TestParseLVSLine(t *testing.T) {
	cases := []struct {
		line     string
		wantOK   bool
		wantName string
		wantSize int64
	}{
		{"  vol-1  10240.00", true, "vol-1", 10240},
		{"  vol-1  0.00", true, "vol-1", 0},
		{"only-one-field", false, "", 0},
		{"", false, "", 0},
	}
	for _, c := range cases {
		name, size, ok := parseLVSLine(c.line)
		if ok != c.wantOK {
			t.Fatalf("parseLVSLine(%q) ok=%v, want %v", c.line, ok, c.wantOK)
		}
		if !ok {
			continue
		}
		if name != c.wantName || size != c.wantSize {
			t.Fatalf("parseLVSLine(%q) = %s/%d, want %s/%d", c.line, name, size, c.wantName, c.wantSize)
		}
	}
}

func TestParseLVSDataMeta(t *testing.T) {
	data, meta, ok := parseLVSDataMeta("  thinpool0  42  78", "thinpool0")
	if !ok || data != 42 || meta != 78 {
		t.Fatalf("parseLVSDataMeta = %d/%d/%v, want 42/78/true", data, meta, ok)
	}
	if _, _, ok := parseLVSDataMeta("  otherpool  1  2", "thinpool0"); ok {
		t.Fatal("parseLVSDataMeta must reject unexpected name")
	}
	if _, _, ok := parseLVSDataMeta("only-one", "x"); ok {
		t.Fatal("parseLVSDataMeta must reject too few fields")
	}
}

// 安全校验覆盖：防止 LV/VG/snapshot 名片段逃逸（命令行注入）。
func TestLVMBackendRejectsUnsafeSegment(t *testing.T) {
	for _, seg := range []string{"../escape", "-option-like", "with/slash", "with\\back", "with:colon", "with@at", "with#hash", "", "."} {
		if isSafeLVMSegment(seg) {
			t.Fatalf("unsafe segment %q must be rejected", seg)
		}
	}
	for _, seg := range []string{"vol-1", "lv_2", "lv.3", "thinpool0"} {
		if !isSafeLVMSegment(seg) {
			t.Fatalf("valid segment %q rejected", seg)
		}
	}
}

func TestLVMBackendRunnerIntegration(t *testing.T) {
	if _, err := exec.LookPath("lvs"); err != nil {
		t.Skip("lvs binary not available")
	}
	b := newLVMBackend(t, nil, nil)
	if err := b.EnsurePool(); err == nil {
		t.Skip("lvm runtime not functional on this host (thin pool missing)")
	}
}

// 防止 import 折叠
var _ = strings.Fields