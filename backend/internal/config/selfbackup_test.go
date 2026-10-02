package config

// selfbackup_test.go —— 配置库自备份 / 轮转 / WAL 检查点回归测试。
//
// 背景：config.db 是平台唯一真相来源，生产上该目录的 backups/ 长期为空。
// 这里锁定"能生成可打开的一致性快照、且会轮转清理"的核心契约。

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// withBackupTestConfig 准备隔离的数据目录与配置库。
func withBackupTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previousDataDir := os.Getenv("EYVESCLOUD_DATA_DIR")
	previousCfgPath := getConfigPath()
	previousApp := GetTestConfig()

	SetConfigPath(filepath.Join(dir, "config.json"))
	os.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		CloseConfigDB()
		RestoreTestConfig(previousApp)
		SetConfigPath(previousCfgPath)
		if previousDataDir == "" {
			os.Unsetenv("EYVESCLOUD_DATA_DIR")
		} else {
			os.Setenv("EYVESCLOUD_DATA_DIR", previousDataDir)
		}
	})

	if _, err := InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	// 写一点可辨识的数据，确保快照里确实有内容。
	if err := MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.BrandName = "backup-test-brand"
	}); err != nil {
		t.Fatalf("写入测试数据失败: %v", err)
	}
	return dir
}

// TestBackupConfigDatabaseCreatesReadableSnapshot 快照必须是能独立打开、
// 且包含当前数据的完整数据库文件。
func TestBackupConfigDatabaseCreatesReadableSnapshot(t *testing.T) {
	withBackupTestConfig(t)

	path, err := BackupConfigDatabase()
	if err != nil {
		t.Fatalf("生成快照失败: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("快照文件不存在: %v", err)
	}

	// 快照能被独立打开，并且读到写入的品牌名。
	snap, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开快照失败: %v", err)
	}
	defer snap.Close()

	var brand string
	if err := snap.QueryRow("SELECT value FROM app_meta WHERE key='brand_name'").Scan(&brand); err != nil {
		t.Fatalf("从快照读取 brand_name 失败: %v", err)
	}
	if brand != "backup-test-brand" {
		t.Fatalf("快照内容不正确：brand_name=%q", brand)
	}
}

// TestPruneConfigBackupsKeepsNewest 轮转只保留最近 N 份。
func TestPruneConfigBackupsKeepsNewest(t *testing.T) {
	withBackupTestConfig(t)
	dir := ConfigBackupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("创建备份目录失败: %v", err)
	}

	// 造 6 个时间戳递增的假快照。
	names := []string{
		"config-20260101-000000.db",
		"config-20260102-000000.db",
		"config-20260103-000000.db",
		"config-20260104-000000.db",
		"config-20260105-000000.db",
		"config-20260106-000000.db",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("造快照失败: %v", err)
		}
	}
	// 干扰项：不该被计入快照列表。
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("造干扰文件失败: %v", err)
	}

	removed, err := PruneConfigBackups(2)
	if err != nil {
		t.Fatalf("轮转失败: %v", err)
	}
	if removed != 4 {
		t.Fatalf("应删除 4 份，实际 %d", removed)
	}

	left, err := ListConfigBackups()
	if err != nil {
		t.Fatalf("列出快照失败: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("应剩余 2 份，实际 %d", len(left))
	}
	// 保留的必须是最新的两份（列表按倒序）。
	if filepath.Base(left[0]) != names[5] || filepath.Base(left[1]) != names[4] {
		t.Fatalf("保留的应当是最新两份，实际 %v", left)
	}
	// 干扰文件不受影响。
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("轮转不应删除非快照文件")
	}
}

// TestRunConfigBackupOnceAndCheckpoint 一次快照 + 轮转 + WAL 检查点整体可用。
func TestRunConfigBackupOnceAndCheckpoint(t *testing.T) {
	withBackupTestConfig(t)

	path, err := RunConfigBackupOnce(3)
	if err != nil {
		t.Fatalf("执行一次快照失败: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("快照文件不存在: %v", err)
	}

	if err := CheckpointConfigWAL(); err != nil {
		t.Fatalf("WAL 检查点失败: %v", err)
	}
	// 检查点后 -wal 应被截断（允许文件仍存在但为 0 字节）。
	wal := filepath.Join(getDataDir(), "config.db-wal")
	if info, err := os.Stat(wal); err == nil && info.Size() > 0 {
		t.Fatalf("检查点后 WAL 应被截断，实际 %d 字节", info.Size())
	}
}
