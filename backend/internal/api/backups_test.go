package api

import (
	"os"
	"path/filepath"
	"testing"

	"eyvescloud/internal/config"
)

func TestBackupsToPrune(t *testing.T) {
	mk := func(i int) config.InstanceBackup {
		return config.InstanceBackup{ID: string(rune('a' + i)), CreatedAt: "2026-01-01 00:00:00"}
	}
	backups := []config.InstanceBackup{mk(0), mk(1), mk(2), mk(3)} // newest first

	if got := backupsToPrune(backups, 0); got != nil {
		t.Fatalf("keep=0 should prune nothing, got %v", got)
	}
	if got := backupsToPrune(backups, 4); got != nil {
		t.Fatalf("keep>=len should prune nothing, got %v", got)
	}
	got := backupsToPrune(backups, 2)
	if len(got) != 2 {
		t.Fatalf("keep=2 should prune 2, got %d: %v", len(got), got)
	}
	found := map[string]bool{}
	for _, b := range got {
		found[b.ID] = true
	}
	if !found["c"] || !found["d"] {
		t.Fatalf("keep=2 should keep a,b and prune c,d, got %v", got)
	}
}

func TestInstanceBackupConfigAccessors(t *testing.T) {
	if err := os.MkdirAll(filepath.Join(t.TempDir(), "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{
		DataDir: t.TempDir(),
		InstanceBackups: []config.InstanceBackup{
			{ID: "old", ContainerID: 1, CreatedAt: "2026-01-01 00:00:00"},
			{ID: "new", ContainerID: 1, CreatedAt: "2026-01-02 00:00:00"},
			{ID: "other", ContainerID: 2, CreatedAt: "2026-01-03 00:00:00"},
		},
	}

	// ContainerInstanceBackups 应按时间最新在前排序。
	got := config.ContainerInstanceBackups(1)
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("unexpected order: %v", got)
	}

	if b := config.FindInstanceBackup("new"); b == nil || b.ContainerID != 1 {
		t.Fatalf("FindInstanceBackup(new) = %v", b)
	}
	if b := config.FindInstanceBackup("missing"); b != nil {
		t.Fatalf("FindInstanceBackup(missing) should be nil")
	}

	if !config.RemoveInstanceBackup("new") {
		t.Fatal("RemoveInstanceBackup(new) should be true")
	}
	if config.FindInstanceBackup("new") != nil {
		t.Fatal("backup new should be gone")
	}
	if config.FindInstanceBackup("old") == nil {
		t.Fatal("backup old should remain")
	}
}