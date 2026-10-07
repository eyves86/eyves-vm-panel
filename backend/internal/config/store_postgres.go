package config

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// store_postgres.go —— 配置库的 Postgres 后端（P1，实验性，env 门控）。
//
// 门控：EYVESCLOUD_PG_DSN 非空时配置库走 Postgres，否则保持 SQLite（默认，行为不变）。
// 复用同一套行级增量落库 SQL：现有语句全是 `?` 占位符，pgx 只认 `$n`，故在 driver
// 层做 `?`→`$n` 重绑定（rebindConnector），避免改动上百处调用点。少数方言差异
// （保留字 user、INSERT OR REPLACE）已就地改成两端通用写法。
//
// 第一刀范围 = 配置库的读写往返（表结构映射）。已知局限：
//   - 表结构由 ensurePostgresSchema 一次建全（含全部历史迁移列），不做逐列 ALTER 迁移；
//   - 自备份（VACUUM INTO）与 WAL 检查点是 SQLite 专有，PG 下分别「明确报错」与「跳过」；
//   - 单实例写路径仍由 dbMu 串行，尚未做分片（P3）。
const pgDSNEnv = "EYVESCLOUD_PG_DSN"

// configDBIsPostgres 标记当前配置库是否为 Postgres 后端，供 SQLite 专有维护路径
// （WAL 检查点 / VACUUM 备份）判断是否跳过。仅在 dbMu 保护下读写。
var configDBIsPostgres bool

// rebind 把 `?` 占位符改写成 pgx 需要的 `$1..$n`。引用段（单引号 / 双引号包裹的
// 字符串与标识符）内的 `?` 原样保留，含 SQL 的相邻引号转义写法。语句里没有 `?`
// 时直接返回原串（零分配热路径）。
func rebind(q string) string {
	if !strings.ContainsRune(q, '?') {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); {
		switch c := q[i]; c {
		case '\'', '"':
			b.WriteByte(c)
			i++
			for i < len(q) {
				b.WriteByte(q[i])
				if q[i] == c {
					if i+1 < len(q) && q[i+1] == c { // '' / "" 转义，继续留在段内
						i++
						b.WriteByte(q[i])
						i++
						continue
					}
					i++
					break
				}
				i++
			}
		case '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// rebindConnector 包一层 driver.Connector，在每条语句送到 pgx 之前重绑定占位符。
type rebindConnector struct{ inner driver.Connector }

func (c rebindConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return rebindConn{Conn: conn}, nil
}

func (c rebindConnector) Driver() driver.Driver { return c.inner.Driver() }

// rebindConn 重绑定后转发给内层连接。Begin 直接由内层提升（store 用 db.Begin()，
// 走的是弃用的无 ctx 路径，pgx 的 BeginTx 无需包装）。
type rebindConn struct{ driver.Conn }

func (c rebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rebind(query))
}

func (c rebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, rebind(query))
	}
	return c.Conn.Prepare(rebind(query))
}

// ExecContext / QueryContext 返回 driver.ErrSkip 时，database/sql 会回退到
// PrepareContext（已重绑定）再执行，故无需再包 Stmt。
func (c rebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if ex, ok := c.Conn.(driver.ExecerContext); ok {
		return ex.ExecContext(ctx, rebind(query), args)
	}
	return nil, driver.ErrSkip
}

func (c rebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if qe, ok := c.Conn.(driver.QueryerContext); ok {
		return qe.QueryContext(ctx, rebind(query), args)
	}
	return nil, driver.ErrSkip
}

// openPostgresConfigDB 打开 Postgres 配置库并建表。调用方（openConfigDB）已持有 dbMu。
func openPostgresConfigDB(dsn string) error {
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("解析 %s 失败: %w", pgDSNEnv, err)
	}
	next := sql.OpenDB(rebindConnector{inner: stdlib.GetConnector(*cc)})
	// SQLite 必须单连接（单写者），Postgres 不需要；所有配置库访问仍由 dbMu 串行。
	next.SetMaxOpenConns(4)
	next.SetMaxIdleConns(4)
	db = next
	configDBIsPostgres = true
	if err := next.Ping(); err != nil {
		return fmt.Errorf("连接 Postgres 配置库失败: %w", err)
	}
	return ensurePostgresSchema()
}

// ensurePostgresSchema 建全配置库表结构（含历史迁移新增列）。幂等（IF NOT EXISTS）。
// 类型映射：INTEGER→BIGINT、REAL→DOUBLE PRECISION、自增主键→IDENTITY；保留字
// "user" 加引号（SQLite 同样接受带引号写法，故运行时 SQL 也统一加引号）。
func ensurePostgresSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS app_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS containers (
			id BIGINT PRIMARY KEY,
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
			node_id TEXT,
			node_local_id BIGINT NOT NULL DEFAULT 0,
			vcpu DOUBLE PRECISION,
			cpu_percent BIGINT NOT NULL DEFAULT 0,
			ram_mb BIGINT,
			disk_gb DOUBLE PRECISION,
			network_bw_mbps BIGINT,
			network_down_mbps BIGINT NOT NULL DEFAULT 0,
			network_up_mbps BIGINT NOT NULL DEFAULT 0,
			monthly_traffic_gb BIGINT,
			traffic_mode TEXT,
			traffic_in_gb BIGINT,
			traffic_out_gb BIGINT,
			traffic_used_rx BIGINT,
			traffic_used_tx BIGINT,
			traffic_reset_date TEXT,
			io_speed_mbps BIGINT,
			io_read_mbps BIGINT NOT NULL DEFAULT 0,
			io_write_mbps BIGINT NOT NULL DEFAULT 0,
			status TEXT,
			restore_on_host_boot BIGINT NOT NULL DEFAULT 0,
			ip TEXT,
			lan_ipv4_mode TEXT,
			lan_interface TEXT,
			lan_ipv4_address TEXT NOT NULL DEFAULT '',
			lan_ipv4_prefix_len BIGINT NOT NULL DEFAULT 0,
			lan_ipv4_gateway TEXT NOT NULL DEFAULT '',
			ipv6 TEXT,
			ipv6_prefix_len BIGINT,
			ipv6_interface TEXT,
			vnc_port BIGINT,
			ssh_port BIGINT,
			ssh_password TEXT,
			ssh_host_key TEXT,
			port_mapping_limit BIGINT,
			snapshot_limit BIGINT,
			created_at TEXT,
			expires_at TEXT,
			snapshot_schedule_enabled BIGINT,
			snapshot_schedule_interval_hours BIGINT,
			snapshot_schedule_time TEXT,
			snapshot_schedule_last_run TEXT,
			snapshot_schedule_next_run TEXT,
			snapshot_schedule_created_by TEXT,
			policy_blocked BIGINT,
			policy_blocked_reason TEXT,
			policy_blocked_at TEXT,
			firewall_enabled BIGINT NOT NULL DEFAULT 0,
			firewall_default_action TEXT NOT NULL DEFAULT 'DROP',
			firewall_rules TEXT,
			allowed_image_ids TEXT,
			image_limit_configured BIGINT NOT NULL DEFAULT 0,
			tenant TEXT NOT NULL DEFAULT '',
			cloud_init_user_data TEXT,
			data_disk_gb DOUBLE PRECISION NOT NULL DEFAULT 0,
			data_disk_mount_path TEXT,
			rescue_enabled BIGINT NOT NULL DEFAULT 0,
			rescue_iso_id TEXT,
			rescue_iso_path TEXT,
			optional_iso_id TEXT,
			optional_iso_path TEXT,
			root_volume_id TEXT,
			data_volume_ids TEXT,
			suspended BIGINT NOT NULL DEFAULT 0,
			suspended_at TEXT,
			suspended_reason TEXT,
			remark TEXT,
			locked BIGINT NOT NULL DEFAULT 0,
			recycled_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS port_mappings (
			container_id BIGINT NOT NULL,
			position BIGINT NOT NULL,
			container_port BIGINT NOT NULL,
			host_port BIGINT NOT NULL,
			host_ip TEXT,
			protocol TEXT,
			description TEXT,
			PRIMARY KEY (container_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS container_public_ipv4s (
			container_id BIGINT NOT NULL,
			position BIGINT NOT NULL,
			address TEXT NOT NULL,
			interface TEXT,
			prefix_len BIGINT,
			gateway TEXT,
			rdns TEXT,
			PRIMARY KEY (container_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS container_ipv6_addresses (
			container_id BIGINT NOT NULL,
			position BIGINT NOT NULL,
			address TEXT NOT NULL,
			prefix_len BIGINT,
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
			access_code_password TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			token_version BIGINT,
			allowed_image_ids TEXT,
			image_limit_configured BIGINT NOT NULL DEFAULT 0,
			role TEXT NOT NULL DEFAULT 'operator',
			tenant TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sub_user_container_names (
			sub_user_id TEXT NOT NULL,
			position BIGINT NOT NULL,
			container_name TEXT NOT NULL,
			PRIMARY KEY (sub_user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS sub_user_container_uuids (
			sub_user_id TEXT NOT NULL,
			position BIGINT NOT NULL,
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
			disabled BIGINT,
			container_uuids TEXT,
			last_used_ip TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			time TEXT,
			action TEXT,
			target TEXT,
			detail TEXT,
			"user" TEXT,
			ip TEXT,
			user_agent TEXT,
			success_set BIGINT,
			success BIGINT,
			error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS security_conntrack_snapshots (
			id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			container_ip TEXT NOT NULL,
			line TEXT NOT NULL,
			captured_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conntrack_snapshots_ip_time
			ON security_conntrack_snapshots(container_ip, captured_at)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			type TEXT,
			container_id BIGINT,
			container_name TEXT,
			status TEXT,
			error TEXT,
			created_at TEXT,
			template_id TEXT,
			"user" TEXT,
			ip TEXT,
			user_agent TEXT,
			cfg_name TEXT,
			cfg_virtualization TEXT,
			cfg_template_id TEXT,
			cfg_vcpu DOUBLE PRECISION,
			cfg_cpu_percent BIGINT,
			cfg_ram_mb BIGINT,
			cfg_disk_gb DOUBLE PRECISION,
			cfg_network_bw_mbps BIGINT,
			cfg_network_down_mbps BIGINT NOT NULL DEFAULT 0,
			cfg_network_up_mbps BIGINT NOT NULL DEFAULT 0,
			cfg_monthly_traffic_gb BIGINT,
			cfg_traffic_mode TEXT,
			cfg_traffic_in_gb BIGINT,
			cfg_traffic_out_gb BIGINT,
			cfg_io_speed_mbps BIGINT,
			cfg_io_read_mbps BIGINT NOT NULL DEFAULT 0,
			cfg_io_write_mbps BIGINT NOT NULL DEFAULT 0,
			cfg_management_port BIGINT NOT NULL DEFAULT 0,
			cfg_port_mapping_count BIGINT,
			cfg_assign_nat BIGINT,
			cfg_lan_ipv4_mode TEXT,
			cfg_lan_interface TEXT,
			cfg_lan_ipv4_address TEXT NOT NULL DEFAULT '',
			cfg_lan_ipv4_prefix_len BIGINT NOT NULL DEFAULT 0,
			cfg_lan_ipv4_gateway TEXT NOT NULL DEFAULT '',
			cfg_snapshot_limit BIGINT,
			cfg_assign_ipv4 BIGINT,
			cfg_ipv4_count BIGINT,
			cfg_public_ipv4s TEXT,
			cfg_assign_ipv6 BIGINT,
			cfg_ipv6_count BIGINT,
			cfg_ipv6_addresses TEXT,
			cfg_ssh_auth_mode TEXT,
			cfg_ssh_password TEXT,
			cfg_ssh_public_key TEXT,
			cfg_allowed_image_ids TEXT,
			cfg_image_limit_configured BIGINT NOT NULL DEFAULT 0,
			cfg_expires_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS task_extra_ports (
			task_id TEXT NOT NULL,
			position BIGINT NOT NULL,
			port BIGINT NOT NULL,
			PRIMARY KEY (task_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS task_nat_port_mappings (
			task_id TEXT NOT NULL,
			position BIGINT NOT NULL,
			host_port BIGINT NOT NULL,
			container_port BIGINT NOT NULL,
			protocol TEXT,
			description TEXT,
			PRIMARY KEY (task_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS login_logs (
			id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			time TEXT,
			username TEXT,
			ip TEXT,
			user_agent TEXT,
			success BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS enabled_images (
			position BIGINT PRIMARY KEY,
			image_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS snapshots (
			id TEXT PRIMARY KEY,
			container_id BIGINT,
			container_name TEXT,
			lxc_name TEXT,
			created_at TEXT,
			created_by TEXT,
			scheduled BIGINT,
			path TEXT,
			size_bytes BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS volumes (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			size_mb BIGINT NOT NULL DEFAULT 0,
			attached_to_container_id BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_volumes_container ON volumes (attached_to_container_id)`,
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
			cpu_count BIGINT NOT NULL DEFAULT 0,
			ram_total_mb BIGINT NOT NULL DEFAULT 0,
			ram_used_mb BIGINT NOT NULL DEFAULT 0,
			disk_total_gb DOUBLE PRECISION NOT NULL DEFAULT 0,
			disk_used_gb DOUBLE PRECISION NOT NULL DEFAULT 0,
			container_count BIGINT NOT NULL DEFAULT 0,
			region_id TEXT NOT NULL DEFAULT '',
			node_group_id TEXT NOT NULL DEFAULT '',
			cluster_id TEXT NOT NULL DEFAULT '',
			virt_types TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			maintenance_mode BIGINT NOT NULL DEFAULT 0,
			maintenance_since TEXT NOT NULL DEFAULT '',
			tls_skip_verify BIGINT NOT NULL DEFAULT 0,
			allow_private_addr BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_region ON nodes (region_id)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_node_group ON nodes (node_group_id)`,
		`CREATE TABLE IF NOT EXISTS node_leases (
			node_id TEXT PRIMARY KEY,
			token TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			in_progress BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS task_history (
			id            TEXT PRIMARY KEY,
			type          TEXT,
			container_id  BIGINT,
			container_name TEXT,
			status        TEXT,
			error         TEXT,
			stage         TEXT,
			stage_detail  TEXT,
			percent       BIGINT NOT NULL DEFAULT 0,
			"user"        TEXT,
			ip            TEXT,
			user_agent    TEXT,
			created_at    TEXT,
			started_at    TEXT,
			ended_at      TEXT,
			duration_ms   BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_created ON task_history (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_status ON task_history (status)`,
		`CREATE INDEX IF NOT EXISTS idx_task_history_container ON task_history (container_id)`,
		`CREATE TABLE IF NOT EXISTS task_logs (
			id         BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			task_id    TEXT NOT NULL,
			level      TEXT NOT NULL,
			message    TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_logs_task ON task_logs (task_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create postgres schema: %w", err)
		}
	}
	return nil
}
