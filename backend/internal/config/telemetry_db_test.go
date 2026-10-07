package config

// telemetry_db_test.go —— 遥测分库（P1-c）回归测试。
//
// 背景：改版前 container_metrics / container_metrics_hourly 与整个配置快照同处
// config.db，并**共用同一个 `*sql.DB` 与同一把 dbMu**（池上限 1）。后果是心跳/
// 指标写入与配置保存互相排队：任一方慢都直接拖住另一方，一次 rollup 就能把面板的
// 配置读写整体卡住。
//
// 本组用例锁定四件事：
//  1. 遥测确实落在独立的 telemetry.db 与独立的 *sql.DB 上；
//  2. pragma 走 DSN 生效（busy_timeout 是连接级 pragma，只有 DSN 能覆盖池内每条连接）；
//  3. 指标写入/读取跨重启保留；
//  4. 旧版 config.db 里遗留的遥测行会被完整搬迁，且旧表被清理。

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestTelemetryUsesSeparateDatabaseAndDSNPragmas 遥测库与配置库必须物理分离，
// 且 pragma 通过 DSN（而不是建池时的一次 Exec）生效。
func TestTelemetryUsesSeparateDatabaseAndDSNPragmas(t *testing.T) {
	withSecGroupPersistTest(t)

	if db == nil || telemetryDB == nil {
		t.Fatalf("配置库或遥测库未打开：db=%v telemetryDB=%v", db, telemetryDB)
	}
	if db == telemetryDB {
		t.Fatal("遥测与配置必须使用不同的 *sql.DB（当前仍是同一个连接池）")
	}

	if _, err := os.Stat(getTelemetryDBPath()); err != nil {
		t.Fatalf("遥测库文件不存在: %v", err)
	}
	if filepath.Dir(getTelemetryDBPath()) != filepath.Dir(getDBPath()) {
		t.Fatalf("遥测库应与配置库同目录: %s vs %s", getTelemetryDBPath(), getDBPath())
	}

	// 库文件含容器标识等指标数据，必须锁到 0600（首次落盘就要正确，不能等下次启动）。
	// 0600 语义在 Windows 上不成立，仅在有意义的平台上断言。
	if runtime.GOOS != "windows" {
		for _, p := range []string{getTelemetryDBPath(), getDBPath()} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("stat %s 失败: %v", p, err)
			}
			if perm := info.Mode().Perm(); perm != 0600 {
				t.Errorf("%s 权限应为 0600，实际 %04o", filepath.Base(p), perm)
			}
		}
	}

	// 全新安装的配置库里不应再有遥测表。
	if tableExists(db, "container_metrics") {
		t.Error("container_metrics 不应再建在 config.db 里")
	}

	telemetryMu.RLock()
	var mode string
	modeErr := telemetryDB.QueryRow("PRAGMA journal_mode").Scan(&mode)
	var busy int
	busyErr := telemetryDB.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	telemetryMu.RUnlock()

	if modeErr != nil {
		t.Fatalf("读取遥测库 journal_mode 失败: %v", modeErr)
	}
	if strings.ToLower(mode) != "wal" {
		t.Errorf("遥测库 journal_mode 应为 wal，实际 %q（DSN pragma 未生效）", mode)
	}
	if busyErr != nil {
		t.Fatalf("读取遥测库 busy_timeout 失败: %v", busyErr)
	}
	if busy != 5000 {
		t.Errorf("遥测库 busy_timeout 应为 5000，实际 %d（DSN pragma 未生效）", busy)
	}

	// 配置库同样应通过 DSN 拿到连接级 pragma（旧实现是建池时逐条 Exec）。
	dbMu.Lock()
	var cfgBusy, cfgFK int
	cfgBusyErr := db.QueryRow("PRAGMA busy_timeout").Scan(&cfgBusy)
	cfgFKErr := db.QueryRow("PRAGMA foreign_keys").Scan(&cfgFK)
	dbMu.Unlock()
	if cfgBusyErr != nil || cfgBusy != 5000 {
		t.Errorf("配置库 busy_timeout 应为 5000，实际 %d (err=%v)", cfgBusy, cfgBusyErr)
	}
	if cfgFKErr != nil || cfgFK != 1 {
		t.Errorf("配置库 foreign_keys 应为 1，实际 %d (err=%v)", cfgFK, cfgFKErr)
	}
}

// TestTelemetrySamplesSurviveRestart 指标写入落在 telemetry.db，重启后仍在。
func TestTelemetrySamplesSurviveRestart(t *testing.T) {
	withSecGroupPersistTest(t)

	samples := []MetricSample{
		{TS: 1000, CPU: 1.5, Memory: 2.5, NetworkRx: 3, NetworkTx: 4, DiskRead: 5, DiskWrite: 6},
		{TS: 2000, CPU: 7.5, Memory: 8.5, NetworkRx: 9, NetworkTx: 10, DiskRead: 11, DiskWrite: 12},
	}
	if err := SaveMetricSamples("c-restart", samples); err != nil {
		t.Fatalf("写入指标失败: %v", err)
	}

	reopenConfig(t)

	got, err := LoadMetricSamples("c-restart", 0)
	if err != nil {
		t.Fatalf("重启后读取指标失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("重启后应有 2 条指标，实际 %d 条", len(got))
	}
	if got[0].CPU != 1.5 || got[1].TS != 2000 {
		t.Fatalf("指标内容不一致: %+v", got)
	}
}

// TestLegacyMetricsMigratedToTelemetryDB 旧版遗留在 config.db 的遥测行必须搬迁
// 到 telemetry.db，且旧表被清理（不能半途丢数据）。
func TestLegacyMetricsMigratedToTelemetryDB(t *testing.T) {
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

	// 造一个「旧版」配置库：只有遥测表与数据，模拟升级前的现场。
	legacy, err := sql.Open("sqlite", getDBPath())
	if err != nil {
		t.Fatalf("创建旧版配置库失败: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE container_metrics (
			container_key TEXT NOT NULL, ts INTEGER NOT NULL,
			cpu REAL NOT NULL DEFAULT 0, memory REAL NOT NULL DEFAULT 0,
			network_rx REAL NOT NULL DEFAULT 0, network_tx REAL NOT NULL DEFAULT 0,
			disk_read REAL NOT NULL DEFAULT 0, disk_write REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE container_metrics_hourly (
			container_key TEXT NOT NULL, hour INTEGER NOT NULL,
			count INTEGER NOT NULL DEFAULT 0, avg_cpu REAL NOT NULL DEFAULT 0,
			max_cpu REAL NOT NULL DEFAULT 0, avg_memory REAL NOT NULL DEFAULT 0,
			max_memory REAL NOT NULL DEFAULT 0, avg_network_rx REAL NOT NULL DEFAULT 0,
			avg_network_tx REAL NOT NULL DEFAULT 0, avg_disk_read REAL NOT NULL DEFAULT 0,
			avg_disk_write REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (container_key, hour)
		)`,
		`INSERT INTO container_metrics (container_key, ts, cpu) VALUES ('c-old', 100, 1.0)`,
		`INSERT INTO container_metrics (container_key, ts, cpu) VALUES ('c-old', 200, 2.0)`,
		`INSERT INTO container_metrics (container_key, ts, cpu) VALUES ('c-old', 300, 3.0)`,
		`INSERT INTO container_metrics_hourly (container_key, hour, count, avg_cpu, max_cpu)
			VALUES ('c-old', 0, 3, 2.0, 3.0)`,
	} {
		if _, err := legacy.Exec(stmt); err != nil {
			legacy.Close()
			t.Fatalf("预置旧版遥测数据失败: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("关闭旧版库失败: %v", err)
	}

	if _, err := InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}

	raw, err := LoadMetricSamples("c-old", 0)
	if err != nil {
		t.Fatalf("迁移后读取原始样本失败: %v", err)
	}
	if len(raw) != 3 {
		t.Fatalf("迁移后应有 3 条原始样本，实际 %d 条", len(raw))
	}
	if raw[0].TS != 100 || raw[2].CPU != 3.0 {
		t.Fatalf("迁移后样本内容不一致: %+v", raw)
	}

	hourly, err := LoadMetricHourly("c-old", 0)
	if err != nil {
		t.Fatalf("迁移后读取小时聚合失败: %v", err)
	}
	if len(hourly) != 1 || hourly[0].Count != 3 {
		t.Fatalf("迁移后小时聚合不一致: %+v", hourly)
	}

	if tableExists(db, "container_metrics") {
		t.Error("迁移完成后应清理 config.db 里的旧遥测表")
	}
	if !strings.HasPrefix(getTelemetryDBPath(), dir) {
		t.Errorf("遥测库应位于数据目录内，实际 %s", getTelemetryDBPath())
	}
}

// TestConfigSaveNotBlockedByTelemetryLock 配置保存与遥测写入必须互不阻塞。
// 持住遥测写锁（模拟一次很慢的 rollup）时，配置保存仍应在毫秒级完成。
func TestConfigSaveNotBlockedByTelemetryLock(t *testing.T) {
	withSecGroupPersistTest(t)

	telemetryMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- MutateGlobal(func(cfg *EyvescloudConfig) {
			cfg.NodeGroups = append(cfg.NodeGroups, NodeGroup{ID: "ng-isolation", Name: "isolation"})
		})
	}()

	select {
	case err := <-done:
		telemetryMu.Unlock()
		if err != nil {
			t.Fatalf("配置保存失败: %v", err)
		}
	case <-time.After(10 * time.Second):
		telemetryMu.Unlock()
		t.Fatal("持有遥测锁期间配置保存被阻塞——配置与遥测仍未解耦（已回退到共享锁？）")
	}
}
