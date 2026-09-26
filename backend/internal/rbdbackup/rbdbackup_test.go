package rbdbackup

import (
	"strings"
	"testing"
	"time"
)

func TestCursorFingerprintStableAndDistinct(t *testing.T) {
	a := Cursor{Backend: BackendRBD, Instance: "ct-1"}
	b := Cursor{Backend: BackendRBD, Instance: "ct-1"}
	c := Cursor{Backend: BackendRBD, Instance: "ct-2"}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("same cursor must produce same fingerprint")
	}
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatal("different instance must produce different fingerprint")
	}
	if len(a.Fingerprint()) != 16 {
		t.Fatalf("fingerprint len = %d, want 16", len(a.Fingerprint()))
	}
}

func TestBuildRBDCommandsFullAndIncremental(t *testing.T) {
	full, err := BuildRBDCommands(RBDConfig{Pool: "rbd", ImageName: "eyves-vol-1", BackupPath: "/tmp/full.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(full[0], " "), "export rbd/eyves-vol-1 /tmp/full.bin") {
		t.Fatalf("full export = %v", full[0])
	}

	incr, err := BuildRBDCommands(RBDConfig{Pool: "rbd", ImageName: "eyves-vol-1", Snapshot: "snap-1", BackupPath: "/tmp/incr.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(incr[0], " "), "export-diff --from-snap snap-1 rbd/eyves-vol-1 /tmp/incr.bin") {
		t.Fatalf("incr export = %v", incr[0])
	}
}

func TestBuildRBDCommandsRejectsUnsafe(t *testing.T) {
	if _, err := BuildRBDCommands(RBDConfig{Pool: "rbd/bad", ImageName: "x", BackupPath: "/tmp/x"}); err == nil {
		t.Fatal("unsafe pool must error")
	}
	if _, err := BuildRBDCommands(RBDConfig{Pool: "rbd", ImageName: "x", Snapshot: "../bad", BackupPath: "/tmp/x"}); err == nil {
		t.Fatal("unsafe snapshot must error")
	}
	if _, err := BuildRBDCommands(RBDConfig{Pool: "rbd", ImageName: "x", BackupPath: ""}); err == nil {
		t.Fatal("empty backup_path must error")
	}
}

func TestBuildDirTarCommandsGzipAndZstd(t *testing.T) {
	gz, err := BuildDirTarCommands(DirConfig{SourcePath: "/var/lib/eyvescloud/ct-1", OutputPath: "/tmp/ct-1.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	if gz[0][0] != "tar" || !strings.Contains(strings.Join(gz[0], " "), "-czf") {
		t.Fatalf("gzip tar = %v", gz[0])
	}

	zs, err := BuildDirTarCommands(DirConfig{SourcePath: "/var/lib/eyvescloud/ct-1", OutputPath: "/tmp/ct-1.tar.zst", UseZstd: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(zs[0], " "), "--zstd") {
		t.Fatalf("zstd tar = %v", zs[0])
	}
}

func TestBuildDirTarCommandsRejectsRelativeAndRoot(t *testing.T) {
	for _, bad := range []string{".", "/", "../x"} {
		if _, err := BuildDirTarCommands(DirConfig{SourcePath: bad, OutputPath: "/tmp/x"}); err == nil {
			t.Fatalf("invalid source %q must error", bad)
		}
	}
}

func TestBuildDirExtractRequiresFields(t *testing.T) {
	if _, err := BuildDirExtractCommands("", "/tmp/x"); err == nil {
		t.Fatal("empty tarball must error")
	}
}

func TestVerifyManifestDeterministic(t *testing.T) {
	m := Manifest{
		Backend: BackendRBD, Instance: "ct-1", CreatedAt: time.Now(),
		Files: []FileMeta{
			{Path: "blob", SHA256: "abc", Size: 100},
			{Path: "blob2", SHA256: "def", Size: 200},
		},
	}
	a := VerifyManifest(m)
	b := VerifyManifest(m)
	if a == "" || a != b {
		t.Fatalf("verify = %q, %q", a, b)
	}
	// 不同内容 → 不同 hash。
	m.Files[0].SHA256 = "xyz"
	if VerifyManifest(m) == a {
		t.Fatal("hash must change with content")
	}
}

func TestRBDImportCommandsRoundtrip(t *testing.T) {
	cmds, err := RBDImportCommands("rbd", "vol-x", "/tmp/incr.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmds[0], " "), "import-diff /tmp/incr.bin rbd/vol-x") {
		t.Fatalf("import-diff = %v", cmds[0])
	}
}