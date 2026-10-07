package config

// store_logs_test.go —— #95：有界日志表退出「每次保存重写」的回归。
//
// 背景：audit_logs ≤500 / login_logs ≤200 原先是每次保存无条件 DELETE+INSERT
// （~700 行）。常规写入其实已由 appendAuditLogRow / appendLoginLogRow 增量落库，
// 于是这 ~700 行是纯固定写放大。改为 logsDirty 门控后：
//   - 干净状态（内存==库）→ 保存不写日志表；
//   - 增量追加 → 追加自身写 1 行，随后的保存再写 0 行；
//   - 内存被改动却未落库（裁剪 / 追加失败自愈）→ MarkLogsDirty 后保存重写并回一致。

import (
	"path/filepath"
	"testing"
)

func initLogsTest(t *testing.T) {
	t.Helper()
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(t.TempDir(), "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
}

// TestCleanSaveDoesNotRewriteBoundedLogs 干净状态下保存不得触碰日志表。
func TestCleanSaveDoesNotRewriteBoundedLogs(t *testing.T) {
	initLogsTest(t)
	installWriteProbe(t, "audit_logs", "login_logs")

	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "audit_logs", "DELETE", 0)
	assertProbe(t, "audit_logs", "INSERT", 0)
	assertProbe(t, "login_logs", "DELETE", 0)
	assertProbe(t, "login_logs", "INSERT", 0)
}

// TestIncrementalAppendDoesNotTriggerLogRewrite 增量追加只写它自己那一行，
// 随后的保存不再重写整表（这正是 #95 要消灭的 ~700 行固定写）。
func TestIncrementalAppendDoesNotTriggerLogRewrite(t *testing.T) {
	initLogsTest(t)
	installWriteProbe(t, "audit_logs", "login_logs")

	AddAuditLog("test.action", "t", "d", "u")
	AddLoginLog("u", "127.0.0.1", "probe", true)

	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	// 追加各写 1 行 INSERT，删除各 0；保存不再重写。
	assertProbe(t, "audit_logs", "INSERT", 1)
	assertProbe(t, "audit_logs", "DELETE", 0)
	assertProbe(t, "login_logs", "INSERT", 1)
	assertProbe(t, "login_logs", "DELETE", 0)
}

// TestMarkLogsDirtyRewritesAndReconciles 内存删行 + 置脏 → 保存把库改回一致。
func TestMarkLogsDirtyRewritesAndReconciles(t *testing.T) {
	initLogsTest(t)
	AddAuditLog("a.keep", "t", "d", "u")
	AddAuditLog("a.drop", "t", "d", "u")
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "audit_logs")

	// 模拟保留期裁剪：内存里删掉第一条，但没走增量追加路径。
	AppConfigMu.Lock()
	AppConfig.AuditLogs = AppConfig.AuditLogs[1:]
	AppConfigMu.Unlock()
	MarkLogsDirty()

	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	// AFTER DELETE 触发器按「行」计数：重写先清空原有 2 行（2 次），再插入保留的 1 行。
	assertProbe(t, "audit_logs", "DELETE", 2)
	assertProbe(t, "audit_logs", "INSERT", 1)

	// 关库重开：磁盘上只应剩被保留的那条（防止「裁剪只在内存、重启即恢复」）。
	CloseConfigDB()
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.AuditLogs); n != 1 {
		t.Fatalf("重载后审计日志条数 = %d, want 1", n)
	}
	if cfg.AuditLogs[0].Action != "a.drop" {
		t.Fatalf("保留的应是 a.drop，实际 %q", cfg.AuditLogs[0].Action)
	}
}

// TestFailedAppendMarksLogsDirty 追加落库失败必须置脏，使下一次保存自愈补齐。
func TestFailedAppendMarksLogsDirty(t *testing.T) {
	initLogsTest(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_audit_insert BEFORE INSERT ON audit_logs
		BEGIN SELECT RAISE(ABORT,'inject'); END`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS fail_audit_insert`) }()

	AddAuditLog("a.fail", "t", "d", "u")

	dbMu.Lock()
	dirty := logsDirty
	dbMu.Unlock()
	if !dirty {
		t.Fatal("追加落库失败后 logsDirty 应为 true（否则该条日志永不落库）")
	}

	// 去掉注入后，下一次保存应把有界表重写回一致：内存里的那一条最终出现在库中。
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS fail_audit_insert`); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	dbMu.Lock()
	dirty = logsDirty
	dbMu.Unlock()
	if dirty {
		t.Fatal("重写成功后 logsDirty 应被清除")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'a.fail'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("自愈后库中 a.fail 条数 = %d, want 1", n)
	}
}

// TestLogsDirtyNotSetByRoutineSave 守护契约：普通保存绝不置脏（否则退化为每次重写）。
func TestLogsDirtyNotSetByRoutineSave(t *testing.T) {
	initLogsTest(t)
	AddAuditLog("a", "t", "d", "u")
	for i := 0; i < 3; i++ {
		if err := SaveConfig(); err != nil {
			t.Fatal(err)
		}
	}
	dbMu.Lock()
	dirty := logsDirty
	dbMu.Unlock()
	if dirty {
		t.Fatal("常规保存后 logsDirty 不应为 true")
	}
}
