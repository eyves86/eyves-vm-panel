package config

// selfbackup.go —— 配置库（config.db）自备份与 WAL 维护。
//
// 背景：config.db 是整个平台的**唯一真相来源**——它损坏时客户的容器还在跑，
// 但面板再也管不了它们。生产实测该目录下 backups/ 长期为空，即"零备份"；
// 同时 WAL 文件已涨到 4.3MB（主库仅 436KB），说明检查点没被触发过。
//
// 设计取舍：
//   - 用 SQLite 的 `VACUUM INTO` 而不是拷文件。WAL 模式下主库文件本身不是
//     完整状态（最近的写入还在 -wal 里），直接 cp 会得到一份缺数据的备份。
//   - 快照按时间戳命名并轮转保留，避免把宿主磁盘写满。
//   - 备份失败只记日志、不影响主流程：面板可用性优先于快照完整性。

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// ConfigBackupDirName 是数据目录下存放快照的子目录名。
	ConfigBackupDirName = "backups"
	// 默认保留份数与间隔：每 6 小时一份、留 28 份（约一周）。
	defaultBackupInterval = 6 * time.Hour
	DefaultBackupKeep     = 28
	// WAL 检查点间隔：防止 -wal 无限增长（生产实测曾达 4.3MB）。
	walCheckpointInterval = 30 * time.Minute
)

// ConfigBackupDir 返回快照目录（不存在时不创建）。
func ConfigBackupDir() string {
	return filepath.Join(getDataDir(), ConfigBackupDirName)
}

// BackupConfigDatabase 生成一份配置库的一致性快照，返回快照路径。
//
// 使用 `VACUUM INTO`：它在事务内把当前数据库的完整内容写成一个新文件，
// 因此不受 WAL 中未合并数据的影响，也不需要停服。
func BackupConfigDatabase() (string, error) {
	dir := ConfigBackupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	dest := filepath.Join(dir, fmt.Sprintf("config-%s.db", time.Now().Format("20060102-150405")))

	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return "", fmt.Errorf("配置库尚未打开")
	}
	// VACUUM INTO 要求目标文件不存在。
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("清理同名快照失败: %w", err)
	}
	if _, err := db.Exec("VACUUM INTO ?", dest); err != nil {
		return "", fmt.Errorf("快照配置库失败: %w", err)
	}
	return dest, nil
}

// ListConfigBackups 按时间倒序返回现有快照路径。
func ListConfigBackups() ([]string, error) {
	entries, err := os.ReadDir(ConfigBackupDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "config-") && strings.HasSuffix(name, ".db") {
			out = append(out, filepath.Join(ConfigBackupDir(), name))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// PruneConfigBackups 只保留最近 keep 份快照，返回删除数量。
// keep <= 0 时使用 DefaultBackupKeep。
func PruneConfigBackups(keep int) (int, error) {
	if keep <= 0 {
		keep = DefaultBackupKeep
	}
	all, err := ListConfigBackups()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, path := range all[min(keep, len(all)):] {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// RunConfigBackupOnce 执行一次「快照 + 轮转」，返回快照路径。
func RunConfigBackupOnce(keep int) (string, error) {
	path, err := BackupConfigDatabase()
	if err != nil {
		return "", err
	}
	if _, err := PruneConfigBackups(keep); err != nil {
		// 轮转失败不影响本次快照的可用性。
		log.Printf("Warning: 清理旧配置快照失败: %v", err)
	}
	return path, nil
}

// StartConfigBackupScheduler 启动后台任务：定期快照配置库 + 触发 WAL 检查点。
//
// 启动后立即做一次快照（覆盖"上次运行至今从未备份"的情况），之后按 interval 循环。
// interval <= 0 时使用 6 小时。
func StartConfigBackupScheduler(interval time.Duration, keep int) {
	if interval <= 0 {
		interval = defaultBackupInterval
	}
	if keep <= 0 {
		keep = DefaultBackupKeep
	}
	go func() {
		// 首次快照稍等片刻，避开启动高峰（此时同时在建索引/探测容器）。
		time.Sleep(90 * time.Second)
		if path, err := RunConfigBackupOnce(keep); err != nil {
			log.Printf("Warning: 配置库自动快照失败: %v", err)
		} else {
			log.Printf("配置库快照已生成: %s", path)
		}

		backupTicker := time.NewTicker(interval)
		defer backupTicker.Stop()
		walTicker := time.NewTicker(walCheckpointInterval)
		defer walTicker.Stop()

		for {
			select {
			case <-backupTicker.C:
				if path, err := RunConfigBackupOnce(keep); err != nil {
					log.Printf("Warning: 配置库自动快照失败: %v", err)
				} else {
					log.Printf("配置库快照已生成: %s", path)
				}
			case <-walTicker.C:
				if err := CheckpointConfigWAL(); err != nil {
					log.Printf("Warning: WAL 检查点失败: %v", err)
				}
			}
		}
	}()
}

// CheckpointConfigWAL 把 WAL 内容合并回主库并截断 WAL 文件。
//
// 不做检查点时 -wal 会随写入持续增长（生产实测达到主库体积的 10 倍），
// 既占用磁盘也让崩溃恢复窗口变长。
func CheckpointConfigWAL() error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("配置库尚未打开")
	}
	// TRUNCATE 模式：合并后把 -wal 截断为 0 字节。
	_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
