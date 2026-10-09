package config

// telemetry_db.go —— 遥测（时序）数据用独立连接池承载，与配置库解耦。
//
// 背景（P1-c）：container_metrics / container_metrics_hourly 是整个面板最高频的写
// （300k 容器下尤甚）。v3 起全部数据都在 Postgres，遥测不再单独一个库文件，而是
// 复用配置库的 DSN 打开**独立连接池**——配置保存与指标写入各走各的连接，互不排队。
// 表名与配置库不冲突，同库不同表。
//
// 设计取舍：
//   - 遥测是**可丢弃数据**：不进自备份，恢复时丢的是历史曲线，不是业务真相。
//     配置库仍是唯一真相来源。（旧版遥测位于 SQLite 的 telemetry.db，属可丢弃数据，
//     不做跨引擎搬迁。）
//   - 读路径不加 telemetryMu（Postgres 读快照天然一致）；写路径仍串行，避免高频写
//     把连接池打满。连接池上限见 openPostgresConn。

import (
	"database/sql"
	"fmt"
	"sync"
)

var (
	// telemetryMu 保护 telemetryDB 句柄与遥测写路径。
	telemetryMu sync.RWMutex
	telemetryDB *sql.DB
	// telemetryDSN 记录当前句柄对应的 DSN。DSN 一旦变化（测试换 schema、运维改
	// 数据源），旧句柄必须关闭重开，否则遥测会静默写进已被废弃的数据源。
	telemetryDSN string
)

// openTelemetryDB 用配置库的 DSN 打开遥测连接池并建表。
//
// 调用约定：**由 openConfigDB 在持有 dbMu 的情况下调用**，因此本函数内部
// 绝不能再去获取 dbMu（Go 的 sync.Mutex 不可重入，会自锁）。锁序为
// dbMu → telemetryMu。
func openTelemetryDB(dsn string) error {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	if telemetryDB != nil {
		if telemetryDSN == dsn {
			return nil
		}
		// 注意：这里不能调 CloseTelemetryDB（它会再次获取同一把锁，自锁死锁）。
		_ = telemetryDB.Close()
		telemetryDB = nil
		telemetryDSN = ""
	}
	next, err := openPostgresConn(dsn)
	if err != nil {
		return fmt.Errorf("failed to open telemetry database: %v", err)
	}
	if err := ensureTelemetrySchema(next); err != nil {
		_ = next.Close()
		return err
	}
	telemetryDB = next
	telemetryDSN = dsn
	return nil
}

func ensureTelemetrySchema(handle *sql.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS container_metrics (
			container_key TEXT NOT NULL,
			ts BIGINT NOT NULL,
			cpu DOUBLE PRECISION NOT NULL DEFAULT 0,
			memory DOUBLE PRECISION NOT NULL DEFAULT 0,
			network_rx DOUBLE PRECISION NOT NULL DEFAULT 0,
			network_tx DOUBLE PRECISION NOT NULL DEFAULT 0,
			disk_read DOUBLE PRECISION NOT NULL DEFAULT 0,
			disk_write DOUBLE PRECISION NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_container_metrics_key_ts ON container_metrics (container_key, ts)`,
		`CREATE TABLE IF NOT EXISTS container_metrics_hourly (
			container_key TEXT NOT NULL,
			hour BIGINT NOT NULL,
			count BIGINT NOT NULL DEFAULT 0,
			avg_cpu DOUBLE PRECISION NOT NULL DEFAULT 0,
			max_cpu DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_memory DOUBLE PRECISION NOT NULL DEFAULT 0,
			max_memory DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_network_rx DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_network_tx DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_disk_read DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_disk_write DOUBLE PRECISION NOT NULL DEFAULT 0,
			PRIMARY KEY (container_key, hour)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_hourly_hour ON container_metrics_hourly (hour)`,
	} {
		if _, err := handle.Exec(stmt); err != nil {
			return fmt.Errorf("failed to initialize telemetry schema: %v", err)
		}
	}
	return nil
}

// CloseTelemetryDB 关闭遥测库连接。
func CloseTelemetryDB() {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	if telemetryDB != nil {
		_ = telemetryDB.Close()
		telemetryDB = nil
	}
	telemetryDSN = ""
}
