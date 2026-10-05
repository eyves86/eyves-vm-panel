package config

// log_append_test.go —— 审计/登录日志增量追加的不变量。
//
// 背景（写放大）：AddAuditLog/AddLoginLog 原先每次追加一行都会 SaveConfig()，
// 触发整库 17 张表 DELETE+INSERT。改成只 INSERT 一行 + 按 id 裁剪后，必须保证
// SQLite 表与内存切片始终一致、上限口径一致——否则日志会丢或错位。

import "testing"

func TestAppendAuditLogRowKeepsMemoryAndDBInSync(t *testing.T) {
	withSaveLogTestConfig(t)

	for i := 0; i < auditLogKeep+7; i++ {
		AddAuditLog("test.action", "target", "detail", "tester")
	}

	AppConfigMu.RLock()
	mem := len(AppConfig.AuditLogs)
	AppConfigMu.RUnlock()
	if mem != auditLogKeep {
		t.Fatalf("内存审计日志应为上限 %d 条，实际 %d", auditLogKeep, mem)
	}

	rows, err := loadAuditLogs()
	if err != nil {
		t.Fatalf("读取审计日志失败: %v", err)
	}
	if len(rows) != mem {
		t.Fatalf("DB 审计日志 %d 条与内存 %d 条不一致（增量追加与内存口径不一致）", len(rows), mem)
	}
	if len(rows) == 0 || rows[len(rows)-1].Action != "test.action" {
		t.Fatalf("最新审计日志未落库: %+v", rows)
	}
}

func TestAppendLoginLogRowKeepsMemoryAndDBInSync(t *testing.T) {
	withSaveLogTestConfig(t)

	for i := 0; i < loginLogKeep+5; i++ {
		AddLoginLog("u", "1.2.3.4", "ua", true)
	}

	AppConfigMu.RLock()
	mem := len(AppConfig.LoginLogs)
	AppConfigMu.RUnlock()
	if mem != loginLogKeep {
		t.Fatalf("内存登录日志应为上限 %d 条，实际 %d", loginLogKeep, mem)
	}

	rows, err := loadLoginLogs()
	if err != nil {
		t.Fatalf("读取登录日志失败: %v", err)
	}
	if len(rows) != mem {
		t.Fatalf("DB 登录日志 %d 条与内存 %d 条不一致", len(rows), mem)
	}
}
