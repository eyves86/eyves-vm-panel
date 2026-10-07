package config

// store_postgres_test.go —— P1：Postgres 后端的往返校验（env 门控）。
//
// 只有设置了 EYVESCLOUD_PG_TEST_DSN（指向一个一次性的隔离 Postgres 库）才运行，
// 未设置直接 skip，故默认的 SQLite 测试流不受影响。测试库会被整体重建，
// 绝不要指向任何真实数据库。
//
// 覆盖的接缝（都是 rebind 与方言差异最容易出错的地方）：
//   - `?`→`$n` 重绑定（全部语句）；
//   - 保留字 "user"（audit_logs / tasks / task_history 的写入与过滤）；
//   - 自增主键（audit_logs / login_logs / task_logs 依赖 IDENTITY 与 id 排序）；
//   - ON CONFLICT upsert（task_history 覆盖写，替代 SQLite 的 INSERT OR REPLACE）；
//   - 加密落库字段（SSH 口令 / 访问码 / 节点 token）跨重启解密还原。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func pgTestDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EYVESCLOUD_PG_TEST_DSN"))
	if dsn == "" {
		t.Skip("未设置 EYVESCLOUD_PG_TEST_DSN，跳过 Postgres 往返校验")
	}
	return dsn
}

// wipePostgresTestSchema 清空测试库（一次性专用库，整体重建 public schema 即可）。
func wipePostgresTestSchema(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试 Postgres 失败: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{"DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("清理测试 schema 失败 (%s): %v", stmt, err)
		}
	}
}

func withPostgresConfigTest(t *testing.T) {
	t.Helper()
	dsn := pgTestDSN(t)
	wipePostgresTestSchema(t, dsn)
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(t.TempDir(), "config.json"))
	// 必须重定向数据目录：InitConfig 的「首次安装」分支会往 DataDir 写
	// initial-admin-credentials.txt，否则会覆盖真实 ~/.eyvescloud 下的同名文件。
	t.Setenv("EYVESCLOUD_DATA_DIR", t.TempDir())
	t.Setenv(pgDSNEnv, dsn)
	if _, err := InitConfig(); err != nil {
		t.Fatalf("InitConfig(Postgres) 失败: %v", err)
	}
}

func TestPostgresConfigRoundTrip(t *testing.T) {
	withPostgresConfigTest(t)

	if err := MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.Containers = []Container{{
			ID: 1, UUID: "uuid-pg-1", Name: "pg-box", Status: "running",
			SSHPassword: "s3cr3t-pw", AccessCode: "code-pg", AccessCodePassword: "code-pw",
		}}
		cfg.SubUsers = []SubUser{{ID: "su-pg", Username: "alice", PassHash: "h", Role: "operator"}}
		cfg.ApiKeys = []ApiKeyConfig{{ID: "ak-pg", Name: "k", KeyHash: "kh"}}
		cfg.Snapshots = []Snapshot{{ID: "snap-pg", ContainerID: 1, ContainerName: "pg-box", CreatedBy: "alice"}}
		cfg.Tasks = []SavedTask{{ID: "task-pg", Type: "create", ContainerID: 1, ContainerName: "pg-box", Status: "running", User: "alice"}}
		cfg.Nodes = []Node{{ID: "node-pg", Name: "n1", Address: "10.0.0.9", Token: "tok-pg"}}
		cfg.EnabledImages = []string{"img-a", "img-b"}
	}); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := SaveConfig(); err != nil {
		t.Fatalf("SaveConfig(Postgres) 失败: %v", err)
	}
	AddAuditLog("pg.action", "target-pg", "detail", "user-pg")
	AddLoginLog("alice", "127.0.0.1", "pg-ua", true)
	if err := UpsertTaskHistory(TaskHistoryEntry{ID: "th-pg", Type: "create", Status: "running", User: "user-pg"}); err != nil {
		t.Fatalf("UpsertTaskHistory(Postgres) 失败: %v", err)
	}
	if err := AppendTaskLog("th-pg", "INFO", "hello"); err != nil {
		t.Fatalf("AppendTaskLog(Postgres) 失败: %v", err)
	}

	cfg := reopenConfig(t)

	if len(cfg.Containers) != 1 {
		t.Fatalf("容器未往返: %+v", cfg.Containers)
	}
	c := cfg.Containers[0]
	if c.Name != "pg-box" || c.SSHPassword != "s3cr3t-pw" || c.AccessCode != "code-pg" || c.AccessCodePassword != "code-pw" {
		t.Fatalf("容器字段跨重启不一致（含加密往返）: %+v", c)
	}
	if len(cfg.SubUsers) != 1 || cfg.SubUsers[0].Username != "alice" {
		t.Fatalf("子用户未往返: %+v", cfg.SubUsers)
	}
	if len(cfg.ApiKeys) != 1 || cfg.ApiKeys[0].ID != "ak-pg" {
		t.Fatalf("API Key 未往返: %+v", cfg.ApiKeys)
	}
	if len(cfg.Snapshots) != 1 || cfg.Snapshots[0].ID != "snap-pg" {
		t.Fatalf("快照未往返: %+v", cfg.Snapshots)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].User != "alice" {
		t.Fatalf(`任务未往返（tasks."user" 列）: %+v`, cfg.Tasks)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Token != "tok-pg" {
		t.Fatalf("节点未往返（token 加密往返）: %+v", cfg.Nodes)
	}
	if len(cfg.EnabledImages) != 2 {
		t.Fatalf("启用镜像未往返: %+v", cfg.EnabledImages)
	}

	var sawAudit, sawLogin bool
	for _, l := range cfg.AuditLogs {
		if l.Action == "pg.action" {
			sawAudit = true
			if l.User != "user-pg" {
				t.Errorf(`audit_logs."user" 列往返错误: %q`, l.User)
			}
		}
	}
	for _, l := range cfg.LoginLogs {
		if l.Username == "alice" {
			sawLogin = true
		}
	}
	if !sawAudit || !sawLogin {
		t.Fatalf("审计/登录日志未往返: audit=%v login=%v", sawAudit, sawLogin)
	}

	// task_history 的 ON CONFLICT 覆盖写 + 保留字过滤。
	got, ok, err := GetTaskHistory("th-pg")
	if err != nil || !ok {
		t.Fatalf("GetTaskHistory 失败: ok=%v err=%v", ok, err)
	}
	if got.User != "user-pg" {
		t.Errorf(`task_history."user" 往返错误: %q`, got.User)
	}
	if err := UpsertTaskHistory(TaskHistoryEntry{ID: "th-pg", Type: "create", Status: "completed", User: "user-pg"}); err != nil {
		t.Fatalf("UpsertTaskHistory 覆盖写失败（ON CONFLICT 路径）: %v", err)
	}
	items, total, err := ListTaskHistory(nil, "", "user-pg", 10, 0)
	if err != nil {
		t.Fatalf("ListTaskHistory 过滤失败: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Status != "completed" {
		t.Fatalf("task_history 过滤/覆盖写结果异常: total=%d items=%+v", total, items)
	}
	logs, err := ListTaskLogs("th-pg")
	if err != nil || len(logs) != 1 || logs[0].Message != "hello" {
		t.Fatalf("task_logs 未往返: %+v err=%v", logs, err)
	}
}

// TestPostgresRebindPlaceholders 纯函数校验 ?→$n 的重绑定（含引用段跳过）。
func TestPostgresRebindPlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"SELECT * FROM t WHERE a = ? AND b = ?", "SELECT * FROM t WHERE a = $1 AND b = $2"},
		{"SELECT * FROM t WHERE s = 'a?b' AND n = ?", "SELECT * FROM t WHERE s = 'a?b' AND n = $1"},
		{`INSERT INTO t(a, "user") VALUES (?, ?)`, `INSERT INTO t(a, "user") VALUES ($1, $2)`},
		{"SELECT * FROM t WHERE s = 'it''s ?' AND n = ?", "SELECT * FROM t WHERE s = 'it''s ?' AND n = $1"},
	}
	for _, c := range cases {
		if got := rebind(c.in); got != c.want {
			t.Errorf("rebind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
