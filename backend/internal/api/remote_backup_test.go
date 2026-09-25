package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

func TestValidateRemoteDir(t *testing.T) {
	valid := []string{"/data/backups", "/srv/eyves-cloud-1", "/a/b/c"}
	for _, dir := range valid {
		if err := validateRemoteDir(dir); err != nil {
			t.Errorf("validateRemoteDir(%q) = %v, want nil", dir, err)
		}
	}
	invalid := []string{
		"",                  // 空
		"data/backups",      // 非绝对路径
		"/",                 // 根目录
		"/data/../etc",      // 目录穿越
		"/data/./backups",   // 含 .
		"/data/bac kups",    // 含空格
		"/data/back;rm -rf", // 命令注入
		"/data/$(id)",       // 命令替换
	}
	for _, dir := range invalid {
		if err := validateRemoteDir(dir); err == nil {
			t.Errorf("validateRemoteDir(%q) = nil, want error", dir)
		}
	}
}

func TestRemoteBackupFilePath(t *testing.T) {
	s := config.RemoteBackupSettings{RemoteDir: "/data/eyvescloud-backups/"}
	b := config.InstanceBackup{ID: "bak-7", ContainerID: 7, Path: "/var/lib/eyves/instance-backups/7/bak-7.tar"}

	got, err := remoteBackupFilePath(s, b)
	if err != nil {
		t.Fatalf("remoteBackupFilePath() error = %v", err)
	}
	want := "/data/eyvescloud-backups/7/bak-7.tar"
	if got != want {
		t.Fatalf("remoteBackupFilePath() = %q, want %q", got, want)
	}

	// 文件名含不安全字符必须被拒绝。
	unsafe := b
	unsafe.Path = "/tmp/bak 7;rm -rf.tar"
	if _, err := remoteBackupFilePath(s, unsafe); err == nil {
		t.Fatalf("expected error for unsafe backup file name")
	}

	// 非法远端目录必须被拒绝。
	if _, err := remoteBackupFilePath(config.RemoteBackupSettings{RemoteDir: "relative"}, b); err == nil {
		t.Fatalf("expected error for relative remote_dir")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/data/x"); got != "'/data/x'" {
		t.Fatalf("shellQuote plain = %q", got)
	}
	// 单引号必须被安全转义，不能提前闭合引号。
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("shellQuote quoted = %q", got)
	}
}

func TestRemoteBackupKeyPathDefault(t *testing.T) {
	orig := config.AppConfig
	defer func() { config.AppConfig = orig }()
	config.AppConfig = &config.EyvescloudConfig{DataDir: "/var/lib/eyvescloud"}

	if got := remoteBackupKeyPath(config.RemoteBackupSettings{}); got != filepath.Join("/var/lib/eyvescloud", "ssh", "backup_ed25519") {
		t.Fatalf("default key path = %q", got)
	}
	if got := remoteBackupKeyPath(config.RemoteBackupSettings{KeyPath: " /custom/key "}); got != "/custom/key" {
		t.Fatalf("custom key path = %q", got)
	}
}

func TestSyncBackupToRemoteNoopWhenDisabled(t *testing.T) {
	orig := config.AppConfig
	defer func() { config.AppConfig = orig }()
	config.AppConfig = &config.EyvescloudConfig{
		DataDir: t.TempDir(),
		InstanceBackups: []config.InstanceBackup{
			{ID: "bak-1", ContainerID: 1, Path: "/nowhere/bak-1.tar"},
		},
	}
	if err := syncBackupToRemote("bak-1"); err != nil {
		t.Fatalf("syncBackupToRemote should be a no-op when disabled, got %v", err)
	}
}

func TestSyncBackupToRemoteRecordsFailureOnInvalidRemoteDir(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "instance-backups", "3", "bak-3.tar")
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	orig := config.AppConfig
	defer func() { config.AppConfig = orig }()
	config.AppConfig = &config.EyvescloudConfig{
		DataDir: dir,
		RemoteBackupSettings: config.RemoteBackupSettings{
			Enabled:   true,
			Host:      "backup.example.com",
			User:      "backup",
			RemoteDir: "relative-dir", // 非法：不是绝对路径
		},
		InstanceBackups: []config.InstanceBackup{
			{ID: "bak-3", ContainerID: 3, Path: archive},
		},
	}

	err := syncBackupToRemote("bak-3")
	if err == nil {
		t.Fatalf("expected error for invalid remote_dir")
	}
	b := config.FindInstanceBackup("bak-3")
	if b == nil {
		t.Fatal("backup record missing")
	}
	if b.RemoteUploaded {
		t.Fatalf("RemoteUploaded should be false on failure")
	}
	if !strings.Contains(b.RemoteError, "remote_dir") {
		t.Fatalf("RemoteError should mention remote_dir, got %q", b.RemoteError)
	}
}
