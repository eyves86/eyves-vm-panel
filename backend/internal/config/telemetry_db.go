package config

// telemetry_db.go —— 遥测（时序）数据独立成库，与配置库彻底解耦。
//
// 背景（P1-c）：改版前 container_metrics / container_metrics_hourly 与整个配置
// 快照同处一个 config.db，且共用**同一个 `*sql.DB` 和同一把 dbMu**，而该连接池被
// 设为 MaxOpenConns(1)。后果是：
//
//   - 心跳/指标写入（300k 容器下是最高频的写）与配置保存互相排队，任一方慢都会
//     直接拖住另一方；
//   - SQLite 单写者模型下，一次大 rollup 事务会把面板的配置读写整体卡住。
//
// 设计取舍：
//   - 遥测单独一个文件 telemetry.db + 独立的连接池与互斥，配置库的写延迟不再受
//     遥测体量影响（反之亦然）。这是 #86「配置/遥测分表」的核心。
//   - 遥测是**可丢弃数据**：不进自备份（selfbackup 仍然只 VACUUM config.db），
//     恢复时丢的是历史曲线，不是业务真相。配置库仍是唯一真相来源。
//   - 读路径不加 telemetryMu：WAL 下读快照天然一致、且读不抢写锁；写路径仍串行，
//     以避免并发写触发 SQLITE_BUSY。这样连接池上限 >1 才真正有意义。
//   - pragma 全部移入 DSN（见 sqliteDSN）：池里每条新连接都会继承 busy_timeout /
//     synchronous / foreign_keys，而不只是建池时那一条。

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// telemetryMu 保护 telemetryDB 句柄与遥测写路径。
	// 读用 RLock（WAL 下读快照一致，允许多读并发），写用 Lock（SQLite 单写者）。
	telemetryMu sync.RWMutex
	telemetryDB *sql.DB
	// telemetryDBPath 记录当前句柄对应的库文件路径。配置路径一旦变化
	// （测试换临时目录、运维改数据目录），旧句柄必须关闭重开，否则遥测
	// 会静默写进已被废弃的目录。
	telemetryDBPath string
)

// 遥测连接池上限：独立于配置库，给读并发留出余量。
const (
	telemetryMaxOpenConns = 4
	telemetryMaxIdleConns = 4
)

// sqliteDSN 构造带 pragma 的 SQLite DSN。
//
// pragma 必须逐条 repeated `_pragma=`，且**不能**被 URL 编码——modernc 驱动会把
// 原样字符串交给 PRAGMA，例如 `_pragma=journal_mode(WAL)`。这里手工拼串而不用
// url.Values.Encode()，正是因为 Encode() 会把括号转义成 %28/%29 导致 pragma 失效。
func sqliteDSN(dbPath string, pragmas []string) string {
	var sb strings.Builder
	sb.WriteString("file:")
	sb.WriteString(filepath.ToSlash(dbPath))
	if len(pragmas) > 0 {
		sb.WriteString("?_pragma=")
		sb.WriteString(strings.Join(pragmas, "&_pragma="))
	}
	return sb.String()
}

// configDBPragmas / telemetryDBPragmas：配置库与遥测库的 pragma 集合。
// journal_mode=WAL 是库文件级属性（一次写入后持久），其余为连接级，
// 所以放进 DSN 才能保证池中每条连接都被设置。
var configDBPragmas = []string{
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"busy_timeout(5000)",
	"foreign_keys(1)",
}

var telemetryDBPragmas = []string{
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"busy_timeout(5000)",
}

// getTelemetryDBPath 返回遥测库路径：与配置库同目录，固定名 telemetry.db。
func getTelemetryDBPath() string {
	return filepath.Join(filepath.Dir(getDBPath()), "telemetry.db")
}

// openTelemetryDB 打开遥测库、建表并在需要时迁移遗留数据。
//
// 调用约定：**由 openConfigDB 在持有 dbMu 的情况下调用**，因此本函数内部
// 绝不能再去获取 dbMu（Go 的 sync.Mutex 不可重入，会自锁）。锁序为
// dbMu → telemetryMu。
func openTelemetryDB() error {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	want := getTelemetryDBPath()
	if telemetryDB != nil {
		if telemetryDBPath == want {
			return nil
		}
		// 注意：这里不能调 CloseTelemetryDB（它会再次获取同一把锁，自锁死锁）。
		_ = telemetryDB.Close()
		telemetryDB = nil
		telemetryDBPath = ""
	}
	if err := os.MkdirAll(filepath.Dir(want), 0700); err != nil {
		return fmt.Errorf("failed to create telemetry directory: %v", err)
	}
	next, err := sql.Open("sqlite", sqliteDSN(want, telemetryDBPragmas))
	if err != nil {
		return fmt.Errorf("failed to open telemetry database: %v", err)
	}
	next.SetMaxOpenConns(telemetryMaxOpenConns)
	next.SetMaxIdleConns(telemetryMaxIdleConns)

	if err := ensureTelemetrySchema(next); err != nil {
		_ = next.Close()
		return err
	}
	// 安全加固：文件直到 ensureTelemetrySchema 执行查询时才真正落盘，
	// chmod 必须放在其后——放在 sql.Open 之后会打在尚不存在的路径上静默失败，
	// 文件将以 0644 落盘（实测踩到过）。
	_ = os.Chmod(want, 0600)

	telemetryDB = next
	telemetryDBPath = want
	return nil
}

func ensureTelemetrySchema(handle *sql.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS container_metrics (
			container_key TEXT NOT NULL,
			ts INTEGER NOT NULL,
			cpu REAL NOT NULL DEFAULT 0,
			memory REAL NOT NULL DEFAULT 0,
			network_rx REAL NOT NULL DEFAULT 0,
			network_tx REAL NOT NULL DEFAULT 0,
			disk_read REAL NOT NULL DEFAULT 0,
			disk_write REAL NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_container_metrics_key_ts ON container_metrics (container_key, ts)`,
		`CREATE TABLE IF NOT EXISTS container_metrics_hourly (
			container_key TEXT NOT NULL,
			hour INTEGER NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			avg_cpu REAL NOT NULL DEFAULT 0,
			max_cpu REAL NOT NULL DEFAULT 0,
			avg_memory REAL NOT NULL DEFAULT 0,
			max_memory REAL NOT NULL DEFAULT 0,
			avg_network_rx REAL NOT NULL DEFAULT 0,
			avg_network_tx REAL NOT NULL DEFAULT 0,
			avg_disk_read REAL NOT NULL DEFAULT 0,
			avg_disk_write REAL NOT NULL DEFAULT 0,
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
	telemetryDBPath = ""
}

// tableExists 判断 config 库中是否存在某张表（用于遗留数据迁移的健壮性判断）。
func tableExists(handle *sql.DB, name string) bool {
	var n int
	err := handle.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return err == nil && n > 0
}

// migrateLegacyMetrics 把旧版 config.db 里的遥测行搬到 telemetry.db。
//
// 只在旧库仍留有 container_metrics 表时执行；搬迁并在行数校验一致后删除旧表
// （旧表自此不再被写入，留着只会白占 config.db 体积）。任何一步不满足就保留
// 旧表并记日志，绝不半途丢数据。
//
// 同样要求调用方已持有 dbMu（见 openTelemetryDB 的调用约定）。
func migrateLegacyMetrics() {
	if db == nil || telemetryDB == nil {
		return
	}
	if !tableExists(db, "container_metrics") {
		return
	}
	var legacyRaw, legacyHourly int
	_ = db.QueryRow(`SELECT COUNT(*) FROM container_metrics`).Scan(&legacyRaw)
	if tableExists(db, "container_metrics_hourly") {
		_ = db.QueryRow(`SELECT COUNT(*) FROM container_metrics_hourly`).Scan(&legacyHourly)
	}
	if legacyRaw == 0 && legacyHourly == 0 {
		// 表已在旧版被清空（或从未写入）：直接删掉空表，避免每次启动重复探测。
		dropLegacyMetricTables()
		return
	}

	if err := copyLegacyMetricRows("container_metrics", legacyRaw, func() (*sql.Rows, error) {
		return db.Query(`SELECT container_key, ts, cpu, memory, network_rx, network_tx, disk_read, disk_write FROM container_metrics`)
	}, `INSERT INTO container_metrics
		(container_key, ts, cpu, memory, network_rx, network_tx, disk_read, disk_write)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`); err != nil {
		log.Printf("Warning: 遥测数据迁移（原始样本）失败，保留旧表: %v", err)
		return
	}
	if legacyHourly > 0 {
		if err := copyLegacyMetricRows("container_metrics_hourly", legacyHourly, func() (*sql.Rows, error) {
			return db.Query(`SELECT container_key, hour, count, avg_cpu, max_cpu, avg_memory, max_memory,
				avg_network_rx, avg_network_tx, avg_disk_read, avg_disk_write FROM container_metrics_hourly`)
		}, `INSERT INTO container_metrics_hourly
			(container_key, hour, count, avg_cpu, max_cpu, avg_memory, max_memory,
			 avg_network_rx, avg_network_tx, avg_disk_read, avg_disk_write)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`); err != nil {
			log.Printf("Warning: 遥测数据迁移（小时聚合）失败，保留旧表: %v", err)
			return
		}
	}

	// 源表行数与目标库总行数一致才认为搬迁完整（目标库此前必为空库）。
	var newRaw int
	_ = telemetryDB.QueryRow(`SELECT COUNT(*) FROM container_metrics`).Scan(&newRaw)
	if newRaw < legacyRaw {
		log.Printf("Warning: 遥测数据迁移后行数不足（源 %d / 目标 %d），保留旧表", legacyRaw, newRaw)
		return
	}
	dropLegacyMetricTables()
	log.Printf("遥测数据已迁移至独立库 %s（原始样本 %d 行、小时聚合 %d 行）", getTelemetryDBPath(), legacyRaw, legacyHourly)
}

func copyLegacyMetricRows(table string, expected int, query func() (*sql.Rows, error), insert string) error {
	rows, err := query()
	if err != nil {
		return fmt.Errorf("读取旧表 %s 失败: %w", table, err)
	}
	defer rows.Close()

	// 列数固定，按宽度动态扫描。
	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	tx, err := telemetryDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(insert)
	if err != nil {
		return err
	}
	defer stmt.Close()

	written := 0
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		holders := make([]interface{}, len(cols))
		for i := range vals {
			holders[i] = &vals[i]
		}
		if err := rows.Scan(holders...); err != nil {
			return err
		}
		if _, err := stmt.Exec(vals...); err != nil {
			return err
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if written != expected {
		return fmt.Errorf("旧表 %s 读取行数 %d 与预期 %d 不一致", table, written, expected)
	}
	return tx.Commit()
}

func dropLegacyMetricTables() {
	// 旧遥测表已搬迁完毕，从配置库删除以回收空间。
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS container_metrics`,
		`DROP TABLE IF EXISTS container_metrics_hourly`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			log.Printf("Warning: 清理旧遥测表失败: %v", err)
		}
	}
}
