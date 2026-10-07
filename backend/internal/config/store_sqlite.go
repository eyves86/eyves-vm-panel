package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	// P1：env 门控 —— 设置 EYVESCLOUD_PG_DSN 时配置库走 Postgres（实验性），否则保持
	// SQLite（默认，行为不变）。PG 无本地库文件 / 无 WAL，故跳过 chmod 与遗留指标迁移；
	// 遥测库当前仍是本机 SQLite（P1-c），PG 下同样打开。
	if dsn := strings.TrimSpace(os.Getenv(pgDSNEnv)); dsn != "" {
		if err := openPostgresConfigDB(dsn); err != nil {
			return err
		}
		return openTelemetryDB()
	}
	configDBIsPostgres = false
	dbPath := getDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return fmt.Errorf("failed to create database directory: %v", err)
	}
	next, err := sql.Open("sqlite", sqliteDSN(dbPath, configDBPragmas))
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %v", err)
	}
	next.SetMaxOpenConns(1)
	next.SetMaxIdleConns(1)

	// 安全加固：SQLite 文件含 AdminPassHash / JWTSecret / ApiKeyHash 等敏感字段，
	// 无论是否新建，都把它严格锁到当前用户可读。
	// 注意 chmod 必须放在 ensureSchema 之后：sql.Open 是惰性的，文件要等到第一次
	// 执行语句时才落盘，提前 chmod 会打在尚不存在的路径上静默失败（首次启动即以
	// 0644 落盘，直到下次启动才被纠正）。
	db = next
	if err := ensureSchema(); err != nil {
		return err
	}
	_ = os.Chmod(dbPath, 0600)
	// 遥测独立成库（P1-c）：必须在 db 就绪之后打开，迁移会从旧的 config.db
	// 读取遗留指标行。openTelemetryDB 内部不再取 dbMu（此处已持有），锁序 dbMu → telemetryMu。
	if err := openTelemetryDB(); err != nil {
		return err
	}
	migrateLegacyMetrics()
	return nil
}

// sqliteSchemaStmts 是 SQLite 的基础建表语句。列集合必须与 postgresSchemaStmts 一致
// （TestSQLiteAndPostgresSchemaParity 守卫）；此后新增列统一走共享的
// ensureSchemaMigrations（PG 侧做 INTEGER→BIGINT / REAL→DOUBLE PRECISION 类型翻译），
// 两端各自动补齐。
func sqliteSchemaStmts() []string {
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
			suspended_reason TEXT,
			remark TEXT,
			locked INTEGER NOT NULL DEFAULT 0
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
		`CREATE TABLE IF NOT EXISTS container_access_links (
			container_uuid TEXT PRIMARY KEY,
			access_code TEXT,
			access_code_password TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sub_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			password TEXT,
			pass_hash TEXT,
			access_code TEXT,
			access_code_password TEXT,
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
		// 遥测表（container_metrics / container_metrics_hourly）已迁至独立的
		// telemetry.db，由 ensureTelemetrySchema 建表，见 telemetry_db.go。
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
		// P2：节点从 app_meta 的单行 JSON 改为**每节点一行**。此前 30k 节点下一次
		// 心跳保存要重新序列化 9.5MB JSON + 重加密全部节点 token（实测 220ms/次），
		// 而心跳只改一个节点。行级化后单次保存只写 1 行（见 store_rows.go 的 diffNodes）。
		// token / install_key 仍逐行 AES-GCM 加密落库，且只在所属节点行变化时才重新加密。
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			address TEXT NOT NULL DEFAULT '',
			public_host TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL DEFAULT '',
			install_key TEXT NOT NULL DEFAULT '',
			install_key_created_at TEXT NOT NULL DEFAULT '',
			install_key_ip TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			os_name TEXT NOT NULL DEFAULT '',
			cpu_count INTEGER NOT NULL DEFAULT 0,
			ram_total_mb INTEGER NOT NULL DEFAULT 0,
			ram_used_mb INTEGER NOT NULL DEFAULT 0,
			disk_total_gb REAL NOT NULL DEFAULT 0,
			disk_used_gb REAL NOT NULL DEFAULT 0,
			container_count INTEGER NOT NULL DEFAULT 0,
			region_id TEXT NOT NULL DEFAULT '',
			node_group_id TEXT NOT NULL DEFAULT '',
			cluster_id TEXT NOT NULL DEFAULT '',
			virt_types TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			maintenance_mode INTEGER NOT NULL DEFAULT 0,
			maintenance_since TEXT NOT NULL DEFAULT '',
			tls_skip_verify INTEGER NOT NULL DEFAULT 0,
			allow_private_addr INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_region ON nodes (region_id)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_node_group ON nodes (node_group_id)`,
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
	return stmts
}

func ensureSchema() error {
	for _, stmt := range sqliteSchemaStmts() {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create sqlite schema: %v", err)
		}
	}
	return ensureSchemaMigrations()
}

// schemaColumnMigration 描述一条「后加列」迁移：table.name 缺列时按 def 补列。
type schemaColumnMigration struct {
	table string
	name  string
	def   string
}

// schemaColumnMigrations 是两端共享的后加列清单：SQLite 与 Postgres 启动时都跑它。
// 故「两端最终列集合一致」等价于 sqliteBase ∪ M == postgresBase ∪ M —— PG 的基础建表
// 把历史列内联了，SQLite 的基础建表只保留最早的列，两者靠这份清单补齐到同一集合
// （TestSQLiteAndPostgresSchemaParity 正是按并集断言的）。
func schemaColumnMigrations() []schemaColumnMigration {
	return []schemaColumnMigration{
		{"api_keys", "scopes", "TEXT"},
		{"api_keys", "expires_at", "TEXT"},
		{"api_keys", "disabled", "INTEGER"},
		{"api_keys", "container_uuids", "TEXT"},
		{"api_keys", "last_used_ip", "TEXT"},
		{"api_keys", "key_fingerprint", "TEXT"},
		// v2 PATCH cpu_percent 支持（此前字段被静默丢弃，契约 bug 修复）。
		{"containers", "cpu_percent", "INTEGER NOT NULL DEFAULT 0"},
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
		// 访问码专用口令（加密落库）。存量行默认空，加载时自动回填随机口令。
		{"sub_users", "access_code_password", "TEXT NOT NULL DEFAULT ''"},
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
		// 实例备注与锁定（企业面板通用属性：备注用于运维标注，锁定后禁止删除/重装等破坏性操作）。
		{"containers", "remark", "TEXT"},
		{"containers", "locked", "INTEGER NOT NULL DEFAULT 0"},
		{"containers", "recycled_at", "TEXT"},
		// 节点容器归属持久化（此前不落库：面板重启后节点容器"变成本机"，
		// orphan 检测失配、代理调用找不到映射 —— 生产实测踩坑）。
		{"containers", "node_id", "TEXT"},
		// node_local_id：实例在所属被控上的本地 ID（代理调用用）。
		{"containers", "node_local_id", "INTEGER NOT NULL DEFAULT 0"},
	}
}

func ensureSchemaMigrations() error {
	added := map[string]bool{}
	for _, column := range schemaColumnMigrations() {
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
	// 这几条是「NULL → 默认值」的一次性回填，只对历史遗留行有意义。必须带 IS NULL
	// 谓词：否则每次启动都会改写整张表（10w 容器 = 每次启动 10w 行更新），而回填完成
	// 后它本就是空操作，白白制造写放大。谓词让首次回填后自然归零。
	if _, err := db.Exec(`UPDATE containers
		SET lan_ipv4_mode = COALESCE(lan_ipv4_mode, ''),
		    lan_interface = COALESCE(lan_interface, ''),
		    lan_ipv4_address = COALESCE(lan_ipv4_address, ''),
		    lan_ipv4_prefix_len = COALESCE(lan_ipv4_prefix_len, 0),
		    lan_ipv4_gateway = COALESCE(lan_ipv4_gateway, '')
		WHERE lan_ipv4_mode IS NULL OR lan_interface IS NULL OR lan_ipv4_address IS NULL
		   OR lan_ipv4_prefix_len IS NULL OR lan_ipv4_gateway IS NULL`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE containers
		SET storage_pool_id = COALESCE(storage_pool_id, ''),
		    storage_path = COALESCE(storage_path, '')
		WHERE storage_pool_id IS NULL OR storage_path IS NULL`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE tasks
		SET cfg_lan_ipv4_mode = COALESCE(cfg_lan_ipv4_mode, ''),
		    cfg_lan_interface = COALESCE(cfg_lan_interface, ''),
		    cfg_lan_ipv4_address = COALESCE(cfg_lan_ipv4_address, ''),
		    cfg_lan_ipv4_prefix_len = COALESCE(cfg_lan_ipv4_prefix_len, 0),
		    cfg_lan_ipv4_gateway = COALESCE(cfg_lan_ipv4_gateway, '')
		WHERE cfg_lan_ipv4_mode IS NULL OR cfg_lan_interface IS NULL OR cfg_lan_ipv4_address IS NULL
		   OR cfg_lan_ipv4_prefix_len IS NULL OR cfg_lan_ipv4_gateway IS NULL`); err != nil {
		return err
	}
	return nil
}

// ensureColumn 在列缺失时补列，返回是否真的新增。db 同一时刻只属于一个后端，
// 故按 configDBIsPostgres 分支选择列存在性判断的方言；PG 侧还需把建表列类型
// （SQLite 的 INTEGER/REAL）翻译成与 ensurePostgresSchema 一致的类型，否则
// ALTER 出来的列会是 int4/float4（traffic_used_rx 之类的字节计数会溢出 int4）。
func ensureColumn(table, name, def string) (bool, error) {
	exists, err := columnExists(table, name)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if configDBIsPostgres {
		def = pgColumnDef(def)
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + def)
	return err == nil, err
}

func columnExists(table, name string) (bool, error) {
	if configDBIsPostgres {
		var one int
		err := db.QueryRow(`SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			table, name).Scan(&one)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
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
			return true, nil
		}
	}
	return false, rows.Err()
}

// pgColumnDef 把迁移列表里的 SQLite 类型翻译成 Postgres 建表所用的类型。
func pgColumnDef(def string) string {
	def = strings.ReplaceAll(def, "INTEGER", "BIGINT")
	return strings.ReplaceAll(def, "REAL", "DOUBLE PRECISION")
}

func loadConfigFromDB() (*EyvescloudConfig, bool, error) {
	meta := map[string]string{}
	rows, err := db.Query("SELECT key, value FROM app_meta")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		// value 用 NullString 兜底：任一行的 value 为 NULL 时，
		// 直接 Scan 进 string 会报错并让整个配置加载失败（面板起不来）。
		var v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			log.Printf("Warning: skipping unreadable app_meta row (key=%q): %v", k, err)
			continue
		}
		meta[k] = v.String
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if meta["admin_user"] == "" {
		return nil, false, nil
	}

	cfg := &EyvescloudConfig{
		AdminUser:     meta["admin_user"],
		AdminPassHash: meta["admin_pass_hash"],
		// AdminTokenVersion 从库恢复（F-01 修复）：保证改密/吊销在重启后仍生效。
		AdminTokenVersion:    atoi(meta["admin_token_version"]),
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
		AbuseDetectionEnabled:     atobDefault(meta, "abuse_detection_enabled", true),
		TaskConcurrency:           atoi(meta["task_concurrency"]),
		TaskQueueMaxPending:       atoi(meta["task_queue_max_pending"]),
		Language:                  meta["language"],
		LoginFooterText:           meta["login_footer_text"],
		LoginFooterHidden:         atob(meta["login_footer_hidden"]),
		BrandName:                 meta["brand_name"],
		BrandLogo:                 meta["brand_logo"],
		BrandFavicon:              meta["brand_favicon"],
		BrandLoginTitle:           meta["brand_login_title"],
		BrandPoweredHidden:        atob(meta["brand_powered_hidden"]),
		PanelDomain:               meta["panel_domain"],
		TurnstileSiteKey:          meta["turnstile_site_key"],
		TurnstileSecretKey:        meta["turnstile_secret_key"],
		TurnstileAdminLogin:       atob(meta["turnstile_admin_login"]),
		TurnstileUserLogin:        atob(meta["turnstile_user_login"]),
		MetricRetentionDays:       atoi(meta["metric_retention_days"]),
		AuditRetentionDays:        atoi(meta["audit_retention_days"]),
		MemoryOvercommitEnabled:   atob(meta["memory_overcommit_enabled"]),
		MemoryOvercommitRatio:     atof(meta["memory_overcommit_ratio"]),
		NATSubnetOversubscription: atob(meta["nat_subnet_oversubscription"]),
		DiskOvercommitRatio:       atof(meta["disk_overcommit_ratio"]),
		// 节点对接密钥（本面板作为被控）：密文落库，读取后下方统一解密。
		AgentPairingKey:       meta["agent_pairing_key"],
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
		// password 为 at-rest 密文，需先解密再反序列化（存量明文原样通过）。
		if err := unmarshalDecryptingFields(raw, &cfg.SMTPSettings, []string{"password"}); err != nil {
			log.Printf("Warning: 解析已存 SMTP 设置失败（按空处理）: %v", err)
		}
	}
	// 企业集成 / 运维集合（此前完全未落库，重启即丢）。解析失败按空处理，
	// 不让一条损坏记录把整个面板拖得起不来。
	if raw := strings.TrimSpace(meta["ssh_keys"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.SSHKeys)
	}
	if raw := strings.TrimSpace(meta["webhooks"]); raw != "" {
		if err := unmarshalDecryptingFields(raw, &cfg.Webhooks, []string{"secret"}); err != nil {
			log.Printf("Warning: 解析已存 Webhook 失败（按空处理）: %v", err)
		}
	}
	if raw := strings.TrimSpace(meta["recipes"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Recipes)
	}
	if raw := strings.TrimSpace(meta["scheduled_actions"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.ScheduledActions)
	}
	if raw := strings.TrimSpace(meta["node_groups"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.NodeGroups)
	}
	if raw := strings.TrimSpace(meta["clusters"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.Clusters)
	}
	if raw := strings.TrimSpace(meta["notifications"]); raw != "" {
		if err := unmarshalDecryptingFields(raw, &cfg.Notifications, []string{"smtp_password"}); err != nil {
			log.Printf("Warning: 解析已存通知设置失败（按空处理）: %v", err)
		}
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
	// 安全组（此前未落库：重启后配置全丢）。反序列化失败时保持为空而不是报错，
	// 避免一条损坏的安全组记录把整个面板拖得起不来。
	if raw := strings.TrimSpace(meta["sec_groups"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.SecGroups); err != nil {
			log.Printf("Warning: 解析已存安全组失败（按空处理）: %v", err)
		}
	}
	if raw := strings.TrimSpace(meta["sec_group_rules"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.SecGroupRules); err != nil {
			log.Printf("Warning: 解析已存安全组规则失败（按空处理）: %v", err)
		}
	}
	cfg.SecurityGroupEnforced = atob(meta["security_group_enforced"])

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
	// 节点自 P2 起独立成表（每节点一行），不再走 app_meta 的单行 JSON。
	// 表为空而旧键仍在 = 正在升级，先把旧键搬进表再读，避免升级丢节点。
	if _, err := migrateLegacyNodesRow(meta); err != nil {
		return nil, false, err
	}
	if cfg.Nodes, err = loadNodes(); err != nil {
		return nil, false, err
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

	// AdminTOTPSecret 解密（F-02）：存量明文原样通过（自动迁移），解密失败置空
	// 并禁用 TOTP（密钥残缺时放行比误锁更可恢复，设置页重存即可）。
	if raw := strings.TrimSpace(cfg.AdminTOTPSecret); raw != "" {
		plainTOTP, err := DecryptNodeToken(raw)
		if err != nil {
			cfg.AdminTOTPSecret = ""
			cfg.AdminTOTPEnabled = false
		} else {
			cfg.AdminTOTPSecret = plainTOTP
		}
	}

	// JWTSecret 解密（F-02）：存量明文原样通过；解密失败置空 = 所有既有 JWT
	// 立即失效（Fail-closed：签名密钥残缺时宁可全体登出，也不能拿密文当密钥用）。
	if raw := strings.TrimSpace(cfg.JWTSecret); raw != "" {
		plainJWT, err := DecryptNodeToken(raw)
		if err != nil {
			cfg.JWTSecret = ""
		} else {
			cfg.JWTSecret = plainJWT
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
	// 机器级访问码凭据独立成表（container_access_links），加载后挂到容器上。
	if err := attachContainerAccessLinks(cfg); err != nil {
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
	// 用刚加载到的内存快照播种「已落库行指纹」：加载后内存==库，故紧接其后的
	// 第一次保存若没有改动，不会产生任何行写入（重启不再触发一次全量重写）。
	seedPersistedRows(cfg, meta)
	return cfg, true, nil
}

// seedPersistedRows 由加载到的配置播种行指纹。必须在 dbMu 保护下、且与
// saveConfigIncremental 采用同一指纹口径（containerFingerprint 会先归一别名）。
// dbMeta 是库中实际的 app_meta 键值：用于只播种「库里确实存在」的键。
func seedPersistedRows(cfg *EyvescloudConfig, dbMeta map[string]string) {
	if cfg == nil {
		return
	}
	// 重新装载配置后，上一次的下标缓存对新切片没有意义，先清空。
	resetRowIndexes()
	// 装载后内存日志 == 库内容（loadAuditLogs/loadLoginLogs 刚从库读出），
	// 清掉可能残留的脏标记，避免一次无意义的日志表重写。
	logsDirty = false
	next := newRowFingerprints()
	for _, c := range cfg.Containers {
		next.containers[c.ID] = containerFingerprint(c)
		code := strings.TrimSpace(c.AccessCode)
		pw := strings.TrimSpace(c.AccessCodePassword)
		if code != "" || pw != "" {
			next.accessLinks[c.UUID] = fingerprint([2]string{code, pw})
		}
	}
	for _, n := range cfg.Nodes {
		next.nodes[n.ID] = fingerprint(n)
	}
	for _, su := range cfg.SubUsers {
		next.subUsers[su.ID] = fingerprint(su)
	}
	for _, k := range cfg.ApiKeys {
		next.apiKeys[k.ID] = fingerprint(k)
	}
	for _, s := range cfg.Snapshots {
		next.snapshots[s.ID] = fingerprint(s)
	}
	for _, t := range cfg.Tasks {
		next.tasks[t.ID] = fingerprint(t)
	}
	next.enabledImages = fingerprint(cfg.EnabledImages)
	next.meta = seedMetaFingerprints(cfg, dbMeta)
	persistedRows = next
}

// saveConfigToDB 把内存配置落库。P1 起为**行级增量**：只写发生变化/消失的行，
// 未变化的行不产生任何语句（设计不变量：单次操作写 O(受影响行)）。
// 事务提交成功后才更新 persistedRows，失败（回滚）则保持旧指纹，下次重试仍会重写。
func saveConfigToDB() error {
	return saveConfigToDBHinted(nil)
}

// saveConfigToDBHinted 是带「脏集声明」的保存入口。hint 为 nil 时行为与全量
// 保存完全一致（逐行指纹 diff + upsert/delete）；非 nil 时只对声明过的行重算
// 指纹，其余行沿用上次指纹，把 O(全部行) 的指纹扫描降到 O(声明行)。
//
// 安全约定：声明者必须保证「声明集合 ⊇ 本次实际改动集合」；任何未改造的写入
// 路径都应调用 saveConfigToDB()（nil hint，全量兜底）。若本次保存失败，置
// forceFullScanNextSave，令下一次保存强制全量，避免被漏声明的改动永久丢失。
func saveConfigToDBHinted(hint *dirtyHint) error {
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

	next, err := saveConfigIncremental(tx, hint)
	if err != nil {
		forceFullScanNextSave = true
		return err
	}
	// app_meta 也走行级：只写指纹变化的键（见 store_meta.go）。
	if err := diffMeta(tx, &next); err != nil {
		forceFullScanNextSave = true
		return err
	}
	// 有界日志表（audit_logs/login_logs）默认不写：常规写入已由 appendAuditLogRow /
	// appendLoginLogRow 增量落库，库与内存一致；只有 logsDirty 置位（追加失败自愈 /
	// 保留期裁剪）时才重写。这去掉了「每次保存重写 ~700 行」的固定写入（#95）。
	writeLogs := logsDirty
	if writeLogs {
		if err := saveBoundedLogs(tx); err != nil {
			forceFullScanNextSave = true
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		forceFullScanNextSave = true
		return err
	}
	// 提交成功才清脏：失败时保持置位，下次保存继续尝试重写。
	if writeLogs {
		logsDirty = false
	}
	persistedRows = next
	return nil
}

// verifyHintCompleteness 用一次全量扫描复核刚完成的声明式保存：把全量扫描的结果与
// 「已落库指纹」逐集合比对，返回第一个差异。比较在回滚事务里进行，不改变库状态。
//
// 这是 #96 大规模改造的护栏：把声明式保存路径的 handler 跑一遍后调用它，任何漏声明
// 都会被定位到具体集合与键。**它假定调用方遵守「声明 ⊇ 已改动」契约**，故仅供那些
// 确实遵守契约的路径/测试使用——故意违反契约的契约测试不能用它判定。
func verifyHintCompleteness() error {
	if db == nil {
		return fmt.Errorf("sqlite database is not initialized")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	full, err := saveConfigIncremental(tx, nil)
	if err != nil {
		return err
	}
	for _, c := range []struct {
		name string
		a, b map[string]string
	}{
		{"sub_users", full.subUsers, persistedRows.subUsers},
		{"api_keys", full.apiKeys, persistedRows.apiKeys},
		{"snapshots", full.snapshots, persistedRows.snapshots},
		{"tasks", full.tasks, persistedRows.tasks},
		{"nodes", full.nodes, persistedRows.nodes},
		{"access_links", full.accessLinks, persistedRows.accessLinks},
	} {
		if diff := firstMapDiff(c.a, c.b); diff != "" {
			return fmt.Errorf("%s: %s", c.name, diff)
		}
	}
	if full.enabledImages != persistedRows.enabledImages {
		return fmt.Errorf("enabled_images 指纹不一致")
	}
	if diff := firstIntMapDiff(full.containers, persistedRows.containers); diff != "" {
		return fmt.Errorf("containers: %s", diff)
	}
	return nil
}

func firstMapDiff(a, b map[string]string) string {
	for k, v := range a {
		if b[k] != v {
			return fmt.Sprintf("键 %q 全量=%q 已落库=%q（未声明或未落库）", k, v, b[k])
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			return fmt.Sprintf("键 %q 已落库但全量扫描中不存在（可能被误删声明）", k)
		}
	}
	return ""
}

func firstIntMapDiff(a, b map[int]string) string {
	for k, v := range a {
		if b[k] != v {
			return fmt.Sprintf("ID %d 全量=%q 已落库=%q（未声明或未落库）", k, v, b[k])
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			return fmt.Sprintf("ID %d 已落库但全量扫描中不存在（可能被误删声明）", k)
		}
	}
	return ""
}

// upsertContainerRow 写入单个容器及其子表（端口映射 / 公网 IPv4 / IPv6）。
// 行级口径：先删该容器的子表行与主表行，再整体插入，避免位置序列表（position
// 主键）残留旧行。只影响该容器，O(该容器的子表行数)。
func upsertContainerRow(tx *sql.Tx, c Container) error {
	if err := deleteContainerChildren(tx, c.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM containers WHERE id = ?`, c.ID); err != nil {
		return err
	}
	{
		NormalizeContainerResourceAliases(&c)
		allowedImageIDs := encodeStringSlice(c.AllowedImageIDs)
		if _, err := tx.Exec(`INSERT INTO containers (
			id, uuid, name, virtualization, lxc_name, kvm_name, disk_image, storage_pool_id, storage_path, mac_address, template,
			node_id, node_local_id,
			vcpu, cpu_percent, ram_mb, disk_gb, network_bw_mbps, network_down_mbps, network_up_mbps,
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
			suspended, suspended_at, suspended_reason, remark, locked, recycled_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? )`,
			c.ID, c.UUID, c.Name, c.Virtualization, c.LXCName, c.KVMName, c.DiskImage, c.StoragePoolID, c.StoragePath, c.MACAddress, c.Template,
			c.NodeID, c.NodeLocalID,
			c.VCPU, c.CPUPercent, c.RAMMB, c.DiskGB, c.NetworkBWMbps, c.NetworkDownMbps, c.NetworkUpMbps,
			c.MonthlyTrafficGB, c.TrafficMode, c.TrafficInGB,
			c.TrafficOutGB, c.TrafficUsedRX, c.TrafficUsedTX, c.TrafficResetDate,
			c.IOSpeedMBps, c.IOReadMBps, c.IOWriteMBps,
			c.Status, boolInt(c.RestoreOnHostBoot), c.IP, c.LANIPv4Mode, c.LANInterface, c.LANIPv4Address, c.LANIPv4PrefixLen, c.LANIPv4Gateway,
			c.IPv6, c.IPv6PrefixLen, c.IPv6Interface, c.VNCPort, c.SSHPort, EncryptSecretAtRest(c.SSHPassword),
			c.SSHHostKey, c.PortMappingLimit, c.SnapshotLimit, c.CreatedAt, c.ExpiresAt,
			boolInt(c.SnapshotScheduleEnabled), c.SnapshotScheduleIntervalHours, c.SnapshotScheduleTime,
			c.SnapshotScheduleLastRun, c.SnapshotScheduleNextRun, c.SnapshotScheduleCreatedBy,
			boolInt(c.PolicyBlocked), c.PolicyBlockedReason, c.PolicyBlockedAt,
			boolInt(c.FirewallEnabled), normalizeFirewallDefaultAction(c.FirewallDefaultAction), marshalFirewallRules(c.FirewallRules), allowedImageIDs, boolInt(c.ImageLimitConfigured),
			c.Tenant, c.CloudInitUserData, c.DataDiskGB, c.DataDiskMountPath,
			boolInt(c.RescueEnabled), c.RescueISOID, c.RescueISOPath,
			c.OptionalISOID, c.OptionalISOPath,
			c.RootVolumeID, encodeStringSlice(c.DataVolumeIDs),
			boolInt(c.Suspended), c.SuspendedAt, c.SuspendedReason, c.Remark, boolInt(c.Locked), c.RecycledAt,
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

// deleteContainerChildren 删除容器的位置序列表子行（幂等）。
func deleteContainerChildren(tx *sql.Tx, id int) error {
	for _, table := range []string{"port_mappings", "container_public_ipv4s", "container_ipv6_addresses"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE container_id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

// deleteContainerRow 删除容器主表行及其子表行（幂等）。访问码凭据独立成表，
// 由 saveConfigIncremental 的 diffAccessLinks 统一处理。
func deleteContainerRow(tx *sql.Tx, id int) error {
	if err := deleteContainerChildren(tx, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM containers WHERE id = ?`, id)
	return err
}

// upsertAccessLinkRow 落库「机器级访问码凭据」（access_code + access_code_password）。
// 两者都必须能被管理端/用户端回显，故以 enc:v1: 可逆密文落库。
func upsertAccessLinkRow(tx *sql.Tx, c Container) error {
	if _, err := tx.Exec(`DELETE FROM container_access_links WHERE container_uuid = ?`, c.UUID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO container_access_links (container_uuid, access_code, access_code_password)
		VALUES (?, ?, ?)`, c.UUID, EncryptSecretAtRest(strings.TrimSpace(c.AccessCode)), EncryptSecretAtRest(strings.TrimSpace(c.AccessCodePassword))); err != nil {
		return err
	}
	return nil
}

// attachContainerAccessLinks 读取机器级访问码凭据，解密后挂到对应容器上。
// 独立于 containers 主表的 SELECT，避免改动体量巨大的容器列清单。
func attachContainerAccessLinks(cfg *EyvescloudConfig) error {
	if cfg == nil {
		return nil
	}
	rows, err := db.Query(`SELECT container_uuid, access_code, access_code_password FROM container_access_links`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pair struct{ code, pw string }
	links := map[string]pair{}
	for rows.Next() {
		var uuid string
		var code, pw sql.NullString
		if err := rows.Scan(&uuid, &code, &pw); err != nil {
			return err
		}
		links[uuid] = pair{DecryptSecretAtRest(code.String), DecryptSecretAtRest(pw.String)}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range cfg.Containers {
		if v, ok := links[cfg.Containers[i].UUID]; ok {
			cfg.Containers[i].AccessCode = v.code
			cfg.Containers[i].AccessCodePassword = v.pw
		}
	}
	return nil
}

// upsertSubUserRow 行级写入单个子用户及其两个位置序列表（容器名 / 容器 UUID）。
func upsertSubUserRow(tx *sql.Tx, su SubUser) error {
	if err := deleteSubUserChildren(tx, su.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sub_users WHERE id = ?`, su.ID); err != nil {
		return err
	}
	{
		allowedImageIDs := encodeStringSlice(su.AllowedImageIDs)
		// access_code 加密落库（渗透测试 F-02）：访问码=免密登录凭据，明文落库
		// 让 DB 泄漏直接等于账号泄漏。加密失败拒绝落库（与节点 token 同口径）。
		accessCodeEnc := su.AccessCode
		if accessCodeEnc != "" {
			enc, err := EncryptNodeToken(su.AccessCode)
			if err != nil {
				return fmt.Errorf("加密子用户 %s access_code 失败: %w", su.Username, err)
			}
			accessCodeEnc = enc
		}
		// access_code_password 同样加密落库：该口令可由管理员/用户端回显，
		// 明文落库等于 DB 泄漏即口令泄漏。
		accessCodePasswordEnc := su.AccessCodePassword
		if accessCodePasswordEnc != "" {
			enc, err := EncryptNodeToken(su.AccessCodePassword)
			if err != nil {
				return fmt.Errorf("加密子用户 %s access_code_password 失败: %w", su.Username, err)
			}
			accessCodePasswordEnc = enc
		}
		if _, err := tx.Exec(`INSERT INTO sub_users(id, username, password, pass_hash, access_code, access_code_password, created_at, token_version, allowed_image_ids, image_limit_configured, role, tenant)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, su.ID, su.Username, "", su.PassHash, accessCodeEnc, accessCodePasswordEnc, su.CreatedAt, su.TokenVersion, allowedImageIDs, boolInt(su.ImageLimitConfigured), subUserRoleForStorage(su.Role), su.Tenant); err != nil {
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

func deleteSubUserChildren(tx *sql.Tx, id string) error {
	for _, table := range []string{"sub_user_container_names", "sub_user_container_uuids"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE sub_user_id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

func deleteSubUserRow(tx *sql.Tx, id string) error {
	if err := deleteSubUserChildren(tx, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM sub_users WHERE id = ?`, id)
	return err
}

// upsertAPIKeyRow 行级写入单条 API Key。
func upsertAPIKeyRow(tx *sql.Tx, k ApiKeyConfig) error {
	if _, err := tx.Exec(`DELETE FROM api_keys WHERE id = ?`, k.ID); err != nil {
		return err
	}
	scopes := encodeStringSlice(k.Scopes)
	containerUUIDs := encodeStringSlice(k.ContainerUUIDs)
	_, err := tx.Exec(`INSERT INTO api_keys(id, name, key_hash, key_fingerprint, prefix, ip_whitelist, created_at, last_used, scopes, expires_at, disabled, container_uuids, last_used_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.ID, k.Name, k.KeyHash, k.KeyFingerprint, k.Prefix, k.IPWhitelist, k.CreatedAt, k.LastUsed, scopes, k.ExpiresAt, boolInt(k.Disabled), containerUUIDs, k.LastUsedIP)
	return err
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
		if _, err := tx.Exec(`INSERT INTO audit_logs(time, action, target, detail, "user", ip, user_agent, success_set, success, error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, log.Time, log.Action, log.Target, log.Detail, log.User, log.IP, log.UserAgent, successSet, success, log.Error); err != nil {
			return err
		}
	}
	return nil
}

// upsertTaskRow 行级写入单个任务及其两个位置序列表（额外端口 / NAT 映射）。
func upsertTaskRow(tx *sql.Tx, task SavedTask) error {
	if err := deleteTaskChildren(tx, task.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, task.ID); err != nil {
		return err
	}
	{
		cfg := parseSavedTaskConfig(task.Config)
		if _, err := tx.Exec(`INSERT INTO tasks(
			id, type, container_id, container_name, status, error, created_at, template_id, "user", ip, user_agent,
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
			cfg.SSHAuthMode, EncryptSecretAtRest(cfg.SSHPassword), cfg.SSHPublicKey, encodeStringSlice(cfg.AllowedImageIDs), boolInt(cfg.ImageLimitConfigured), cfg.ExpiresAt,
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

func deleteTaskChildren(tx *sql.Tx, id string) error {
	for _, table := range []string{"task_extra_ports", "task_nat_port_mappings"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE task_id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

func deleteTaskRow(tx *sql.Tx, id string) error {
	if err := deleteTaskChildren(tx, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
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

// appendAuditLogRow 增量追加一行审计日志（不再触发整库 DELETE+INSERT），并按 id 裁剪到 keep 行。
// 调用方已在 AppConfigMu 内把同一行 append 进内存切片并做同口径裁剪，故 DB 与内存始终一致。
// keep<=0 表示不裁剪。
func appendAuditLogRow(log AuditLog, keep int) error {
	successSet := 0
	success := 0
	if log.Success != nil {
		successSet = 1
		if *log.Success {
			success = 1
		}
	}
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
	if _, err := tx.Exec(`INSERT INTO audit_logs(time, action, target, detail, "user", ip, user_agent, success_set, success, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.Time, log.Action, log.Target, log.Detail, log.User, log.IP, log.UserAgent, successSet, success, log.Error); err != nil {
		return err
	}
	if keep > 0 {
		if _, err := tx.Exec(`DELETE FROM audit_logs WHERE id NOT IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT ?)`, keep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// appendLoginLogRow 同 appendAuditLogRow，作用于 login_logs。
func appendLoginLogRow(log SavedLoginLog, keep int) error {
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
	if _, err := tx.Exec(`INSERT INTO login_logs(time, username, ip, user_agent, success) VALUES (?, ?, ?, ?, ?)`,
		log.Time, log.Username, log.IP, log.UserAgent, boolInt(log.Success)); err != nil {
		return err
	}
	if keep > 0 {
		if _, err := tx.Exec(`DELETE FROM login_logs WHERE id NOT IN (SELECT id FROM login_logs ORDER BY id DESC LIMIT ?)`, keep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func saveEnabledImages(tx *sql.Tx) error {
	for i, id := range AppConfig.EnabledImages {
		if _, err := tx.Exec(`INSERT INTO enabled_images(position, image_id) VALUES (?, ?)`, i, id); err != nil {
			return err
		}
	}
	return nil
}

// upsertSnapshotRow 行级写入单条快照记录。
func upsertSnapshotRow(tx *sql.Tx, snapshot Snapshot) error {
	if _, err := tx.Exec(`DELETE FROM snapshots WHERE id = ?`, snapshot.ID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO snapshots(id, container_id, container_name, lxc_name, created_at, created_by, scheduled, path, size_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshot.ID, snapshot.ContainerID, snapshot.ContainerName, snapshot.LXCName, snapshot.CreatedAt, snapshot.CreatedBy, boolInt(snapshot.Scheduled), snapshot.Path, snapshot.SizeBytes)
	return err
}

// ---- P2：节点行级落库 ----
//
// 加密只发生在这里，且只对「指纹变化」的节点调用（见 store_rows.go 的 diffNodes），
// 因此未改动的节点不会被重复加密。指纹取的是**明文** Node，AES-GCM 的随机 nonce
// 不参与判定。

const nodeColumns = `id, name, address, public_host, token, install_key,
	install_key_created_at, install_key_ip, status, last_seen, version, os_name,
	cpu_count, ram_total_mb, ram_used_mb, disk_total_gb, disk_used_gb, container_count,
	region_id, node_group_id, cluster_id, virt_types, created_at,
	maintenance_mode, maintenance_since, tls_skip_verify, allow_private_addr`

func nodeRowValues(n Node) ([]any, error) {
	token, err := EncryptNodeToken(n.Token)
	if err != nil {
		return nil, fmt.Errorf("加密节点 %s Token 失败: %w", n.ID, err)
	}
	installKey, err := EncryptNodeToken(n.InstallKey)
	if err != nil {
		return nil, fmt.Errorf("加密节点 %s install_key 失败: %w", n.ID, err)
	}
	return []any{
		n.ID, n.Name, n.Address, n.PublicHost, token, installKey,
		n.InstallKeyCreatedAt, n.InstallKeyIP, n.Status, n.LastSeen, n.Version, n.OSName,
		n.CPUCount, n.RAMTotalMB, n.RAMUsedMB, n.DiskTotalGB, n.DiskUsedGB, n.ContainerCount,
		n.RegionID, n.NodeGroupID, n.ClusterID, encodeStringSlice(n.VirtTypes), n.CreatedAt,
		boolInt(n.MaintenanceMode), n.MaintenanceSince, boolInt(n.TLSSkipVerify), boolInt(n.AllowPrivateAddr),
	}, nil
}

// upsertNodeRow 行级写入单个节点（主键即节点 ID，无需子表清理）。
func upsertNodeRow(tx *sql.Tx, n Node) error {
	values, err := nodeRowValues(n)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, n.ID); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	_, err = tx.Exec(`INSERT INTO nodes(`+nodeColumns+`) VALUES (`+placeholders+`)`, values...)
	return err
}

func deleteNodeRow(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// loadNodes 按创建时间读回节点。**顺序即 API 下发顺序**（列表接口原样透传，
// 前端不再排序），拆分前是 app_meta JSON 数组的追加顺序，即创建顺序；
// 这里用 created_at 复现同一顺序，并以 id 打破同秒并列，保证重启前后顺序稳定。
func loadNodes() ([]Node, error) {
	rows, err := db.Query(`SELECT ` + nodeColumns + ` FROM nodes ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Node{}
	for rows.Next() {
		var n Node
		var virtTypes string
		var maintenanceMode, tlsSkipVerify, allowPrivateAddr int
		if err := rows.Scan(&n.ID, &n.Name, &n.Address, &n.PublicHost, &n.Token, &n.InstallKey,
			&n.InstallKeyCreatedAt, &n.InstallKeyIP, &n.Status, &n.LastSeen, &n.Version, &n.OSName,
			&n.CPUCount, &n.RAMTotalMB, &n.RAMUsedMB, &n.DiskTotalGB, &n.DiskUsedGB, &n.ContainerCount,
			&n.RegionID, &n.NodeGroupID, &n.ClusterID, &virtTypes, &n.CreatedAt,
			&maintenanceMode, &n.MaintenanceSince, &tlsSkipVerify, &allowPrivateAddr); err != nil {
			return nil, err
		}
		n.VirtTypes = decodeStringSlice(virtTypes)
		n.MaintenanceMode = maintenanceMode != 0
		n.TLSSkipVerify = tlsSkipVerify != 0
		n.AllowPrivateAddr = allowPrivateAddr != 0
		// 读取时把 enc:v1: 密文还原为明文（内存态保持明文）。存量明文值原样通过；
		// 解密失败不阻断启动，但该节点 Token 置空使其失效，等待重新注册——宁可断连
		// 也不能拿密文当凭据误用。install_key 解密失败同样置空（换发即可恢复）。
		if plain, err := DecryptNodeToken(n.Token); err != nil {
			n.Token = ""
		} else {
			n.Token = plain
		}
		if plainKey, err := DecryptNodeToken(n.InstallKey); err != nil {
			n.InstallKey = ""
			n.InstallKeyCreatedAt = ""
		} else {
			n.InstallKey = plainKey
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// migrateLegacyNodesRow 处理升级路径：老库里节点存在 app_meta 的单行 JSON（键
// "nodes"），新版本改成了 nodes 表。表为空而旧键存在时把旧键搬进表，避免升级后
// 节点全部丢失。
//
// 搬迁与「删除旧键」在**同一个事务**里完成，不依赖 app_meta 的清理时机：否则一旦
// 在下次保存前进程重启，就会反复走迁移路径。旧键解析失败则返回错误、拒绝启动——
// 宁可停服务让人来看，也不能把库里的节点数据静默丢掉。
// 返回是否发生了搬迁。
func migrateLegacyNodesRow(meta map[string]string) (bool, error) {
	raw := strings.TrimSpace(meta["nodes"])
	if raw == "" {
		return false, nil
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		// 表里已有数据（搬迁已完成）：旧键可能因上次「提交后、清理前」退出而残留，
		// 就地删掉，幂等且无需再搬。
		if _, err := db.Exec(`DELETE FROM app_meta WHERE key = 'nodes'`); err != nil {
			return false, err
		}
		return false, nil
	}
	var legacy []Node
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return false, fmt.Errorf("解析历史 app_meta[nodes] 失败: %w", err)
	}
	usable := make([]Node, 0, len(legacy))
	for _, n := range legacy {
		if n.ID != "" {
			usable = append(usable, n)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for i := range usable {
		// 旧值的 token 可能是明文（加密保护上线前遗留）也可能是密文；
		// upsertNodeRow 统一重新加密落库，EncryptNodeToken 对两者都幂等处理。
		if err := upsertNodeRow(tx, usable[i]); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM app_meta WHERE key = 'nodes'`); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return len(usable) > 0, nil
}

func loadContainers() ([]Container, error) {
	rows, err := db.Query(`SELECT
		id, uuid, name, virtualization, lxc_name, kvm_name, disk_image, storage_pool_id, storage_path, mac_address, template,
		node_id, node_local_id,
		vcpu, cpu_percent, ram_mb, disk_gb, network_bw_mbps, network_down_mbps, network_up_mbps,
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
		suspended, suspended_at, suspended_reason, remark, locked, recycled_at
		FROM containers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Container{}
	skipped := 0
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
		var remarkCol sql.NullString
		var lockedCol int
		var recycledCol sql.NullString
		var nodeIDCol sql.NullString
		var nodeLocalIDCol sql.NullInt64
		// ssh_password 以 enc:v1: 密文落库（审计 H-5）：先扫进临时变量再解密。
		var sshPasswordCol sql.NullString
		if err := rows.Scan(
			&c.ID, &c.UUID, &c.Name, &c.Virtualization, &c.LXCName, &c.KVMName, &c.DiskImage, &storagePoolID, &storagePath, &c.MACAddress, &c.Template,
			&nodeIDCol, &nodeLocalIDCol,
			&c.VCPU, &c.CPUPercent, &c.RAMMB, &c.DiskGB, &c.NetworkBWMbps, &c.NetworkDownMbps, &c.NetworkUpMbps,
			&c.MonthlyTrafficGB, &c.TrafficMode, &c.TrafficInGB,
			&c.TrafficOutGB, &c.TrafficUsedRX, &c.TrafficUsedTX, &c.TrafficResetDate,
			&c.IOSpeedMBps, &c.IOReadMBps, &c.IOWriteMBps,
			&c.Status, &restoreOnHostBoot, &c.IP, &lanIPv4Mode, &lanInterface, &lanIPv4Address, &lanIPv4PrefixLen, &lanIPv4Gateway,
			&c.IPv6, &c.IPv6PrefixLen, &c.IPv6Interface, &c.VNCPort, &c.SSHPort, &sshPasswordCol,
			&c.SSHHostKey, &c.PortMappingLimit, &c.SnapshotLimit, &c.CreatedAt, &c.ExpiresAt,
			&scheduleEnabled, &c.SnapshotScheduleIntervalHours, &c.SnapshotScheduleTime,
			&c.SnapshotScheduleLastRun, &c.SnapshotScheduleNextRun, &c.SnapshotScheduleCreatedBy,
			&policyBlocked, &c.PolicyBlockedReason, &c.PolicyBlockedAt,
			&firewallEnabled, &firewallDefaultAction, &firewallRulesJSON, &allowedImageIDs, &imageLimitConfigured,
			&tenant, &cloudInitUserData, &c.DataDiskGB, &dataDiskMountPath,
			&rescueEnabled, &rescueISOID, &rescueISOPath,
			&optionalISOID, &optionalISOPath,
			&rootVolumeID, &dataVolumeIDs,
			&suspended, &suspendedAt, &suspendedReason, &remarkCol, &lockedCol, &recycledCol,
		); err != nil {
			// 单条容器记录损坏不应让整个面板起不来：跳过并留下可定位的日志。
			// 此前这里直接 return err → 整个 config 加载失败 → 面板无法启动。
			// （生产踩坑：mac_address 等列只要有一行为 NULL 就会触发。）
			// 被跳过的容器仍存在于宿主机上，可用 CLI 的「导入现有容器」重新纳管。
			log.Printf("Warning: skipping unreadable container row (id=%d): %v", c.ID, err)
			skipped++
			continue
		}
		c.CloudInitUserData = cloudInitUserData.String
		// SSH 口令：解密 enc:v1: 密文；存量明文原样通过；解密失败置空
		// （宁可让面板"看不到口令"，也不把密文当口令用）。
		c.SSHPassword = DecryptSecretAtRest(sshPasswordCol.String)
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
		c.Remark = remarkCol.String
		c.Locked = lockedCol != 0
		c.RecycledAt = recycledCol.String
		c.NodeID = nodeIDCol.String
		c.NodeLocalID = int(nodeLocalIDCol.Int64)
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
	// 子表按容器 ID 一次取回后再分组：旧写法对每个容器各查三次（10w 容器 ≈ 30 万次
	// 往返），SQLite 在进程内尚可接受，Postgres 上启动要数百秒。
	pmByID, err := loadPortMappingsByContainer()
	if err != nil {
		return nil, err
	}
	ipv4ByID, err := loadPublicIPv4sByContainer()
	if err != nil {
		return nil, err
	}
	ipv6ByID, err := loadIPv6AddressesByContainer()
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].PortMappings = mapGetOrEmpty(pmByID, result[i].ID)
		result[i].PublicIPv4s = mapGetOrEmpty(ipv4ByID, result[i].ID)
		result[i].IPv6Addresses = mapGetOrEmpty(ipv6ByID, result[i].ID)
		result[i].NormalizeNetworkAssignments()
	}
	if skipped > 0 {
		log.Printf("Warning: %d container record(s) were skipped due to unreadable data; "+
			"the panel started with the remaining records. Run `eyvescloud cli` → 导入现有容器 "+
			"to re-adopt the affected instances, then repair the database.", skipped)
	}
	return result, nil
}

// mapGetOrEmpty 返回 m[k]，键不存在时给出非 nil 空切片：JSON 形状必须是 [] 而不是
// null，否则容器/子用户列表的响应体会与逐行加载时不一致。
func mapGetOrEmpty[K comparable, V any](m map[K][]V, k K) []V {
	if v, ok := m[k]; ok && v != nil {
		return v
	}
	return []V{}
}

func loadPortMappingsByContainer() (map[int][]PortMapping, error) {
	rows, err := db.Query(`SELECT container_id, container_port, host_port, host_ip, protocol, description FROM port_mappings ORDER BY container_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]PortMapping{}
	for rows.Next() {
		var id int
		var pm PortMapping
		var hostIP sql.NullString
		if err := rows.Scan(&id, &pm.ContainerPort, &pm.HostPort, &hostIP, &pm.Protocol, &pm.Description); err != nil {
			return nil, err
		}
		pm.HostIP = hostIP.String
		out[id] = append(out[id], pm)
	}
	return out, rows.Err()
}

func loadPublicIPv4sByContainer() (map[int][]PublicIPv4Assignment, error) {
	rows, err := db.Query(`SELECT container_id, address, interface, prefix_len, gateway, rdns FROM container_public_ipv4s ORDER BY container_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]PublicIPv4Assignment{}
	for rows.Next() {
		var id int
		var item PublicIPv4Assignment
		var iface sql.NullString
		var prefixLen sql.NullInt64
		var gateway sql.NullString
		var rdns sql.NullString
		if err := rows.Scan(&id, &item.Address, &iface, &prefixLen, &gateway, &rdns); err != nil {
			return nil, err
		}
		item.Interface = iface.String
		if prefixLen.Valid {
			item.PrefixLen = int(prefixLen.Int64)
		}
		item.Gateway = gateway.String
		item.RDNS = rdns.String
		out[id] = append(out[id], item)
	}
	return out, rows.Err()
}

func loadIPv6AddressesByContainer() (map[int][]IPv6Assignment, error) {
	rows, err := db.Query(`SELECT container_id, address, prefix_len, interface, rdns FROM container_ipv6_addresses ORDER BY container_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]IPv6Assignment{}
	for rows.Next() {
		var id int
		var item IPv6Assignment
		var prefixLen sql.NullInt64
		var iface sql.NullString
		var rdns sql.NullString
		if err := rows.Scan(&id, &item.Address, &prefixLen, &iface, &rdns); err != nil {
			return nil, err
		}
		if prefixLen.Valid {
			item.PrefixLen = int(prefixLen.Int64)
		}
		item.Interface = iface.String
		item.RDNS = rdns.String
		out[id] = append(out[id], item)
	}
	return out, rows.Err()
}

func loadSubUsers() ([]SubUser, error) {
	rows, err := db.Query(`SELECT id, username, password, pass_hash, access_code, access_code_password, created_at, token_version, allowed_image_ids, image_limit_configured, role, tenant FROM sub_users ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SubUser{}
	for rows.Next() {
		var su SubUser
		var allowedImageIDs sql.NullString
		var imageLimitConfigured int
		if err := rows.Scan(&su.ID, &su.Username, &su.Password, &su.PassHash, &su.AccessCode, &su.AccessCodePassword, &su.CreatedAt, &su.TokenVersion, &allowedImageIDs, &imageLimitConfigured, &su.Role, &su.Tenant); err != nil {
			return nil, err
		}
		su.AllowedImageIDs = decodeStringSlice(allowedImageIDs.String)
		su.ImageLimitConfigured = imageLimitConfigured != 0
		// 明文口令不以持久化凭据为准：既有库中可能残留的历史明文一律清空，
		// 仅保留 bcrypt pass_hash 用于登录校验，降低 DB 泄露面。
		su.Password = ""
		// access_code 解密（F-02）：存量明文（无 enc: 前缀）原样通过（自动迁移）；
		// 解密失败置空——访问码失效可由管理员重新生成，比误用密文安全。
		if su.AccessCode != "" {
			if plain, err := DecryptNodeToken(su.AccessCode); err == nil {
				su.AccessCode = plain
			} else if strings.HasPrefix(su.AccessCode, "enc:") {
				su.AccessCode = ""
			}
		}
		// access_code_password 同口径解密；解密失败置空，稍后按空值回填新口令。
		if su.AccessCodePassword != "" {
			if plain, err := DecryptNodeToken(su.AccessCodePassword); err == nil {
				su.AccessCodePassword = plain
			} else if strings.HasPrefix(su.AccessCodePassword, "enc:") {
				su.AccessCodePassword = ""
			}
		}
		result = append(result, su)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// 说明：访问码/访问码口令已下沉到「机器级」（container_access_links 表），
	// 子用户行上的同名历史字段不再参与鉴权，加载后也不再回填。
	namesByID, err := loadStringListByKey("sub_user_container_names", "container_name", "sub_user_id")
	if err != nil {
		return nil, err
	}
	uuidsByID, err := loadStringListByKey("sub_user_container_uuids", "container_uuid", "sub_user_id")
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].ContainerNames = mapGetOrEmpty(namesByID, result[i].ID)
		result[i].ContainerUUIDs = mapGetOrEmpty(uuidsByID, result[i].ID)
	}
	return result, nil
}

// loadStringListByKey 一次取回整张 (key → 有序值列表) 表。表名/列名是代码内常量，
// 不存在注入面；position 保证同 key 内的原始顺序。
func loadStringListByKey(table, valueColumn, keyColumn string) (map[string][]string, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s, %s FROM %s ORDER BY %s, position`, keyColumn, valueColumn, table, keyColumn))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = append(out[key], value)
	}
	return out, rows.Err()
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
	rows, err := db.Query(`SELECT time, action, target, detail, "user", ip, user_agent, success_set, success, error FROM audit_logs ORDER BY id`)
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
		id, type, container_id, container_name, status, error, created_at, template_id, "user", ip, user_agent,
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
		cfg.SSHPassword = DecryptSecretAtRest(sshPassword.String)
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
	// 与容器子表同口径：任务子表一次取回后按 task_id 分组。任务历史随实例操作长期
	// 累积，10 万量级下逐行取会是 20 万次往返（Postgres 上以分钟计）。
	extraPortsByTask, err := loadTaskExtraPortsByTask()
	if err != nil {
		return nil, err
	}
	natPortsByTask, err := loadTaskNATPortMappingsByTask()
	if err != nil {
		return nil, err
	}
	for i := range result {
		configs[i].ExtraPorts = mapGetOrEmpty(extraPortsByTask, result[i].ID)
		configs[i].NATPortMappings = mapGetOrEmpty(natPortsByTask, result[i].ID)
		result[i].Config = encodeSavedTaskConfig(configs[i])
	}
	return result, nil
}

func loadTaskExtraPortsByTask() (map[string][]int, error) {
	rows, err := db.Query(`SELECT task_id, port FROM task_extra_ports ORDER BY task_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]int{}
	for rows.Next() {
		var taskID string
		var port int
		if err := rows.Scan(&taskID, &port); err != nil {
			return nil, err
		}
		out[taskID] = append(out[taskID], port)
	}
	return out, rows.Err()
}

func loadTaskNATPortMappingsByTask() (map[string][]PortMapping, error) {
	rows, err := db.Query(`SELECT task_id, host_port, container_port, protocol, description
		FROM task_nat_port_mappings ORDER BY task_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]PortMapping{}
	for rows.Next() {
		var taskID string
		var mapping PortMapping
		if err := rows.Scan(&taskID, &mapping.HostPort, &mapping.ContainerPort, &mapping.Protocol, &mapping.Description); err != nil {
			return nil, err
		}
		out[taskID] = append(out[taskID], mapping)
	}
	return out, rows.Err()
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
