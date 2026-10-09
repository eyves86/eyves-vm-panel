package config

// telemetry_db_test.go —— 遥测（时序）与配置库解耦的回归测试（P1-c）。
//
// 背景：container_metrics / container_metrics_hourly 是全平台最高频的写（300k 容器
// 下尤甚）。改版前它们与整个配置快照共用同一个 `*sql.DB` 与同一把 dbMu，导致心跳/
// 指标写入与配置保存互相排队——任一方慢都直接拖住另一方。v3 起所有数据都在
// Postgres，遥测不再单开库文件，而是复用配置库 DSN 打开**独立连接池**：配置保存与
// 指标写入各走各的连接池，互不排队。
//
// 本组用例锁定三件事：
//  1. 遥测确实落在独立的 *sql.DB（与配置库不同的连接池）上，遥测表建在该池上；
//  2. 指标写入/读取跨重启保留；
//  3. 持住遥测写锁时配置保存不被阻塞（锁级解耦）。

import (
	"testing"
	"time"
)

// TestTelemetryUsesSeparateConnectionPool 遥测与配置必须各用独立的 *sql.DB。
func TestTelemetryUsesSeparateConnectionPool(t *testing.T) {
	withSecGroupPersistTest(t)

	if db == nil || telemetryDB == nil {
		t.Fatalf("配置库或遥测连接池未打开：db=%v telemetryDB=%v", db, telemetryDB)
	}
	if db == telemetryDB {
		t.Fatal("遥测与配置必须使用不同的 *sql.DB（当前仍是同一个连接池）")
	}

	// 遥测表建在遥测连接池上，可读。
	telemetryMu.RLock()
	var n int
	err := telemetryDB.QueryRow("SELECT COUNT(*) FROM container_metrics").Scan(&n)
	dsn := telemetryDSN
	telemetryMu.RUnlock()
	if err != nil {
		t.Fatalf("遥测连接池读取 container_metrics 失败: %v", err)
	}
	if dsn == "" {
		t.Fatal("telemetryDSN 应记录当前数据源（否则换源后遥测会静默写进旧库）")
	}
}

// TestTelemetrySamplesSurviveRestart 指标写入跨重启保留。
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
