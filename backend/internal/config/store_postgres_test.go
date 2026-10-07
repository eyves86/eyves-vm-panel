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

// columnsByTable 从 CREATE TABLE 语句里抽出「表名 → 列名集合」。只处理本项目 DDL 的
// 写法：一列一行，约束行以 PRIMARY/UNIQUE/FOREIGN/CONSTRAINT/CHECK 开头。
func columnsByTable(stmts []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range stmts {
		body := strings.TrimSpace(s)
		if !strings.HasPrefix(strings.ToUpper(body), "CREATE TABLE") {
			continue
		}
		open, closeIdx := strings.Index(body, "("), strings.LastIndex(body, ")")
		if open < 0 || closeIdx <= open {
			continue
		}
		fields := strings.Fields(body[:open])
		table := strings.Trim(fields[len(fields)-1], `"`)
		cols := map[string]bool{}
		for _, line := range strings.Split(body[open+1:closeIdx], "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
			if line == "" {
				continue
			}
			name := strings.Trim(strings.Fields(line)[0], `"`)
			switch strings.ToUpper(name) {
			case "PRIMARY", "UNIQUE", "FOREIGN", "CONSTRAINT", "CHECK":
				continue
			}
			cols[name] = true
		}
		out[table] = cols
	}
	return out
}

// migrationsByTable 把共享的后加列清单整理成「表 → 列集合」。
func migrationsByTable() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, m := range schemaColumnMigrations() {
		if out[m.table] == nil {
			out[m.table] = map[string]bool{}
		}
		out[m.table][m.name] = true
	}
	return out
}

// TestSQLiteAndPostgresSchemaParity 断言两端「最终列集合」逐表一致，即
// sqliteBase ∪ 迁移 == postgresBase ∪ 迁移（PG 基础建表内联了历史列，SQLite 靠迁移补，
// 见 schemaColumnMigrations 的注释）。任何一处加了列而另一处既不在基础也不在迁移清单，
// 都会让该后端缺列 —— PG 侧尤其致命：CREATE TABLE IF NOT EXISTS 对已存在的表是空操作，
// 缺的列只能靠 ensureSchemaMigrations 补。纯静态、不需要 PG，默认测试流即跑。
func TestSQLiteAndPostgresSchemaParity(t *testing.T) {
	mig := migrationsByTable()
	sqlite := columnsByTable(sqliteSchemaStmts())
	pg := columnsByTable(postgresSchemaStmts())
	// 解析自检：DDL 写法若被改坏导致抽不出列，测试会静默通过 —— 先钉死规模。
	if len(sqlite) < 20 || len(pg) < 20 || len(mig) == 0 {
		t.Fatalf("DDL 解析出的表/迁移过少（sqlite=%d pg=%d mig=%d），解析可能失效", len(sqlite), len(pg), len(mig))
	}
	if len(sqlite["containers"]) < 60 {
		t.Fatalf("containers 解析出的列过少（%d），解析可能失效", len(sqlite["containers"]))
	}
	// 某侧「基础 ∪ 迁移」有的列，另一侧也必须在「基础 ∪ 迁移」里有。
	has := func(base map[string]map[string]bool, table, col string) bool {
		return base[table][col] || mig[table][col]
	}
	all := map[string]bool{}
	for _, tset := range []map[string]map[string]bool{sqlite, pg, mig} {
		for table := range tset {
			all[table] = true
		}
	}
	for table := range all {
		for col := range sqlite[table] {
			if !has(pg, table, col) {
				t.Errorf("表 %s 列 %s：SQLite 侧有（基础∪迁移），Postgres 侧没有", table, col)
			}
		}
		for col := range pg[table] {
			if !has(sqlite, table, col) {
				t.Errorf("表 %s 列 %s：Postgres 侧有（基础∪迁移），SQLite 侧没有", table, col)
			}
		}
	}
}

// TestPostgresColumnMigrationOnUpgrade 模拟「旧版本已建好的 PG 库」升级到新版本：
// 删掉两个后加的迁移列（一个 INTEGER 源、一个 REAL 源），重开配置库（= 新版启动），
// 断言列被自动补回且类型与建表一致（bigint / double precision，而非 SQLite 的
// INTEGER/REAL，避免字节计数类列落到 int4 溢出）。
func TestPostgresColumnMigrationOnUpgrade(t *testing.T) {
	withPostgresConfigTest(t)

	for _, col := range []string{"image_limit_configured", "data_disk_gb"} {
		if _, err := db.Exec("ALTER TABLE containers DROP COLUMN " + col); err != nil {
			t.Fatalf("模拟旧库 DROP COLUMN %s 失败: %v", col, err)
		}
	}
	reopenConfig(t)

	for col, want := range map[string]string{
		"image_limit_configured": "bigint",
		"data_disk_gb":           "double precision",
	} {
		var got string
		if err := db.QueryRow(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'containers' AND column_name = ?`, col).Scan(&got); err != nil {
			t.Fatalf("查询列 containers.%s 失败: %v", col, err)
		}
		if got != want {
			t.Errorf("列 containers.%s 类型 = %q, want %q", col, got, want)
		}
	}
}
