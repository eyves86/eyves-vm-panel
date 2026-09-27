package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"eyvescloud/internal/storage"
)

var (
	dbMu sync.Mutex
	db   *sql.DB
)

type savedTaskConfig struct {
	Name                 string        `json:"name"`
	Virtualization       string        `json:"virtualization,omitempty"`
	TemplateID           string        `json:"template_id"`
	StoragePoolID        string        `json:"storage_pool_id,omitempty"`
	VCPU                 float64       `json:"vcpu"`
	CPUPercent           int           `json:"cpu_percent"`
	RAMMB                int           `json:"ram_mb"`
	DiskGB               float64       `json:"disk_gb"`
	NetworkBWMbps        int           `json:"network_bw_mbps"`
	NetworkDownMbps      int           `json:"network_down_mbps"`
	NetworkUpMbps        int           `json:"network_up_mbps"`
	MonthlyTrafficGB     int           `json:"monthly_traffic_gb"`
	TrafficMode          string        `json:"traffic_mode"`
	TrafficInGB          int           `json:"traffic_in_gb"`
	TrafficOutGB         int           `json:"traffic_out_gb"`
	IOSpeedMBps          int           `json:"io_speed_mbps"`
	IOReadMBps           int           `json:"io_read_mbps"`
	IOWriteMBps          int           `json:"io_write_mbps"`
	ExtraPorts           []int         `json:"extra_ports"`
	NATPortMappings      []PortMapping `json:"nat_port_mappings,omitempty"`
	ManagementPort       int           `json:"management_port,omitempty"`
	PortMappingCount     int           `json:"port_mapping_count"`
	AssignNAT            *bool         `json:"assign_nat,omitempty"`
	LANIPv4Mode          string        `json:"lan_ipv4_mode,omitempty"`
	LANInterface         string        `json:"lan_interface,omitempty"`
	LANIPv4Address       string        `json:"lan_ipv4_address,omitempty"`
	LANIPv4PrefixLen     int           `json:"lan_ipv4_prefix_len,omitempty"`
	LANIPv4Gateway       string        `json:"lan_ipv4_gateway,omitempty"`
	SnapshotLimit        int           `json:"snapshot_limit"`
	AllowedImageIDs      []string      `json:"allowed_image_ids,omitempty"`
	ImageLimitConfigured bool          `json:"image_limit_configured,omitempty"`
	AssignIPv4           bool          `json:"assign_ipv4"`
	IPv4Count            int           `json:"ipv4_count,omitempty"`
	PublicIPv4s          []string      `json:"public_ipv4s,omitempty"`
	AssignIPv6           bool          `json:"assign_ipv6"`
	IPv6Count            int           `json:"ipv6_count,omitempty"`
	IPv6Addresses        []string      `json:"ipv6_addresses,omitempty"`
	SSHAuthMode          string        `json:"ssh_auth_mode,omitempty"`
	SSHPassword          string        `json:"ssh_password,omitempty"`
	SSHPublicKey         string        `json:"ssh_public_key,omitempty"`
	ExpiresAt            string        `json:"expires_at"`
}

func parseSavedTaskConfig(raw string) savedTaskConfig {
	if raw == "" {
		return savedTaskConfig{}
	}
	var cfg savedTaskConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	normalizeSavedTaskConfigLimits(&cfg)
	return cfg
}

func encodeSavedTaskConfig(cfg savedTaskConfig) string {
	normalizeSavedTaskConfigLimits(&cfg)
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeSavedTaskConfigLimits(cfg *savedTaskConfig) {
	if cfg == nil {
		return
	}
	if cfg.NetworkBWMbps < 0 {
		cfg.NetworkBWMbps = 0
	}
	if cfg.NetworkDownMbps < 0 {
		cfg.NetworkDownMbps = 0
	}
	if cfg.NetworkUpMbps < 0 {
		cfg.NetworkUpMbps = 0
	}
	if cfg.NetworkDownMbps == 0 && cfg.NetworkUpMbps == 0 && cfg.NetworkBWMbps > 0 {
		cfg.NetworkDownMbps = cfg.NetworkBWMbps
		cfg.NetworkUpMbps = cfg.NetworkBWMbps
	}
	cfg.NetworkBWMbps = LegacySymmetricLimit(cfg.NetworkDownMbps, cfg.NetworkUpMbps)

	if cfg.IOSpeedMBps < 0 {
		cfg.IOSpeedMBps = 0
	}
	if cfg.IOReadMBps < 0 {
		cfg.IOReadMBps = 0
	}
	if cfg.IOWriteMBps < 0 {
		cfg.IOWriteMBps = 0
	}
	if cfg.IOReadMBps == 0 && cfg.IOWriteMBps == 0 && cfg.IOSpeedMBps > 0 {
		cfg.IOReadMBps = cfg.IOSpeedMBps
		cfg.IOWriteMBps = cfg.IOSpeedMBps
	}
	cfg.IOSpeedMBps = LegacySymmetricLimit(cfg.IOReadMBps, cfg.IOWriteMBps)
}

func encodeStringSlice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func getDBPath() string {
	cfgPath := getConfigPath()
	ext := filepath.Ext(cfgPath)
	if ext == "" {
		return cfgPath + ".db"
	}
	return strings.TrimSuffix(cfgPath, ext) + ".db"
}

func openConfigDB() error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		return nil
	}
	dbPath := getDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return fmt.Errorf("failed to create database directory: %v", err)
	}
	next, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %v", err)
	}
	next.SetMaxOpenConns(1)
	next.SetMaxIdleConns(1)
	// 安全加固：SQLite 文件含 AdminPassHash / JWTSecret / ApiKeyHash 等敏感字段，
	// 无论是否新建，都把它严格锁到当前用户可读。
	_ = os.Chmod(dbPath, 0600)

	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := next.Exec(stmt); err != nil {
			_ = next.Close()
			return fmt.Errorf("failed to initialize sqlite pragma: %v", err)
		}
	}

	db = next
	return ensureSchema()
}

func ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS app_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS containers (
			id INTEGER PRIMARY KEY,
			uuid TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			virtualization TEXT,
			lxc_name TEXT,
			kvm_name TEXT,
			disk_image TEXT,
			storage_pool_id TEXT,
			storage_path TEXT,
			mac_address TEXT,
			template TEXT,
			vcpu REAL,
			ram_mb INTEGER,
			disk_gb REAL,
			data_disk_gb REAL NOT NULL DEFAULT 0,
			data_disk_mount_path TEXT,
			network_bw_mbps INTEGER,
			network_down_mbps INTEGER NOT NULL DEFAULT 0,
			network_up_mbps INTEGER NOT NULL DEFAULT 0,
			monthly_traffic_gb INTEGER,
			traffic_mode TEXT,
			traffic_in_gb INTEGER,
			traffic_out_gb INTEGER,
			traffic_used_rx INTEGER,
			traffic_used_tx INTEGER,
			traffic_reset_date TEXT,
			io_speed_mbps INTEGER,
			io_read_mbps INTEGER NOT NULL DEFAULT 0,
			io_write_mbps INTEGER NOT NULL DEFAULT 0,
			status TEXT,
			restore_on_host_boot INTEGER NOT NULL DEFAULT 0,
			ip TEXT,
			lan_ipv4_mode TEXT,
			lan_interface TEXT,
			lan_ipv4_address TEXT,
			lan_ipv4_prefix_len INTEGER,
			lan_ipv4_gateway TEXT,
			ipv6 TEXT,
			ipv6_prefix_len INTEGER,
			ipv6_interface TEXT,
			vnc_port INTEGER,
			ssh_port INTEGER,
			ssh_password TEXT,
			ssh_host_key TEXT,
			port_mapping_limit INTEGER,
			snapshot_limit INTEGER,
			created_at TEXT,
			expires_at TEXT,
			snapshot_schedule_enabled INTEGER,
			snapshot_schedule_interval_hours INTEGER,
			snapshot_schedule_time TEXT,
			snapshot_schedule_last_run TEXT,
			snapshot_schedule_next_run TEXT,
			snapshot_schedule_created_by TEXT,
			policy_blocked INTEGER,
			policy_blocked_reason TEXT,
			policy_blocked_at TEXT,
			allowed_image_ids TEXT,
			image_limit_configured INTEGER NOT NULL DEFAULT 0,
			rescue_enabled INTEGER NOT NULL DEFAULT 0,
			rescue_iso_id TEXT,
			rescue_iso_path TEXT,
			optional_iso_id TEXT,
			optional_iso_path TEXT,
			suspended INTEGER NOT NULL DEFAULT 0,
			suspended_at TEXT,
			suspended_reason TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS port_mappings (
			container_id INTEGER NOT NULL,
			position INTEGER NOT NULL,
			container_port INTEGER NOT NULL,
			host_port INTEGER NOT NULL,
			host_ip TEXT,
			protocol TEXT,
			description TEXT,
			PRIMARY KEY (container_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS container_public_ipv4s (
			container_id INTEGER NOT NULL,
			position INTEGER NOT NULL,
			address TEXT NOT NULL,
			interface TEXT,
			prefix_len INTEGER,
			gateway TEXT,
			rdns TEXT,
			PRIMARY KEY (container_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS container_ipv6_addresses (
			container_id INTEGER NOT NULL,
			position INTEGER NOT NULL,
			address TEXT NOT NULL,
			prefix_len INTEGER,
			interface TEXT,
			rdns TEXT,
			PRIMARY KEY (container_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS sub_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			password TEXT,
			pass_hash TEXT,
			access_code TEXT,
			created_at TEXT,
			token_version INTEGER,
			allowed_image_ids TEXT,
			image_limit_configured INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS sub_user_container_names (
			sub_user_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			container_name TEXT NOT NULL,
			PRIMARY KEY (sub_user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS sub_user_container_uuids (
			sub_user_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			container_uuid TEXT NOT NULL,
			PRIMARY KEY (sub_user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			name TEXT,
			key_hash TEXT,
			key_fingerprint TEXT,
			prefix TEXT,
			ip_whitelist TEXT,
			created_at TEXT,
			last_used TEXT,
			scopes TEXT,
			expires_at TEXT,
			disabled INTEGER,
			container_uuids TEXT,
			last_used_ip TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time TEXT,
			action TEXT,
			target TEXT,
			detail TEXT,
			user TEXT,
			ip TEXT,
			user_agent TEXT,
			success_set INTEGER,
			success INTEGER,
			error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS security_conntrack_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			container_ip TEXT NOT NULL,
			line TEXT NOT NULL,
			captured_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conntrack_snapshots_ip_time
			ON security_conntrack_snapshots(container_ip, captured_at)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			type TEXT,
			container_id INTEGER,
			container_name TEXT,
			status TEXT,
			error TEXT,
			created_at TEXT,
			template_id TEXT,
			user TEXT,
			ip TEXT,
			user_agent TEXT,
			cfg_name TEXT,
			cfg_virtualization TEXT,
			cfg_template_id TEXT,
			cfg_vcpu REAL,
			cfg_cpu_percent INTEGER,
			cfg_ram_mb INTEGER,
			cfg_disk_gb REAL,
			cfg_network_bw_mbps INTEGER,
			cfg_network_down_mbps INTEGER NOT NULL DEFAULT 0,
			cfg_network_up_mbps INTEGER NOT NULL DEFAULT 0,
			cfg_monthly_traffic_gb INTEGER,
			cfg_traffic_mode TEXT,
			cfg_traffic_in_gb INTEGER,
			cfg_traffic_out_gb INTEGER,
			cfg_io_speed_mbps INTEGER,
			cfg_io_read_mbps INTEGER NOT NULL DEFAULT 0,
			cfg_io_write_mbps INTEGER NOT NULL DEFAULT 0,
			cfg_management_port INTEGER NOT NULL DEFAULT 0,
			cfg_port_mapping_count INTEGER,
			cfg_assign_nat INTEGER,
			cfg_lan_ipv4_mode TEXT,
			cfg_lan_interface TEXT,
			cfg_lan_ipv4_address TEXT,
			cfg_lan_ipv4_prefix_len INTEGER,
			cfg_lan_ipv4_gateway TEXT,
			cfg_snapshot_limit INTEGER,
			cfg_assign_ipv4 INTEGER,
			cfg_ipv4_count INTEGER,
			cfg_public_ipv4s TEXT,
			cfg_assign_ipv6 INTEGER,
			cfg_ipv6_count INTEGER,
			cfg_ipv6_addresses TEXT,
			cfg_ssh_auth_mode TEXT,
			cfg_ssh_password TEXT,
			cfg_ssh_public_key TEXT,
			cfg_allowed_image_ids TEXT,
			cfg_image_limit_configured INTEGER NOT NULL DEFAULT 0,
			cfg_expires_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS task_extra_ports (
			task_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			port INTEGER NOT NULL,
			PRIMARY KEY (task_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS task_nat_port_mappings (
			task_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			host_port INTEGER NOT NULL,
			container_port INTEGER NOT NULL,
			protocol TEXT,
			description TEXT,
			PRIMARY KEY (task_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS login_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time TEXT,
			username TEXT,
			ip TEXT,
			user_agent TEXT,
			success INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS enabled_images (
			position INTEGER PRIMARY KEY,
			image_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS snapshots (
			id TEXT PRIMARY KEY,
			container_id INTEGER,
			container_name TEXT,
			lxc_name TEXT,
			created_at TEXT,
			created_by TEXT,
			scheduled INTEGER,
			path TEXT,
			size_bytes INTEGER
		)`,
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
		// P0-1 存储抽象层：卷表，把卷与容器/池解耦。
		// 卷记录独立于 saveConfigToDB 的全量快照流程（不在其 DELETE 列表中），
		// 采用即时 CRUD，与 security_conntrack_snapshots 的直写模式一致。
		`CREATE TABLE IF NOT EXISTS volumes (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			size_mb INTEGER NOT NULL DEFAULT 0,
			attached_to_container_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_volumes_container ON volumes (attached_to_container_id)`,
		// P1-1 节点状态机 + 租约锁
		`CREATE TABLE IF NOT EXISTS node_leases (
			node_id TEXT PRIMARY KEY,
			token TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			in_progress INTEGER NOT NULL DEFAULT 0
		)`,
		// 任务终态留档表：独立于 tasks / task_extra_ports / task_nat_port_mappings
		// 这三张"创建续跑"表。后者只保存 pending/running 用于重启续跑，
		// 完成/失败/取消的任务在此留档，供任务历史与任务中心查询。
		`CREATE TABLE IF NOT EXISTS task_history (
			id            TEXT PRIMARY KEY,
			type          TEXT,
			container_id  INTEGER,
			container_name TEXT,
			status        TEXT,
			error         TEXT,
			stage         TEXT,
			stage_detail  TEXT,
			percent       INTEGER NOT NULL DEFAULT 0,
			user          TEXT,
			ip            TEXT,
			user_agent    TEXT,
			created_at    TEXT,
			started_at    TEXT,
			ended_at      TEXT,
			duration_ms   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_created ON task_history (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_status ON task_history (status)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_container ON task_history (container_id)`,
		// 任务日志表：按 task_id 追加的过程日志（INFO/WARN/ERROR）。
		`CREATE TABLE IF NOT EXISTS task_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id    TEXT NOT NULL,
			level      TEXT NOT NULL,
			message    TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_logs_task ON task_logs (task_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create sqlite schema: %v", err)
		}
	}
	return ensureSchemaMigrations()
}

func ensureSchemaMigrations() error {
	added := map[string]bool{}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{"api_keys", "scopes", "TEXT"},
		{"api_keys", "expires_at", "TEXT"},
		{"api_keys", "disabled", "INTEGER"},
		{"api_keys", "container_uuids", "TEXT"},
		{"api_keys", "last_used_ip", "TEXT"},
		{"api_keys", "key_fingerprint", "TEXT"},
		{"tasks", "ip", "TEXT"},
		{"tasks", "user_agent", "TEXT"},
		{"tasks", "cfg_network_down_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_network_up_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_io_read_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_io_write_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_management_port", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_assign_ipv4", "INTEGER"},
		{"tasks", "cfg_ipv4_count", "INTEGER"},
		{"tasks", "cfg_public_ipv4s", "TEXT"},
		{"tasks", "cfg_assign_nat", "INTEGER"},
		{"tasks", "cfg_lan_ipv4_mode", "TEXT"},
		{"tasks", "cfg_lan_interface", "TEXT"},
		{"tasks", "cfg_lan_ipv4_address", "TEXT NOT NULL DEFAULT ''"},
		{"tasks", "cfg_lan_ipv4_prefix_len", "INTEGER NOT NULL DEFAULT 0"},
		{"tasks", "cfg_lan_ipv4_gateway", "TEXT NOT NULL DEFAULT ''"},
		{"tasks", "cfg_ipv6_count", "INTEGER"},
		{"tasks", "cfg_ipv6_addresses", "TEXT"},
		{"tasks", "cfg_ssh_auth_mode", "TEXT"},
		{"tasks", "cfg_ssh_password", "TEXT"},
		{"tasks", "cfg_ssh_public_key", "TEXT"},
		{"tasks", "cfg_allowed_image_ids", "TEXT"},
		{"tasks", "cfg_image_limit_configured", "INTEGER NOT NULL DEFAULT 0"},
		{"port_mappings", "host_ip", "TEXT"},
		{"container_public_ipv4s", "prefix_len", "INTEGER"},
		{"container_public_ipv4s", "gateway", "TEXT"},
		{"container_public_ipv4s", "rdns", "TEXT"},
		{"container_ipv6_addresses", "rdns", "TEXT"},
		{"sub_users", "allowed_image_ids", "TEXT"},
		{"sub_users", "image_limit_configured", "INTEGER NOT NULL DEFAULT 0"},
		{"sub_users", "role", "TEXT NOT NULL DEFAULT 'operator'"},
		{"sub_users", "tenant", "TEXT NOT NULL DEFAULT ''"},
		{"containers", "network_down_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "network_up_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "io_read_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "io_write_mbps", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "firewall_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "firewall_default_action", "TEXT NOT NULL DEFAULT 'DROP'"},
		{"containers", "firewall_rules", "TEXT"},
		{"containers", "allowed_image_ids", "TEXT"},
		{"containers", "image_limit_configured", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "tenant", "TEXT NOT NULL DEFAULT ''"},
		{"containers", "restore_on_host_boot", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "storage_pool_id", "TEXT"},
		{"containers", "storage_path", "TEXT"},
		{"containers", "lan_ipv4_mode", "TEXT"},
		{"containers", "lan_interface", "TEXT"},
		{"containers", "lan_ipv4_address", "TEXT NOT NULL DEFAULT ''"},
		{"containers", "lan_ipv4_prefix_len", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "lan_ipv4_gateway", "TEXT NOT NULL DEFAULT ''"},
		{"containers", "cloud_init_user_data", "TEXT"},
		{"containers", "data_disk_gb", "REAL NOT NULL DEFAULT 0"},
		{"containers", "data_disk_mount_path", "TEXT"},
		{"containers", "rescue_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "rescue_iso_id", "TEXT"},
		{"containers", "rescue_iso_path", "TEXT"},
		{"containers", "optional_iso_id", "TEXT"},
		{"containers", "optional_iso_path", "TEXT"},
		// P0-1 存储抽象层：容器与卷解耦（旧数据为空 = 直连路径模式，不迁移）。
		{"containers", "root_volume_id", "TEXT"},
		{"containers", "data_volume_ids", "TEXT"},
		// 欠费停机（suspend）状态持久化：挂起的容器重启后仍保持挂起。
		{"containers", "suspended", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "suspended_at", "TEXT"},
		{"containers", "suspended_reason", "TEXT"},
	} {
		wasAdded, err := ensureColumn(column.table, column.name, column.def)
		if err != nil {
			return err
		}
		if wasAdded {
			added[column.table+"."+column.name] = true
		}
	}
	if added["containers.network_down_mbps"] || added["containers.network_up_mbps"] {
		if _, err := db.Exec(`UPDATE containers
			SET network_down_mbps = COALESCE(NULLIF(network_down_mbps, 0), COALESCE(network_bw_mbps, 0)),
			    network_up_mbps = COALESCE(NULLIF(network_up_mbps, 0), COALESCE(network_bw_mbps, 0))
			WHERE COALESCE(network_bw_mbps, 0) > 0`); err != nil {
			return err
		}
	}
	if added["containers.io_read_mbps"] || added["containers.io_write_mbps"] {
		if _, err := db.Exec(`UPDATE containers
			SET io_read_mbps = COALESCE(NULLIF(io_read_mbps, 0), COALESCE(io_speed_mbps, 0)),
			    io_write_mbps = COALESCE(NULLIF(io_write_mbps, 0), COALESCE(io_speed_mbps, 0))
			WHERE COALESCE(io_speed_mbps, 0) > 0`); err != nil {
			return err
		}
	}
	if added["tasks.cfg_network_down_mbps"] || added["tasks.cfg_network_up_mbps"] {
		if _, err := db.Exec(`UPDATE tasks
			SET cfg_network_down_mbps = COALESCE(NULLIF(cfg_network_down_mbps, 0), COALESCE(cfg_network_bw_mbps, 0)),
			    cfg_network_up_mbps = COALESCE(NULLIF(cfg_network_up_mbps, 0), COALESCE(cfg_network_bw_mbps, 0))
			WHERE COALESCE(cfg_network_bw_mbps, 0) > 0`); err != nil {
			return err
		}
	}
	if added["tasks.cfg_io_read_mbps"] || added["tasks.cfg_io_write_mbps"] {
		if _, err := db.Exec(`UPDATE tasks
			SET cfg_io_read_mbps = COALESCE(NULLIF(cfg_io_read_mbps, 0), COALESCE(cfg_io_speed_mbps, 0)),
			    cfg_io_write_mbps = COALESCE(NULLIF(cfg_io_write_mbps, 0), COALESCE(cfg_io_speed_mbps, 0))
			WHERE COALESCE(cfg_io_speed_mbps, 0) > 0`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`UPDATE containers
		SET lan_ipv4_mode = COALESCE(lan_ipv4_mode, ''),
		    lan_interface = COALESCE(lan_interface, ''),
		    lan_ipv4_address = COALESCE(lan_ipv4_address, ''),
		    lan_ipv4_prefix_len = COALESCE(lan_ipv4_prefix_len, 0),
		    lan_ipv4_gateway = COALESCE(lan_ipv4_gateway, '')`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE containers
		SET storage_pool_id = COALESCE(storage_pool_id, ''),
		    storage_path = COALESCE(storage_path, '')`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE tasks
		SET cfg_lan_ipv4_mode = COALESCE(cfg_lan_ipv4_mode, ''),
		    cfg_lan_interface = COALESCE(cfg_lan_interface, ''),
		    cfg_lan_ipv4_address = COALESCE(cfg_lan_ipv4_address, ''),
		    cfg_lan_ipv4_prefix_len = COALESCE(cfg_lan_ipv4_prefix_len, 0),
		    cfg_lan_ipv4_gateway = COALESCE(cfg_lan_ipv4_gateway, '')`); err != nil {
		return err
	}
	return nil
}

func ensureColumn(table, name, def string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if columnName == name {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + def)
	return err == nil, err
}

func loadConfigFromDB() (*EyvescloudConfig, bool, error) {
	meta := map[string]string{}
	rows, err := db.Query("SELECT key, value FROM app_meta")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, false, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if meta["admin_user"] == "" {
		return nil, false, nil
	}

	cfg := &EyvescloudConfig{
		AdminUser:            meta["admin_user"],
		AdminPassHash:        meta["admin_pass_hash"],
		AdminTOTPSecret:      meta["admin_totp_secret"],
		AdminTOTPEnabled:     atob(meta["admin_totp_enabled"]),
		JWTSecret:            meta["jwt_secret"],
		Port:                 atoi(meta["port"]),
		DataDir:              meta["data_dir"],
		NextContainerID:      atoi(meta["next_container_id"]),
		NextVNCPort:          atoi(meta["next_vnc_port"]),
		NextSSHPort:          atoi(meta["next_ssh_port"]),
		NATPortStart:         atoi(meta["nat_port_start"]),
		NATPortEnd:           atoi(meta["nat_port_end"]),
		LXCNATSubnet:         meta["lxc_nat_subnet"],
		KVMNATSubnet:         meta["kvm_nat_subnet"],
		SetupComplete:        atob(meta["setup_complete"]),
		SecurityAutoShutdown: atob(meta["security_auto_shutdown"]),
		ARPProtectionEnabled: atob(meta["arp_protection_enabled"]),
		IPAntiSpoofEnabled:   atob(meta["ip_anti_spoof_enabled"]),
		// 滥用检测默认开启：老库没有该键时按开启处理，保证升级后检测不中断；
		// 管理员显式关闭后会写入 "0"，此后保持关闭。
		AbuseDetectionEnabled: atobDefault(meta, "abuse_detection_enabled", true),
		TaskConcurrency:      atoi(meta["task_concurrency"]),
		Language:             meta["language"],
		LoginFooterText:      meta["login_footer_text"],
		LoginFooterHidden:    atob(meta["login_footer_hidden"]),
		PanelDomain:          meta["panel_domain"],
		TurnstileSiteKey:     meta["turnstile_site_key"],
		TurnstileSecretKey:   meta["turnstile_secret_key"],
		TurnstileAdminLogin:  atob(meta["turnstile_admin_login"]),
		TurnstileUserLogin:   atob(meta["turnstile_user_login"]),
		MetricRetentionDays:  atoi(meta["metric_retention_days"]),
		AuditRetentionDays:   atoi(meta["audit_retention_days"]),
		MemoryOvercommitEnabled: atob(meta["memory_overcommit_enabled"]),
		MemoryOvercommitRatio:   atof(meta["memory_overcommit_ratio"]),
		NATSubnetOversubscription: atob(meta["nat_subnet_oversubscription"]),
		DiskOvercommitRatio:       atof(meta["disk_overcommit_ratio"]),
		// 节点对接密钥（本面板作为被控）：密文落库，读取后下方统一解密。
		AgentPairingKey:        meta["agent_pairing_key"],
		AgentPairingKeyExpiry: meta["agent_pairing_key_expiry"],
		// 更新源：platform/owner/repo/branch/asset_prefix 明文；token 单独加密字段。
		UpdateSource: UpdateSource{
			Platform:    meta["update_source_platform"],
			Owner:       meta["update_source_owner"],
			Repo:        meta["update_source_repo"],
			Branch:      meta["update_source_branch"],
			AssetPrefix: meta["update_source_asset_prefix"],
		},
	}
	if raw := strings.TrimSpace(meta["ksm_tuning"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.KSMTuning)
	}
	if raw := strings.TrimSpace(meta["regions"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Regions)
	}
	if raw := strings.TrimSpace(meta["ip_groups"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.IPGroups)
	}
	if raw := strings.TrimSpace(meta["iso_files"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.ISOFiles)
	}
	if raw := strings.TrimSpace(meta["backup_settings"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.BackupSettings)
	}
	if raw := strings.TrimSpace(meta["instance_backup_settings"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.InstanceBackupSettings)
	}
	if raw := strings.TrimSpace(meta["remote_backup_settings"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.RemoteBackupSettings)
	}
	if raw := strings.TrimSpace(meta["smtp_settings"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.SMTPSettings)
	}
	if raw := strings.TrimSpace(meta["backups"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Backups)
	}
	if raw := strings.TrimSpace(meta["instance_backups"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.InstanceBackups)
	}
	if raw := strings.TrimSpace(meta["backup_plans"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.BackupPlans)
	}
	if raw := strings.TrimSpace(meta["api_rate_limit"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.APIRateLimit)
	}
	if raw := strings.TrimSpace(meta["tenants"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Tenants)
	}
	if raw := strings.TrimSpace(meta["admin_backup_codes"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.AdminBackupCodes)
	}
	if raw := strings.TrimSpace(meta["admins"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Admins)
	}
	cfg.AdminPath = meta["admin_path"]
	if raw := strings.TrimSpace(meta["ssl"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.SSL)
	}
	if raw := strings.TrimSpace(meta["ssl_certificates"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.SSLCertificates)
	}
	if raw := strings.TrimSpace(meta["public_ipv4_pool"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.PublicIPv4Pool)
	}
	if raw := strings.TrimSpace(meta["public_ipv6_prefixes"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.PublicIPv6Prefixes)
	}
	if raw := strings.TrimSpace(meta["webssh_allowed_origins"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.WebSSHAllowedOrigins)
	}
	if raw := strings.TrimSpace(meta["panel_access_policy"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.PanelAccessPolicy)
	}
	if raw := strings.TrimSpace(meta["storage_pools"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.StoragePools)
	}
	if raw := strings.TrimSpace(meta["custom_kvm_images"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.CustomKVMImages)
	}
	if raw := strings.TrimSpace(meta["custom_lxc_images"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.CustomLXCImages)
	}
	loadPolicyState(cfg, meta)
	if raw := strings.TrimSpace(meta["nodes"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Nodes)
	}
	// F7/P2-11：读取时把 enc:v1: 密文还原为明文 Token / InstallKey（内存态保持明文）。
	// 存量明文值（无前缀）原样通过；解密失败不阻断启动，但该节点 Token
	// 置空使其失效，等待重新注册——宁可断连也不能拿密文当凭据误用。
	// install_key 解密失败同样置空（key 换发即可恢复，无需断连节点）。
	for i := range cfg.Nodes {
		plain, err := DecryptNodeToken(cfg.Nodes[i].Token)
		if err != nil {
			cfg.Nodes[i].Token = ""
			continue
		}
		cfg.Nodes[i].Token = plain
		plainKey, err := DecryptNodeToken(cfg.Nodes[i].InstallKey)
		if err != nil {
			cfg.Nodes[i].InstallKey = ""
			cfg.Nodes[i].InstallKeyCreatedAt = ""
		} else {
			cfg.Nodes[i].InstallKey = plainKey
		}
	}
	if cfg.Nodes == nil {
		cfg.Nodes = []Node{}
	}

	// TurnstileSecretKey 解密：存量明文（无 enc: 前缀）原样通过；解密失败
	// 置空并关闭两处登录验证（密钥残缺时放行比误拦更可恢复，配置页重存即可）。
	if raw := strings.TrimSpace(cfg.TurnstileSecretKey); raw != "" {
		plainTS, err := DecryptNodeToken(raw)
		if err != nil {
			cfg.TurnstileSecretKey = ""
			cfg.TurnstileAdminLogin = false
			cfg.TurnstileUserLogin = false
		} else {
			cfg.TurnstileSecretKey = plainTS
		}
	}

	// 节点对接密钥解密（与节点 Token 同级敏感：enc:v1 密文落库，内存态明文）。
	// 存量明文原样通过；解密失败置空（重新生成即可，不影响已对接节点）。
	if raw := strings.TrimSpace(cfg.AgentPairingKey); raw != "" {
		plainPK, err := DecryptNodeToken(raw)
		if err != nil {
			cfg.AgentPairingKey = ""
			cfg.AgentPairingKeyExpiry = ""
		} else {
			cfg.AgentPairingKey = plainPK
		}
	}

	// 更新源 token 解密（可选，私有仓库才填；加密方式与 Turnstile 等同级）。
	if raw := strings.TrimSpace(meta["update_source_token"]); raw != "" {
		plainT, err := DecryptNodeToken(raw)
		if err == nil {
			cfg.UpdateSource.Token = plainT
		}
	}

	if cfg.Containers, err = loadContainers(); err != nil {
		return nil, false, err
	}
	if cfg.SubUsers, err = loadSubUsers(); err != nil {
		return nil, false, err
	}
	if cfg.ApiKeys, err = loadAPIKeys(); err != nil {
		return nil, false, err
	}
	if cfg.AuditLogs, err = loadAuditLogs(); err != nil {
		return nil, false, err
	}
	if cfg.Tasks, err = loadTasks(); err != nil {
		return nil, false, err
	}
	if cfg.LoginLogs, err = loadLoginLogs(); err != nil {
		return nil, false, err
	}
	if cfg.EnabledImages, err = loadEnabledImages(); err != nil {
		return nil, false, err
	}
	if cfg.Snapshots, err = loadSnapshots(); err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

func saveConfigToDB() error {
	// nil 检查必须在 dbMu 之内：CloseConfigDB 会在锁内把 db 置 nil，
	// 后台任务队列 goroutine 若在锁外先判 nil 再拿锁，会在两者之间
	// 被关闭方抢占，随后对 nil *sql.DB 调 Begin() 直接 panic。
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("sqlite database is not initialized")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, table := range []string{
		"port_mappings",
		"container_public_ipv4s",
		"container_ipv6_addresses",
		"sub_user_container_names",
		"sub_user_container_uuids",
		"containers",
		"sub_users",
		"api_keys",
		"audit_logs",
		"task_extra_ports",
		"task_nat_port_mappings",
		"tasks",
		"login_logs",
		"enabled_images",
		"snapshots",
		"app_meta",
	} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}

	if err := saveMeta(tx); err != nil {
		return err
	}
	if err := saveContainers(tx); err != nil {
		return err
	}
	if err := saveSubUsers(tx); err != nil {
		return err
	}
	if err := saveAPIKeys(tx); err != nil {
		return err
	}
	if err := saveAuditLogs(tx); err != nil {
		return err
	}
	if err := saveTasksDB(tx); err != nil {
		return err
	}
	if err := saveLoginLogs(tx); err != nil {
		return err
	}
	if err := saveEnabledImages(tx); err != nil {
		return err
	}
	if err := saveSnapshots(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func saveMeta(tx *sql.Tx) error {
	sslJSON, _ := json.Marshal(AppConfig.SSL)
	sslCertificatesJSON, _ := json.Marshal(AppConfig.SSLCertificates)
	publicIPv4PoolJSON, _ := json.Marshal(AppConfig.PublicIPv4Pool)
	publicIPv6PrefixesJSON, _ := json.Marshal(AppConfig.PublicIPv6Prefixes)
	webSSHAllowedOriginsJSON, _ := json.Marshal(AppConfig.WebSSHAllowedOrigins)
	panelAccessPolicyJSON, _ := json.Marshal(AppConfig.PanelAccessPolicy)
	storagePoolsJSON, _ := json.Marshal(AppConfig.StoragePools)
	customKVMImagesJSON, _ := json.Marshal(AppConfig.CustomKVMImages)
	customLXCImagesJSON, _ := json.Marshal(AppConfig.CustomLXCImages)
	policyRulesJSON, _ := json.Marshal(AppConfig.PolicyRules)
	policyHistoryJSON, _ := json.Marshal(AppConfig.PolicyHistory)
	// F7/P2-11：落库前对节点 Token 副本做 AES-GCM 加密（内存态不改动，
	// 业务层心跳校验/agent 转发仍用明文）。加密失败时拒绝落库——静默
	// 落明文等于关掉该保护。install_key 同为密钥（一次性、24h TTL），
	// 一并加密：短时效降低了泄露窗口，但落库明文仍是不必要的暴露面。
	nodesForDisk := make([]Node, len(AppConfig.Nodes))
	for i, n := range AppConfig.Nodes {
		nodesForDisk[i] = n
		enc, err := EncryptNodeToken(n.Token)
		if err != nil {
			return fmt.Errorf("加密节点 %s Token 失败: %w", n.ID, err)
		}
		nodesForDisk[i].Token = enc
		encKey, err := EncryptNodeToken(n.InstallKey)
		if err != nil {
			return fmt.Errorf("加密节点 %s install_key 失败: %w", n.ID, err)
		}
		nodesForDisk[i].InstallKey = encKey
	}
	nodesJSON, _ := json.Marshal(nodesForDisk)
	regionsJSON, _ := json.Marshal(AppConfig.Regions)
	ipGroupsJSON, _ := json.Marshal(AppConfig.IPGroups)
	isoFilesJSON, _ := json.Marshal(AppConfig.ISOFiles)
	nCIbackup, _ := json.Marshal(AppConfig.AdminBackupCodes)
	backupSettingsJSON, _ := json.Marshal(AppConfig.BackupSettings)
	instanceBackupSettingsJSON, _ := json.Marshal(AppConfig.InstanceBackupSettings)
	remoteBackupSettingsJSON, _ := json.Marshal(AppConfig.RemoteBackupSettings)
	smtpSettingsJSON, _ := json.Marshal(AppConfig.SMTPSettings)
	backupsJSON, _ := json.Marshal(AppConfig.Backups)
	instanceBackupsJSON, _ := json.Marshal(AppConfig.InstanceBackups)
	backupPlansJSON, _ := json.Marshal(AppConfig.BackupPlans)
	rateLimitJSON, _ := json.Marshal(AppConfig.APIRateLimit)
	tenantsJSON, _ := json.Marshal(AppConfig.Tenants)
	adminsJSON, _ := json.Marshal(AppConfig.Admins)
	ksmTuningJSON, _ := json.Marshal(AppConfig.KSMTuning)
	// TurnstileSecretKey 与节点 Token 同级敏感：AES-GCM 密文落库，内存态明文。
	// SiteKey 本身公开（前端渲染 widget 需要），无需加密。
	turnstileSecret := AppConfig.TurnstileSecretKey
	if turnstileSecret != "" {
		encTS, err := EncryptNodeToken(turnstileSecret)
		if err != nil {
			return fmt.Errorf("加密 Turnstile SecretKey 失败: %w", err)
		}
		turnstileSecret = encTS
	}
	// 节点对接密钥同上：一次性、24h TTL，密文落库减小泄露窗口。
	agentPairingKey := AppConfig.AgentPairingKey
	if agentPairingKey != "" {
		encPK, err := EncryptNodeToken(agentPairingKey)
		if err != nil {
			return fmt.Errorf("加密 Agent PairingKey 失败: %w", err)
		}
		agentPairingKey = encPK
	}
	// 更新源：token 加密；其余字段明文（非敏感）。
	us := NormalizeUpdateSource(AppConfig.UpdateSource)
	usToken := ""
	if us.Token != "" {
		encUT, err := EncryptNodeToken(us.Token)
		if err != nil {
			return fmt.Errorf("加密 UpdateSource Token 失败: %w", err)
		}
		usToken = encUT
	}
	values := map[string]string{
		"admin_user":             AppConfig.AdminUser,
		"admin_pass_hash":        AppConfig.AdminPassHash,
		"admin_totp_secret":      AppConfig.AdminTOTPSecret,
		"admin_totp_enabled":     btoa(AppConfig.AdminTOTPEnabled),
		"admin_backup_codes":     string(nCIbackup),
		"admins":                 string(adminsJSON),
		"admin_path":             AppConfig.AdminPath,
		"jwt_secret":             AppConfig.JWTSecret,
		"port":                   strconv.Itoa(AppConfig.Port),
		"data_dir":               AppConfig.DataDir,
		"next_container_id":      strconv.Itoa(AppConfig.NextContainerID),
		"next_vnc_port":          strconv.Itoa(AppConfig.NextVNCPort),
		"next_ssh_port":          strconv.Itoa(AppConfig.NextSSHPort),
		"nat_port_start":         strconv.Itoa(AppConfig.NATPortStart),
		"nat_port_end":           strconv.Itoa(AppConfig.NATPortEnd),
		"lxc_nat_subnet":         AppConfig.LXCNATSubnet,
		"kvm_nat_subnet":         AppConfig.KVMNATSubnet,
		"setup_complete":         btoa(AppConfig.SetupComplete),
		"security_auto_shutdown": btoa(AppConfig.SecurityAutoShutdown),
		"arp_protection_enabled": btoa(AppConfig.ARPProtectionEnabled),
		"ip_anti_spoof_enabled":  btoa(AppConfig.IPAntiSpoofEnabled),
		"abuse_detection_enabled": btoa(AppConfig.AbuseDetectionEnabled),
		"task_concurrency":       strconv.Itoa(AppConfig.TaskConcurrency),
		"language":               NormalizeLanguage(AppConfig.Language),
		"login_footer_text":      AppConfig.LoginFooterText,
		"login_footer_hidden":    btoa(AppConfig.LoginFooterHidden),
		"panel_domain":           AppConfig.PanelDomain,
		"turnstile_site_key":     AppConfig.TurnstileSiteKey,
		"turnstile_secret_key":   turnstileSecret,
		"turnstile_admin_login":  btoa(AppConfig.TurnstileAdminLogin),
		"turnstile_user_login":   btoa(AppConfig.TurnstileUserLogin),
		"agent_pairing_key":      agentPairingKey,
		"agent_pairing_key_expiry": AppConfig.AgentPairingKeyExpiry,
		// 更新源：platform/owner/repo/branch/asset_prefix 明文；token 加密。
		"update_source_platform":    us.Platform,
		"update_source_owner":       us.Owner,
		"update_source_repo":        us.Repo,
		"update_source_branch":      us.Branch,
		"update_source_asset_prefix": us.AssetPrefix,
		"update_source_token":       usToken, // 已加密（空 token → 空）。
		"ssl":                    string(sslJSON),
		"ssl_certificates":       string(sslCertificatesJSON),
		"public_ipv4_pool":       string(publicIPv4PoolJSON),
		"public_ipv6_prefixes":   string(publicIPv6PrefixesJSON),
		"webssh_allowed_origins": string(webSSHAllowedOriginsJSON),
		"panel_access_policy":    string(panelAccessPolicyJSON),
		"storage_pools":          string(storagePoolsJSON),
		"custom_kvm_images":      string(customKVMImagesJSON),
		"custom_lxc_images":      string(customLXCImagesJSON),
		"policy_rules":           string(policyRulesJSON),
		"policy_history":         string(policyHistoryJSON),
		"nodes":                  string(nodesJSON),
		"regions":                string(regionsJSON),
		"ip_groups":              string(ipGroupsJSON),
		"iso_files":              string(isoFilesJSON),
		"metric_retention_days":  strconv.Itoa(AppConfig.MetricRetentionDays),
		"audit_retention_days":   strconv.Itoa(AppConfig.AuditRetentionDays),
		"backup_settings":        string(backupSettingsJSON),
		"instance_backup_settings": string(instanceBackupSettingsJSON),
		"remote_backup_settings":   string(remoteBackupSettingsJSON),
		"smtp_settings":           string(smtpSettingsJSON),
		"backups":                string(backupsJSON),
		"instance_backups":       string(instanceBackupsJSON),
		"backup_plans":           string(backupPlansJSON),
		"api_rate_limit":          string(rateLimitJSON),
		"tenants":                 string(tenantsJSON),
		"memory_overcommit_enabled": btoa(AppConfig.MemoryOvercommitEnabled),
		"memory_overcommit_ratio":   strconv.FormatFloat(AppConfig.MemoryOvercommitRatio, 'f', -1, 64),
		"nat_subnet_oversubscription": btoa(AppConfig.NATSubnetOversubscription),
		"disk_overcommit_ratio":       strconv.FormatFloat(AppConfig.DiskOvercommitRatio, 'f', -1, 64),
		"ksm_tuning":               string(ksmTuningJSON),
		"schema_version":          "1",
		"updated_at":             time.Now().Format("2006-01-02 15:04:05"),
	}
	for k, v := range values {
		if _, err := tx.Exec("INSERT INTO app_meta(key, value) VALUES (?, ?)", k, v); err != nil {
			return err
		}
	}
	return nil
}

func saveContainers(tx *sql.Tx) error {
	for _, c := range AppConfig.Containers {
		NormalizeContainerResourceAliases(&c)
		allowedImageIDs := encodeStringSlice(c.AllowedImageIDs)
		if _, err := tx.Exec(`INSERT INTO containers (
			id, uuid, name, virtualization, lxc_name, kvm_name, disk_image, storage_pool_id, storage_path, mac_address, template,
			vcpu, ram_mb, disk_gb, network_bw_mbps, network_down_mbps, network_up_mbps,
			monthly_traffic_gb, traffic_mode, traffic_in_gb,
			traffic_out_gb, traffic_used_rx, traffic_used_tx, traffic_reset_date,
			io_speed_mbps, io_read_mbps, io_write_mbps,
			status, restore_on_host_boot, ip, lan_ipv4_mode, lan_interface, lan_ipv4_address, lan_ipv4_prefix_len, lan_ipv4_gateway,
			ipv6, ipv6_prefix_len, ipv6_interface, vnc_port, ssh_port, ssh_password,
			ssh_host_key, port_mapping_limit, snapshot_limit, created_at, expires_at,
			snapshot_schedule_enabled, snapshot_schedule_interval_hours, snapshot_schedule_time,
			snapshot_schedule_last_run, snapshot_schedule_next_run, snapshot_schedule_created_by,
			policy_blocked, policy_blocked_reason, policy_blocked_at,
			firewall_enabled, firewall_default_action, firewall_rules, allowed_image_ids, image_limit_configured,
			tenant, cloud_init_user_data, data_disk_gb, data_disk_mount_path,
			rescue_enabled, rescue_iso_id, rescue_iso_path, optional_iso_id, optional_iso_path, root_volume_id, data_volume_ids,
			suspended, suspended_at, suspended_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? )`,
			c.ID, c.UUID, c.Name, c.Virtualization, c.LXCName, c.KVMName, c.DiskImage, c.StoragePoolID, c.StoragePath, c.MACAddress, c.Template,
			c.VCPU, c.RAMMB, c.DiskGB, c.NetworkBWMbps, c.NetworkDownMbps, c.NetworkUpMbps,
			c.MonthlyTrafficGB, c.TrafficMode, c.TrafficInGB,
			c.TrafficOutGB, c.TrafficUsedRX, c.TrafficUsedTX, c.TrafficResetDate,
			c.IOSpeedMBps, c.IOReadMBps, c.IOWriteMBps,
			c.Status, boolInt(c.RestoreOnHostBoot), c.IP, c.LANIPv4Mode, c.LANInterface, c.LANIPv4Address, c.LANIPv4PrefixLen, c.LANIPv4Gateway,
			c.IPv6, c.IPv6PrefixLen, c.IPv6Interface, c.VNCPort, c.SSHPort, c.SSHPassword,
			c.SSHHostKey, c.PortMappingLimit, c.SnapshotLimit, c.CreatedAt, c.ExpiresAt,
			boolInt(c.SnapshotScheduleEnabled), c.SnapshotScheduleIntervalHours, c.SnapshotScheduleTime,
			c.SnapshotScheduleLastRun, c.SnapshotScheduleNextRun, c.SnapshotScheduleCreatedBy,
			boolInt(c.PolicyBlocked), c.PolicyBlockedReason, c.PolicyBlockedAt,
			boolInt(c.FirewallEnabled), normalizeFirewallDefaultAction(c.FirewallDefaultAction), marshalFirewallRules(c.FirewallRules), allowedImageIDs, boolInt(c.ImageLimitConfigured),
			c.Tenant, c.CloudInitUserData, c.DataDiskGB, c.DataDiskMountPath,
			boolInt(c.RescueEnabled), c.RescueISOID, c.RescueISOPath,
			c.OptionalISOID, c.OptionalISOPath,
			c.RootVolumeID, encodeStringSlice(c.DataVolumeIDs),
			boolInt(c.Suspended), c.SuspendedAt, c.SuspendedReason,
		); err != nil {
			return err
		}
		for i, pm := range c.PortMappings {
			if _, err := tx.Exec(`INSERT INTO port_mappings(container_id, position, container_port, host_port, host_ip, protocol, description)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, i, pm.ContainerPort, pm.HostPort, pm.HostIP, pm.Protocol, pm.Description); err != nil {
				return err
			}
		}
		for i, ip := range c.PublicIPv4s {
			if _, err := tx.Exec(`INSERT INTO container_public_ipv4s(container_id, position, address, interface, prefix_len, gateway, rdns)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, i, ip.Address, ip.Interface, ip.PrefixLen, ip.Gateway, ip.RDNS); err != nil {
				return err
			}
		}
		for i, ip := range c.IPv6Addresses {
			if _, err := tx.Exec(`INSERT INTO container_ipv6_addresses(container_id, position, address, prefix_len, interface, rdns)
				VALUES (?, ?, ?, ?, ?, ?)`, c.ID, i, ip.Address, ip.PrefixLen, ip.Interface, ip.RDNS); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveSubUsers(tx *sql.Tx) error {
	for _, su := range AppConfig.SubUsers {
		allowedImageIDs := encodeStringSlice(su.AllowedImageIDs)
		if _, err := tx.Exec(`INSERT INTO sub_users(id, username, password, pass_hash, access_code, created_at, token_version, allowed_image_ids, image_limit_configured, role, tenant)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, su.ID, su.Username, "", su.PassHash, su.AccessCode, su.CreatedAt, su.TokenVersion, allowedImageIDs, boolInt(su.ImageLimitConfigured), subUserRoleForStorage(su.Role), su.Tenant); err != nil {
			return err
		}
		for i, name := range su.ContainerNames {
			if _, err := tx.Exec(`INSERT INTO sub_user_container_names(sub_user_id, position, container_name) VALUES (?, ?, ?)`, su.ID, i, name); err != nil {
				return err
			}
		}
		for i, uuid := range su.ContainerUUIDs {
			if _, err := tx.Exec(`INSERT INTO sub_user_container_uuids(sub_user_id, position, container_uuid) VALUES (?, ?, ?)`, su.ID, i, uuid); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveAPIKeys(tx *sql.Tx) error {
	for _, k := range AppConfig.ApiKeys {
		scopes := encodeStringSlice(k.Scopes)
		containerUUIDs := encodeStringSlice(k.ContainerUUIDs)
		if _, err := tx.Exec(`INSERT INTO api_keys(id, name, key_hash, key_fingerprint, prefix, ip_whitelist, created_at, last_used, scopes, expires_at, disabled, container_uuids, last_used_ip)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.ID, k.Name, k.KeyHash, k.KeyFingerprint, k.Prefix, k.IPWhitelist, k.CreatedAt, k.LastUsed, scopes, k.ExpiresAt, boolInt(k.Disabled), containerUUIDs, k.LastUsedIP); err != nil {
			return err
		}
	}
	return nil
}

// SaveConntrackSnapshot stores raw conntrack lines for a container IP.
func SaveConntrackSnapshot(containerIP string, lines []string) {
	if db == nil || len(lines) == 0 || strings.TrimSpace(containerIP) == "" {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO security_conntrack_snapshots (container_ip, line, captured_at) VALUES (?, ?, ?)`)
	if err != nil {
		return
	}
	defer stmt.Close()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		stmt.Exec(containerIP, line, now)
	}
	tx.Commit()

	// Cleanup old snapshots (>1 hour)
	db.Exec(`DELETE FROM security_conntrack_snapshots WHERE captured_at < ?`,
		time.Now().Add(-1*time.Hour).Format("2006-01-02 15:04:05"))
}

// GetConntrackSnapshotLines returns stored conntrack lines for a container IP.
func GetConntrackSnapshotLines(containerIP string) []string {
	if db == nil || strings.TrimSpace(containerIP) == "" {
		return nil
	}
	rows, err := db.Query(
		`SELECT line FROM security_conntrack_snapshots WHERE container_ip = ? ORDER BY captured_at DESC LIMIT 200`,
		containerIP,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			lines = append(lines, line)
		}
	}
	return lines
}

// ---- P0-1 存储卷记录（volumes 表）----
// 卷记录独立于 saveConfigToDB 的全量快照流程：增删改即时落库，
// 保证容器创建中途崩溃后不丢失/不残留半状态。读取走即时查询，无内存副本，
// 因此也不会与 AppConfigMu 产生锁交互（dbMu 是叶子锁）。

// CreateVolumeRecord 写入一条卷记录（status=creating 阶段调用），ID 冲突显式报错。
func CreateVolumeRecord(v storage.Volume) error {
	v.ID = strings.TrimSpace(v.ID)
	if v.ID == "" {
		return fmt.Errorf("volume id is required")
	}
	if v.CreatedAt == "" {
		v.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("sqlite database is not initialized")
	}
	if volumeRecordExistsLocked(v.ID) {
		return fmt.Errorf("%w: %s", storage.ErrVolumeExists, v.ID)
	}
	_, err := db.Exec(`INSERT INTO volumes (id, pool_id, kind, size_mb, attached_to_container_id, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.PoolID, v.Kind, v.SizeMB, v.AttachedToContainerID, v.Status, v.CreatedAt)
	return err
}

// UpdateVolumeRecord 按 ID 全量更新卷记录（状态机流转、挂载绑定）。
func UpdateVolumeRecord(v storage.Volume) error {
	v.ID = strings.TrimSpace(v.ID)
	if v.ID == "" {
		return fmt.Errorf("volume id is required")
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("sqlite database is not initialized")
	}
	res, err := db.Exec(`UPDATE volumes SET pool_id = ?, kind = ?, size_mb = ?, attached_to_container_id = ?, status = ?
		WHERE id = ?`,
		v.PoolID, v.Kind, v.SizeMB, v.AttachedToContainerID, v.Status, v.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", storage.ErrVolumeNotFound, v.ID)
	}
	return nil
}

// DeleteVolumeRecord 删除卷记录（幂等：记录不存在时返回 nil）。
func DeleteVolumeRecord(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM volumes WHERE id = ?`, id)
	return err
}

// DeleteContainerVolumes 删除挂载在指定容器上的全部卷记录（根卷+数据卷），
// 返回被删除的卷 ID 列表，供调用方写审计日志。
func DeleteContainerVolumes(containerID int) []string {
	if containerID <= 0 {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id FROM volumes WHERE attached_to_container_id = ?`, containerID)
	if err != nil {
		return nil
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, err := db.Exec(`DELETE FROM volumes WHERE id = ?`, id); err != nil {
			return ids
		}
	}
	return ids
}

// GetVolume 按 ID 读取卷记录。数据库未初始化（单元测试环境）时返回不存在。
func GetVolume(id string) (storage.Volume, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return storage.Volume{}, false
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return storage.Volume{}, false
	}
	return getVolumeLocked(id)
}

// ListVolumes 返回全部卷记录（按创建时间升序）。
func ListVolumes() []storage.Volume {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, pool_id, kind, size_mb, attached_to_container_id, status, created_at
		FROM volumes ORDER BY created_at, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []storage.Volume{}
	for rows.Next() {
		var v storage.Volume
		if err := rows.Scan(&v.ID, &v.PoolID, &v.Kind, &v.SizeMB, &v.AttachedToContainerID, &v.Status, &v.CreatedAt); err != nil {
			return nil
		}
		result = append(result, v)
	}
	return result
}

func volumeRecordExistsLocked(id string) bool {
	_, ok := getVolumeLocked(id)
	return ok
}

func getVolumeLocked(id string) (storage.Volume, bool) {
	var v storage.Volume
	err := db.QueryRow(`SELECT id, pool_id, kind, size_mb, attached_to_container_id, status, created_at
		FROM volumes WHERE id = ?`, id).Scan(
		&v.ID, &v.PoolID, &v.Kind, &v.SizeMB, &v.AttachedToContainerID, &v.Status, &v.CreatedAt)
	if err != nil {
		return storage.Volume{}, false
	}
	return v, true
}

func saveAuditLogs(tx *sql.Tx) error {
	for _, log := range AppConfig.AuditLogs {
		successSet := 0
		success := 0
		if log.Success != nil {
			successSet = 1
			if *log.Success {
				success = 1
			}
		}
		if _, err := tx.Exec(`INSERT INTO audit_logs(time, action, target, detail, user, ip, user_agent, success_set, success, error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, log.Time, log.Action, log.Target, log.Detail, log.User, log.IP, log.UserAgent, successSet, success, log.Error); err != nil {
			return err
		}
	}
	return nil
}

func saveTasksDB(tx *sql.Tx) error {
	for _, task := range AppConfig.Tasks {
		cfg := parseSavedTaskConfig(task.Config)
		if _, err := tx.Exec(`INSERT INTO tasks(
			id, type, container_id, container_name, status, error, created_at, template_id, user, ip, user_agent,
			cfg_name, cfg_virtualization, cfg_template_id, cfg_vcpu, cfg_cpu_percent, cfg_ram_mb, cfg_disk_gb,
			cfg_network_bw_mbps, cfg_network_down_mbps, cfg_network_up_mbps,
			cfg_monthly_traffic_gb, cfg_traffic_mode, cfg_traffic_in_gb,
			cfg_traffic_out_gb, cfg_io_speed_mbps, cfg_io_read_mbps, cfg_io_write_mbps,
			cfg_management_port, cfg_port_mapping_count, cfg_assign_nat, cfg_lan_ipv4_mode, cfg_lan_interface,
			cfg_lan_ipv4_address, cfg_lan_ipv4_prefix_len, cfg_lan_ipv4_gateway, cfg_snapshot_limit,
			cfg_assign_ipv4, cfg_ipv4_count, cfg_public_ipv4s, cfg_assign_ipv6, cfg_ipv6_count, cfg_ipv6_addresses,
			cfg_ssh_auth_mode, cfg_ssh_password, cfg_ssh_public_key, cfg_allowed_image_ids, cfg_image_limit_configured, cfg_expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.ID, task.Type, task.ContainerID, task.ContainerName, task.Status, task.Error, task.CreatedAt, task.TemplateID, task.User, task.IP, task.UserAgent,
			cfg.Name, cfg.Virtualization, cfg.TemplateID, cfg.VCPU, cfg.CPUPercent, cfg.RAMMB, cfg.DiskGB,
			cfg.NetworkBWMbps, cfg.NetworkDownMbps, cfg.NetworkUpMbps,
			cfg.MonthlyTrafficGB, cfg.TrafficMode, cfg.TrafficInGB,
			cfg.TrafficOutGB, cfg.IOSpeedMBps, cfg.IOReadMBps, cfg.IOWriteMBps,
			cfg.ManagementPort, cfg.PortMappingCount, boolPtrInt(cfg.AssignNAT), cfg.LANIPv4Mode, cfg.LANInterface,
			cfg.LANIPv4Address, cfg.LANIPv4PrefixLen, cfg.LANIPv4Gateway, cfg.SnapshotLimit,
			boolInt(cfg.AssignIPv4), cfg.IPv4Count, encodeStringSlice(cfg.PublicIPv4s),
			boolInt(cfg.AssignIPv6), cfg.IPv6Count, encodeStringSlice(cfg.IPv6Addresses),
			cfg.SSHAuthMode, cfg.SSHPassword, cfg.SSHPublicKey, encodeStringSlice(cfg.AllowedImageIDs), boolInt(cfg.ImageLimitConfigured), cfg.ExpiresAt,
		); err != nil {
			return err
		}
		for i, port := range cfg.ExtraPorts {
			if _, err := tx.Exec(`INSERT INTO task_extra_ports(task_id, position, port) VALUES (?, ?, ?)`, task.ID, i, port); err != nil {
				return err
			}
		}
		for i, mapping := range cfg.NATPortMappings {
			if _, err := tx.Exec(`INSERT INTO task_nat_port_mappings(task_id, position, host_port, container_port, protocol, description)
				VALUES (?, ?, ?, ?, ?, ?)`,
				task.ID, i, mapping.HostPort, mapping.ContainerPort, mapping.Protocol, mapping.Description,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveLoginLogs(tx *sql.Tx) error {
	for _, log := range AppConfig.LoginLogs {
		if _, err := tx.Exec(`INSERT INTO login_logs(time, username, ip, user_agent, success) VALUES (?, ?, ?, ?, ?)`,
			log.Time, log.Username, log.IP, log.UserAgent, boolInt(log.Success)); err != nil {
			return err
		}
	}
	return nil
}

func saveEnabledImages(tx *sql.Tx) error {
	for i, id := range AppConfig.EnabledImages {
		if _, err := tx.Exec(`INSERT INTO enabled_images(position, image_id) VALUES (?, ?)`, i, id); err != nil {
			return err
		}
	}
	return nil
}

func saveSnapshots(tx *sql.Tx) error {
	for _, snapshot := range AppConfig.Snapshots {
		if _, err := tx.Exec(`INSERT INTO snapshots(id, container_id, container_name, lxc_name, created_at, created_by, scheduled, path, size_bytes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshot.ID, snapshot.ContainerID, snapshot.ContainerName, snapshot.LXCName, snapshot.CreatedAt, snapshot.CreatedBy, boolInt(snapshot.Scheduled), snapshot.Path, snapshot.SizeBytes); err != nil {
			return err
		}
	}
	return nil
}

func loadContainers() ([]Container, error) {
	rows, err := db.Query(`SELECT
		id, uuid, name, virtualization, lxc_name, kvm_name, disk_image, storage_pool_id, storage_path, mac_address, template,
		vcpu, ram_mb, disk_gb, network_bw_mbps, network_down_mbps, network_up_mbps,
		monthly_traffic_gb, traffic_mode, traffic_in_gb,
		traffic_out_gb, traffic_used_rx, traffic_used_tx, traffic_reset_date,
		io_speed_mbps, io_read_mbps, io_write_mbps,
		status, restore_on_host_boot, ip, lan_ipv4_mode, lan_interface, lan_ipv4_address, lan_ipv4_prefix_len, lan_ipv4_gateway,
		ipv6, ipv6_prefix_len, ipv6_interface, vnc_port, ssh_port, ssh_password,
		ssh_host_key, port_mapping_limit, snapshot_limit, created_at, expires_at,
		snapshot_schedule_enabled, snapshot_schedule_interval_hours, snapshot_schedule_time,
		snapshot_schedule_last_run, snapshot_schedule_next_run, snapshot_schedule_created_by,
		policy_blocked, policy_blocked_reason, policy_blocked_at,
		firewall_enabled, firewall_default_action, firewall_rules, allowed_image_ids, image_limit_configured,
		tenant, cloud_init_user_data, data_disk_gb, data_disk_mount_path,
		rescue_enabled, rescue_iso_id, rescue_iso_path, optional_iso_id, optional_iso_path, root_volume_id, data_volume_ids,
		suspended, suspended_at, suspended_reason
		FROM containers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Container{}
	for rows.Next() {
		var c Container
		var scheduleEnabled, policyBlocked, firewallEnabled, imageLimitConfigured, restoreOnHostBoot int
		var firewallDefaultAction string
		var firewallRulesJSON, allowedImageIDs sql.NullString
		var storagePoolID, storagePath sql.NullString
		var lanIPv4Mode, lanInterface sql.NullString
		var lanIPv4Address, lanIPv4Gateway sql.NullString
		var lanIPv4PrefixLen sql.NullInt64
		var tenant sql.NullString
		var cloudInitUserData sql.NullString
		var dataDiskMountPath sql.NullString
		var rescueEnabled int
		var rescueISOID, rescueISOPath sql.NullString
		var optionalISOID, optionalISOPath sql.NullString
		var rootVolumeID, dataVolumeIDs sql.NullString
		var suspended int
		var suspendedAt, suspendedReason sql.NullString
		if err := rows.Scan(
			&c.ID, &c.UUID, &c.Name, &c.Virtualization, &c.LXCName, &c.KVMName, &c.DiskImage, &storagePoolID, &storagePath, &c.MACAddress, &c.Template,
			&c.VCPU, &c.RAMMB, &c.DiskGB, &c.NetworkBWMbps, &c.NetworkDownMbps, &c.NetworkUpMbps,
			&c.MonthlyTrafficGB, &c.TrafficMode, &c.TrafficInGB,
			&c.TrafficOutGB, &c.TrafficUsedRX, &c.TrafficUsedTX, &c.TrafficResetDate,
			&c.IOSpeedMBps, &c.IOReadMBps, &c.IOWriteMBps,
			&c.Status, &restoreOnHostBoot, &c.IP, &lanIPv4Mode, &lanInterface, &lanIPv4Address, &lanIPv4PrefixLen, &lanIPv4Gateway,
			&c.IPv6, &c.IPv6PrefixLen, &c.IPv6Interface, &c.VNCPort, &c.SSHPort, &c.SSHPassword,
			&c.SSHHostKey, &c.PortMappingLimit, &c.SnapshotLimit, &c.CreatedAt, &c.ExpiresAt,
			&scheduleEnabled, &c.SnapshotScheduleIntervalHours, &c.SnapshotScheduleTime,
			&c.SnapshotScheduleLastRun, &c.SnapshotScheduleNextRun, &c.SnapshotScheduleCreatedBy,
			&policyBlocked, &c.PolicyBlockedReason, &c.PolicyBlockedAt,
			&firewallEnabled, &firewallDefaultAction, &firewallRulesJSON, &allowedImageIDs, &imageLimitConfigured,
			&tenant, &cloudInitUserData, &c.DataDiskGB, &dataDiskMountPath,
			&rescueEnabled, &rescueISOID, &rescueISOPath,
			&optionalISOID, &optionalISOPath,
			&rootVolumeID, &dataVolumeIDs,
			&suspended, &suspendedAt, &suspendedReason,
		); err != nil {
			return nil, err
		}
		c.CloudInitUserData = cloudInitUserData.String
		c.DataDiskMountPath = dataDiskMountPath.String
		c.RescueEnabled = rescueEnabled != 0
		c.RescueISOID = rescueISOID.String
		c.RescueISOPath = rescueISOPath.String
		c.OptionalISOID = optionalISOID.String
		c.OptionalISOPath = optionalISOPath.String
		c.RootVolumeID = rootVolumeID.String
		c.DataVolumeIDs = decodeStringSlice(dataVolumeIDs.String)
		c.Suspended = suspended != 0
		c.SuspendedAt = suspendedAt.String
		c.SuspendedReason = suspendedReason.String
		c.Tenant = tenant.String
		c.StoragePoolID = storagePoolID.String
		c.StoragePath = storagePath.String
		c.LANIPv4Mode = lanIPv4Mode.String
		c.LANInterface = lanInterface.String
		c.LANIPv4Address = lanIPv4Address.String
		if lanIPv4PrefixLen.Valid {
			c.LANIPv4PrefixLen = int(lanIPv4PrefixLen.Int64)
		}
		c.LANIPv4Gateway = lanIPv4Gateway.String
		c.SnapshotScheduleEnabled = scheduleEnabled != 0
		c.RestoreOnHostBoot = restoreOnHostBoot != 0
		c.PolicyBlocked = policyBlocked != 0
		c.FirewallEnabled = firewallEnabled != 0
		c.FirewallDefaultAction = normalizeFirewallDefaultAction(firewallDefaultAction)
		c.ImageLimitConfigured = imageLimitConfigured != 0
		if firewallRulesJSON.Valid && strings.TrimSpace(firewallRulesJSON.String) != "" {
			_ = json.Unmarshal([]byte(firewallRulesJSON.String), &c.FirewallRules)
		}
		c.AllowedImageIDs = decodeStringSlice(allowedImageIDs.String)
		NormalizeContainerResourceAliases(&c)
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range result {
		result[i].PortMappings, err = loadPortMappings(result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].PublicIPv4s, err = loadContainerPublicIPv4s(result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].IPv6Addresses, err = loadContainerIPv6Addresses(result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].NormalizeNetworkAssignments()
	}
	return result, nil
}

func loadPortMappings(containerID int) ([]PortMapping, error) {
	rows, err := db.Query(`SELECT container_port, host_port, host_ip, protocol, description FROM port_mappings WHERE container_id = ? ORDER BY position`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PortMapping{}
	for rows.Next() {
		var pm PortMapping
		var hostIP sql.NullString
		if err := rows.Scan(&pm.ContainerPort, &pm.HostPort, &hostIP, &pm.Protocol, &pm.Description); err != nil {
			return nil, err
		}
		pm.HostIP = hostIP.String
		result = append(result, pm)
	}
	return result, rows.Err()
}

func loadContainerPublicIPv4s(containerID int) ([]PublicIPv4Assignment, error) {
	rows, err := db.Query(`SELECT address, interface, prefix_len, gateway, rdns FROM container_public_ipv4s WHERE container_id = ? ORDER BY position`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PublicIPv4Assignment{}
	for rows.Next() {
		var item PublicIPv4Assignment
		var iface sql.NullString
		var prefixLen sql.NullInt64
		var gateway sql.NullString
		var rdns sql.NullString
		if err := rows.Scan(&item.Address, &iface, &prefixLen, &gateway, &rdns); err != nil {
			return nil, err
		}
		item.Interface = iface.String
		if prefixLen.Valid {
			item.PrefixLen = int(prefixLen.Int64)
		}
		item.Gateway = gateway.String
		item.RDNS = rdns.String
		result = append(result, item)
	}
	return result, rows.Err()
}

func loadContainerIPv6Addresses(containerID int) ([]IPv6Assignment, error) {
	rows, err := db.Query(`SELECT address, prefix_len, interface, rdns FROM container_ipv6_addresses WHERE container_id = ? ORDER BY position`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []IPv6Assignment{}
	for rows.Next() {
		var item IPv6Assignment
		var prefixLen sql.NullInt64
		var iface sql.NullString
		var rdns sql.NullString
		if err := rows.Scan(&item.Address, &prefixLen, &iface, &rdns); err != nil {
			return nil, err
		}
		if prefixLen.Valid {
			item.PrefixLen = int(prefixLen.Int64)
		}
		item.Interface = iface.String
		item.RDNS = rdns.String
		result = append(result, item)
	}
	return result, rows.Err()
}

func loadSubUsers() ([]SubUser, error) {
	rows, err := db.Query(`SELECT id, username, password, pass_hash, access_code, created_at, token_version, allowed_image_ids, image_limit_configured, role, tenant FROM sub_users ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SubUser{}
	for rows.Next() {
		var su SubUser
		var allowedImageIDs sql.NullString
		var imageLimitConfigured int
		if err := rows.Scan(&su.ID, &su.Username, &su.Password, &su.PassHash, &su.AccessCode, &su.CreatedAt, &su.TokenVersion, &allowedImageIDs, &imageLimitConfigured, &su.Role, &su.Tenant); err != nil {
			return nil, err
		}
		su.AllowedImageIDs = decodeStringSlice(allowedImageIDs.String)
		su.ImageLimitConfigured = imageLimitConfigured != 0
		// 明文口令不以持久化凭据为准：既有库中可能残留的历史明文一律清空，
		// 仅保留 bcrypt pass_hash 用于登录校验，降低 DB 泄露面。
		su.Password = ""
		result = append(result, su)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range result {
		result[i].ContainerNames, err = loadStringList("sub_user_container_names", "container_name", "sub_user_id", result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].ContainerUUIDs, err = loadStringList("sub_user_container_uuids", "container_uuid", "sub_user_id", result[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func loadStringList(table, valueColumn, keyColumn, key string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %s WHERE %s = ? ORDER BY position`, valueColumn, table, keyColumn), key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func loadAPIKeys() ([]ApiKeyConfig, error) {
	rows, err := db.Query(`SELECT id, name, key_hash, key_fingerprint, prefix, ip_whitelist, created_at, last_used, scopes, expires_at, disabled, container_uuids, last_used_ip FROM api_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ApiKeyConfig{}
	for rows.Next() {
		var k ApiKeyConfig
		var keyFingerprint, scopes, expiresAt, containerUUIDs, lastUsedIP sql.NullString
		var disabled sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &keyFingerprint, &k.Prefix, &k.IPWhitelist, &k.CreatedAt, &k.LastUsed, &scopes, &expiresAt, &disabled, &containerUUIDs, &lastUsedIP); err != nil {
			return nil, err
		}
		k.KeyFingerprint = keyFingerprint.String
		k.Scopes = decodeStringSlice(scopes.String)
		k.ExpiresAt = expiresAt.String
		k.Disabled = disabled.Valid && disabled.Int64 != 0
		k.ContainerUUIDs = decodeStringSlice(containerUUIDs.String)
		k.LastUsedIP = lastUsedIP.String
		result = append(result, k)
	}
	return result, rows.Err()
}

func loadAuditLogs() ([]AuditLog, error) {
	rows, err := db.Query(`SELECT time, action, target, detail, user, ip, user_agent, success_set, success, error FROM audit_logs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuditLog{}
	for rows.Next() {
		var log AuditLog
		var successSet, success int
		if err := rows.Scan(&log.Time, &log.Action, &log.Target, &log.Detail, &log.User, &log.IP, &log.UserAgent, &successSet, &success, &log.Error); err != nil {
			return nil, err
		}
		if successSet != 0 {
			value := success != 0
			log.Success = &value
		}
		result = append(result, log)
	}
	return result, rows.Err()
}

func loadTasks() ([]SavedTask, error) {
	rows, err := db.Query(`SELECT
		id, type, container_id, container_name, status, error, created_at, template_id, user, ip, user_agent,
		cfg_name, cfg_virtualization, cfg_template_id, cfg_vcpu, cfg_cpu_percent, cfg_ram_mb, cfg_disk_gb,
		cfg_network_bw_mbps, cfg_network_down_mbps, cfg_network_up_mbps,
		cfg_monthly_traffic_gb, cfg_traffic_mode, cfg_traffic_in_gb,
		cfg_traffic_out_gb, cfg_io_speed_mbps, cfg_io_read_mbps, cfg_io_write_mbps,
		cfg_management_port, cfg_port_mapping_count, cfg_assign_nat, cfg_lan_ipv4_mode, cfg_lan_interface,
		cfg_lan_ipv4_address, cfg_lan_ipv4_prefix_len, cfg_lan_ipv4_gateway, cfg_snapshot_limit,
		cfg_assign_ipv4, cfg_ipv4_count, cfg_public_ipv4s, cfg_assign_ipv6, cfg_ipv6_count, cfg_ipv6_addresses,
		cfg_ssh_auth_mode, cfg_ssh_password, cfg_ssh_public_key, cfg_allowed_image_ids, cfg_image_limit_configured, cfg_expires_at
		FROM tasks ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SavedTask{}
	configs := []savedTaskConfig{}
	for rows.Next() {
		var t SavedTask
		var cfg savedTaskConfig
		var assignIPv4, assignIPv6, imageLimitConfigured int
		var ip, userAgent, publicIPv4s, ipv6Addresses sql.NullString
		var lanIPv4Mode, lanInterface, lanIPv4Address, lanIPv4Gateway, sshAuthMode, sshPassword, sshPublicKey, allowedImageIDs sql.NullString
		var assignNAT, lanIPv4PrefixLen, ipv4Count, ipv6Count sql.NullInt64
		if err := rows.Scan(
			&t.ID, &t.Type, &t.ContainerID, &t.ContainerName, &t.Status, &t.Error, &t.CreatedAt, &t.TemplateID, &t.User, &ip, &userAgent,
			&cfg.Name, &cfg.Virtualization, &cfg.TemplateID, &cfg.VCPU, &cfg.CPUPercent, &cfg.RAMMB, &cfg.DiskGB,
			&cfg.NetworkBWMbps, &cfg.NetworkDownMbps, &cfg.NetworkUpMbps,
			&cfg.MonthlyTrafficGB, &cfg.TrafficMode, &cfg.TrafficInGB,
			&cfg.TrafficOutGB, &cfg.IOSpeedMBps, &cfg.IOReadMBps, &cfg.IOWriteMBps,
			&cfg.ManagementPort, &cfg.PortMappingCount, &assignNAT, &lanIPv4Mode, &lanInterface,
			&lanIPv4Address, &lanIPv4PrefixLen, &lanIPv4Gateway, &cfg.SnapshotLimit,
			&assignIPv4, &ipv4Count, &publicIPv4s, &assignIPv6, &ipv6Count, &ipv6Addresses,
			&sshAuthMode, &sshPassword, &sshPublicKey, &allowedImageIDs, &imageLimitConfigured, &cfg.ExpiresAt,
		); err != nil {
			return nil, err
		}
		t.IP = ip.String
		t.UserAgent = userAgent.String
		if assignNAT.Valid {
			value := assignNAT.Int64 != 0
			cfg.AssignNAT = &value
		}
		cfg.LANIPv4Mode = lanIPv4Mode.String
		cfg.LANInterface = lanInterface.String
		cfg.LANIPv4Address = lanIPv4Address.String
		if lanIPv4PrefixLen.Valid {
			cfg.LANIPv4PrefixLen = int(lanIPv4PrefixLen.Int64)
		}
		cfg.LANIPv4Gateway = lanIPv4Gateway.String
		cfg.AssignIPv4 = assignIPv4 != 0
		if ipv4Count.Valid {
			cfg.IPv4Count = int(ipv4Count.Int64)
		}
		cfg.PublicIPv4s = decodeStringSlice(publicIPv4s.String)
		cfg.AssignIPv6 = assignIPv6 != 0
		if ipv6Count.Valid {
			cfg.IPv6Count = int(ipv6Count.Int64)
		}
		cfg.IPv6Addresses = decodeStringSlice(ipv6Addresses.String)
		cfg.SSHAuthMode = sshAuthMode.String
		cfg.SSHPassword = sshPassword.String
		cfg.SSHPublicKey = sshPublicKey.String
		cfg.AllowedImageIDs = decodeStringSlice(allowedImageIDs.String)
		cfg.ImageLimitConfigured = imageLimitConfigured != 0
		normalizeSavedTaskConfigLimits(&cfg)
		result = append(result, t)
		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range result {
		configs[i].ExtraPorts, err = loadTaskExtraPorts(result[i].ID)
		if err != nil {
			return nil, err
		}
		configs[i].NATPortMappings, err = loadTaskNATPortMappings(result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Config = encodeSavedTaskConfig(configs[i])
	}
	return result, nil
}

func loadTaskExtraPorts(taskID string) ([]int, error) {
	rows, err := db.Query(`SELECT port FROM task_extra_ports WHERE task_id = ? ORDER BY position`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []int{}
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, err
		}
		result = append(result, port)
	}
	return result, rows.Err()
}

func loadTaskNATPortMappings(taskID string) ([]PortMapping, error) {
	rows, err := db.Query(`SELECT host_port, container_port, protocol, description
		FROM task_nat_port_mappings WHERE task_id = ? ORDER BY position`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PortMapping{}
	for rows.Next() {
		var mapping PortMapping
		if err := rows.Scan(&mapping.HostPort, &mapping.ContainerPort, &mapping.Protocol, &mapping.Description); err != nil {
			return nil, err
		}
		result = append(result, mapping)
	}
	return result, rows.Err()
}

func loadLoginLogs() ([]SavedLoginLog, error) {
	rows, err := db.Query(`SELECT time, username, ip, user_agent, success FROM login_logs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SavedLoginLog{}
	for rows.Next() {
		var log SavedLoginLog
		var success int
		if err := rows.Scan(&log.Time, &log.Username, &log.IP, &log.UserAgent, &success); err != nil {
			return nil, err
		}
		log.Success = success != 0
		result = append(result, log)
	}
	return result, rows.Err()
}

func loadEnabledImages() ([]string, error) {
	rows, err := db.Query(`SELECT image_id FROM enabled_images ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func loadSnapshots() ([]Snapshot, error) {
	rows, err := db.Query(`SELECT id, container_id, container_name, lxc_name, created_at, created_by, scheduled, path, size_bytes FROM snapshots ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Snapshot{}
	for rows.Next() {
		var snapshot Snapshot
		var scheduled int
		if err := rows.Scan(&snapshot.ID, &snapshot.ContainerID, &snapshot.ContainerName, &snapshot.LXCName, &snapshot.CreatedAt, &snapshot.CreatedBy, &scheduled, &snapshot.Path, &snapshot.SizeBytes); err != nil {
			return nil, err
		}
		snapshot.Scheduled = scheduled != 0
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func loadLegacyJSONConfig(path string) (*EyvescloudConfig, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to read legacy config: %v", err)
	}
	// 预置默认值：滥用检测默认开启。旧 JSON 配置没有该字段时保持开启，
	// 若 JSON 中显式写了 false 则以 JSON 为准。
	cfg := &EyvescloudConfig{AbuseDetectionEnabled: true}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf("failed to parse legacy config: %v", err)
	}
	return cfg, true, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func marshalFirewallRules(rules []FirewallRule) interface{} {
	if len(rules) == 0 {
		return nil
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return nil
	}
	return string(data)
}

func normalizeFirewallDefaultAction(action string) string {
	action = strings.ToUpper(strings.TrimSpace(action))
	if action == "ACCEPT" {
		return "ACCEPT"
	}
	return "DROP"
}

func boolPtrInt(value *bool) interface{} {
	if value == nil {
		return nil
	}
	return boolInt(*value)
}

func btoa(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func atob(value string) bool {
	return value == "1" || strings.EqualFold(value, "true")
}

// atobDefault parses a boolean meta value, falling back to def when the key is
// absent or blank. Used for switches whose default is "on" so that existing
// databases (without the key) keep the feature enabled after an upgrade.
func atobDefault(meta map[string]string, key string, def bool) bool {
	raw, ok := meta[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return def
	}
	return atob(raw)
}

func atoi(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}

func atof(value string) float64 {
	n, _ := strconv.ParseFloat(value, 64)
	return n
}
