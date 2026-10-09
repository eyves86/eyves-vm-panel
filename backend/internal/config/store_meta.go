package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---- P1：app_meta 行级增量落库 ----
//
// saveMeta 此前是「DELETE FROM app_meta + 重插全部 ~80 个键」。行数虽有界，却是
// 每次保存的固定成本（实测 ~1.2-1.5ms，占「空声明保存」下限的绝大头）：30k 节点
// 心跳约 500 次/秒时，光这份固定成本就要 ~0.6 核。更糟的是 nodes 这类大值键——
// 30k 节点的 JSON 拼接 + 逐节点 Token 的 AES-GCM 加密，即使一个节点都没变也要重算。
//
// 这里给每个键一个「明文源」（src）：src 指纹未变 → 不序列化、不加密、不写 SQL，
// 直接沿用上次指纹；变了 → upsert；本次不再产出的历史键 → delete。
//
// 指纹取 src（明文）而非落库串：Token/TOTP/JWT 等落库走 AES-256-GCM，密文含随机
// nonce，按落库串取指纹会「每次都变了」。明文未变 ⇒ 库里旧密文仍可解密，跳过写入安全。
//
// 两条安全前提：
//  1. build 必须是 src 的纯函数（同 src ⇒ 同落库值）。由
//     TestMetaEntriesCoverEveryPersistedKey / TestMetaSaveWritesOnlyChangedKeys 兜底：
//     每个键都要在库里出现一次、且只有真变了才重写。
//  2. 敏感键的库中存量若是「加密保护上线前」写入的明文，必须强制重写一次完成透明
//     重加密（storedSane）——否则跳过写入会让明文长期滞留磁盘。

// metaForceRewrite 是一个不可能等于真实指纹的哨兵值（指纹都是十六进制串）。
const metaForceRewrite = "!"

// metaEntry 描述一个 app_meta 键。
type metaEntry struct {
	key string
	// src 返回参与「是否变化」判定的明文源，必须覆盖 build 的全部输入。
	src func() any
	// build 产出实际落库的字符串（可能含 AES-GCM 加密）。必须是 src 的纯函数。
	build func() (string, error)
	// storedSane 非空时用于播种校验：给定库中原始串，判断它是否已是可接受的落库
	// 形态。返回 false 表示存量是历史明文，需强制重写一次。
	storedSane func(raw string) bool
}

func textKey(key string, get func() string) metaEntry {
	return metaEntry{
		key:   key,
		src:   func() any { return get() },
		build: func() (string, error) { return get(), nil },
	}
}

func boolKey(key string, get func() bool) metaEntry {
	return metaEntry{
		key:   key,
		src:   func() any { return get() },
		build: func() (string, error) { return btoa(get()), nil },
	}
}

func intKey(key string, get func() int) metaEntry {
	return metaEntry{
		key:   key,
		src:   func() any { return get() },
		build: func() (string, error) { return strconv.Itoa(get()), nil },
	}
}

func floatKey(key string, get func() float64) metaEntry {
	return metaEntry{
		key:   key,
		src:   func() any { return get() },
		build: func() (string, error) { return strconv.FormatFloat(get(), 'f', -1, 64), nil },
	}
}

// jsonKey 以明文源为唯一事实：src 与 build 读同一个 get()。
func jsonKey(key string, get func() any) metaEntry {
	return metaEntry{
		key: key,
		src: get,
		build: func() (string, error) {
			b, err := json.Marshal(get())
			if err != nil {
				return "", fmt.Errorf("序列化 app_meta[%s] 失败: %w", key, err)
			}
			return string(b), nil
		},
	}
}

// secretKey：src 为明文，落库串为 AES-GCM 密文（空串原样返回）。
func secretKey(key string, get func() string) metaEntry {
	return metaEntry{
		key:        key,
		src:        func() any { return get() },
		build:      func() (string, error) { return EncryptNodeToken(get()) },
		storedSane: secretStoredSane,
	}
}

// jsonSecretKey：src 为明文结构（指纹稳定，不因每次加密的随机 nonce 而抖动），
// 落库串为该结构的 JSON 但 secretFields 列出的字段（json tag 名）已 AES-GCM 加密。
// 适用于「一个 JSON 值里混有普通设置与凭据字段」的配置——整体明文落库会让凭据
// 明文躺在配置库里（Webhooks[].secret 可伪造回调；SMTPSettings.password /
// NotificationConfig.smtp_password 可劫持邮件）。
//
// storedSane 逐字段校验：任一列出字段为非空明文即判为历史明文，强制重写一次完成
// 透明重加密（与 secretKey 的存量迁移同一机制）。
func jsonSecretKey(key string, get func() any, secretFields ...string) metaEntry {
	return metaEntry{
		key: key,
		src: func() any { return get() },
		build: func() (string, error) {
			return marshalEncryptingFields(get(), secretFields)
		},
		storedSane: func(raw string) bool { return jsonFieldsEncryptedSane(raw, secretFields) },
	}
}

// secretStoredSane：空串或 enc:v1: 密文都算「已是可接受的落库形态」；
// 其它形态（历史明文）需要重写一次以完成透明重加密。
func secretStoredSane(raw string) bool {
	return raw == "" || strings.HasPrefix(raw, nodeTokenEncPrefix)
}

// metaEntries 返回全部需要落库的 app_meta 键。**这是 app_meta 的唯一产出清单**：
// 不在此列的键会在下一次保存时被清理（与旧「整表重写」语义一致）。
func metaEntries(cfg *EyvescloudConfig) []metaEntry {
	// us 归一化更新源：platform/owner/repo/branch/asset_prefix 落明文，token 加密。
	us := func() UpdateSource { return NormalizeUpdateSource(cfg.UpdateSource) }
	// updated_at 每次保存生成一次，秒级精度：同一秒内的多次保存不产生写入。
	nowStr := time.Now().Format("2006-01-02 15:04:05")

	return []metaEntry{
		textKey("admin_user", func() string { return cfg.AdminUser }),
		textKey("admin_pass_hash", func() string { return cfg.AdminPassHash }),
		// AdminTokenVersion 必须落库（渗透测试 F-01）：不落库时改密/重置后重启
		// 面板会把版本重置为 0，导致**已吊销的旧管理员 JWT 复活**。
		intKey("admin_token_version", func() int { return cfg.AdminTokenVersion }),
		secretKey("admin_totp_secret", func() string { return cfg.AdminTOTPSecret }),
		boolKey("admin_totp_enabled", func() bool { return cfg.AdminTOTPEnabled }),
		jsonKey("admin_backup_codes", func() any { return cfg.AdminBackupCodes }),
		jsonKey("admins", func() any { return cfg.Admins }),
		textKey("admin_path", func() string { return cfg.AdminPath }),
		secretKey("jwt_secret", func() string { return cfg.JWTSecret }),
		intKey("port", func() int { return cfg.Port }),
		textKey("data_dir", func() string { return cfg.DataDir }),
		intKey("next_container_id", func() int { return cfg.NextContainerID }),
		intKey("next_vnc_port", func() int { return cfg.NextVNCPort }),
		intKey("next_ssh_port", func() int { return cfg.NextSSHPort }),
		intKey("nat_port_start", func() int { return cfg.NATPortStart }),
		intKey("nat_port_end", func() int { return cfg.NATPortEnd }),
		textKey("lxc_nat_subnet", func() string { return cfg.LXCNATSubnet }),
		textKey("kvm_nat_subnet", func() string { return cfg.KVMNATSubnet }),
		boolKey("setup_complete", func() bool { return cfg.SetupComplete }),
		boolKey("security_auto_shutdown", func() bool { return cfg.SecurityAutoShutdown }),
		boolKey("arp_protection_enabled", func() bool { return cfg.ARPProtectionEnabled }),
		boolKey("ip_anti_spoof_enabled", func() bool { return cfg.IPAntiSpoofEnabled }),
		boolKey("abuse_detection_enabled", func() bool { return cfg.AbuseDetectionEnabled }),
		intKey("task_concurrency", func() int { return cfg.TaskConcurrency }),
		intKey("task_queue_max_pending", func() int { return cfg.TaskQueueMaxPending }),
		textKey("language", func() string { return NormalizeLanguage(cfg.Language) }),
		textKey("login_footer_text", func() string { return cfg.LoginFooterText }),
		boolKey("login_footer_hidden", func() bool { return cfg.LoginFooterHidden }),
		textKey("brand_name", func() string { return cfg.BrandName }),
		textKey("brand_logo", func() string { return cfg.BrandLogo }),
		textKey("brand_favicon", func() string { return cfg.BrandFavicon }),
		textKey("brand_login_title", func() string { return cfg.BrandLoginTitle }),
		boolKey("brand_powered_hidden", func() bool { return cfg.BrandPoweredHidden }),
		textKey("panel_domain", func() string { return cfg.PanelDomain }),
		// TurnstileSecretKey 与节点 Token 同级敏感（密文落库）；SiteKey 本身公开
		// （前端渲染 widget 需要），无需加密。
		textKey("turnstile_site_key", func() string { return cfg.TurnstileSiteKey }),
		secretKey("turnstile_secret_key", func() string { return cfg.TurnstileSecretKey }),
		boolKey("turnstile_admin_login", func() bool { return cfg.TurnstileAdminLogin }),
		boolKey("turnstile_user_login", func() bool { return cfg.TurnstileUserLogin }),
		// 节点对接密钥：一次性、24h TTL，密文落库减小泄露窗口。
		secretKey("agent_pairing_key", func() string { return cfg.AgentPairingKey }),
		textKey("agent_pairing_key_expiry", func() string { return cfg.AgentPairingKeyExpiry }),
		textKey("update_source_platform", func() string { return us().Platform }),
		textKey("update_source_owner", func() string { return us().Owner }),
		textKey("update_source_repo", func() string { return us().Repo }),
		textKey("update_source_branch", func() string { return us().Branch }),
		textKey("update_source_asset_prefix", func() string { return us().AssetPrefix }),
		{
			key:        "update_source_token",
			src:        func() any { return us() },
			build:      func() (string, error) { return EncryptNodeToken(us().Token) },
			storedSane: secretStoredSane,
		},
		jsonKey("ssl", func() any { return cfg.SSL }),
		jsonKey("ssl_certificates", func() any { return cfg.SSLCertificates }),
		jsonKey("public_ipv4_pool", func() any { return cfg.PublicIPv4Pool }),
		jsonKey("public_ipv6_prefixes", func() any { return cfg.PublicIPv6Prefixes }),
		jsonKey("webssh_allowed_origins", func() any { return cfg.WebSSHAllowedOrigins }),
		jsonKey("panel_access_policy", func() any { return cfg.PanelAccessPolicy }),
		jsonKey("storage_pools", func() any { return cfg.StoragePools }),
		jsonKey("custom_kvm_images", func() any { return cfg.CustomKVMImages }),
		jsonKey("custom_lxc_images", func() any { return cfg.CustomLXCImages }),
		jsonKey("policy_rules", func() any { return cfg.PolicyRules }),
		jsonKey("policy_history", func() any { return cfg.PolicyHistory }),
		// 安全组此前**完全没有落库**——字段只存在于内存，面板一重启所有安全组与
		// 规则凭空消失（生产 DB 的 78 个 app_meta 键里没有任何 sec_group*）。
		jsonKey("sec_groups", func() any { return cfg.SecGroups }),
		jsonKey("sec_group_rules", func() any { return cfg.SecGroupRules }),
		boolKey("security_group_enforced", func() bool { return cfg.SecurityGroupEnforced }),
		// 节点自 P2 起独立成表（每节点一行，见 store_db.go 的 loadNodes /
		// upsertNodeRow 与 store_rows.go 的 diffNodes）：不再作为单个 app_meta 大值键。
		// 30k 节点时该键序列化 + 逐节点 AES-GCM 加密要 129ms、提交 9.5MB 值要 65ms，
		// 且节点任何一点遥测变化都要整键重写。历史键由 migrateLegacyNodesRow 搬迁后删除。
		jsonKey("regions", func() any { return cfg.Regions }),
		jsonKey("ip_groups", func() any { return cfg.IPGroups }),
		jsonKey("iso_files", func() any { return cfg.ISOFiles }),
		intKey("metric_retention_days", func() int { return cfg.MetricRetentionDays }),
		intKey("audit_retention_days", func() int { return cfg.AuditRetentionDays }),
		jsonKey("backup_settings", func() any { return cfg.BackupSettings }),
		jsonKey("instance_backup_settings", func() any { return cfg.InstanceBackupSettings }),
		jsonKey("remote_backup_settings", func() any { return cfg.RemoteBackupSettings }),
		// smtp_settings 的 password 是 SMTP 账号凭据：混在普通设置里，必须单独加密
		// （此前整值明文落库 = 邮箱口令明文躺在配置库）。
		jsonSecretKey("smtp_settings", func() any { return cfg.SMTPSettings }, "password"),
		jsonKey("backups", func() any { return cfg.Backups }),
		jsonKey("instance_backups", func() any { return cfg.InstanceBackups }),
		jsonKey("backup_plans", func() any { return cfg.BackupPlans }),
		jsonKey("api_rate_limit", func() any { return cfg.APIRateLimit }),
		jsonKey("tenants", func() any { return cfg.Tenants }),
		boolKey("memory_overcommit_enabled", func() bool { return cfg.MemoryOvercommitEnabled }),
		floatKey("memory_overcommit_ratio", func() float64 { return cfg.MemoryOvercommitRatio }),
		boolKey("nat_subnet_oversubscription", func() bool { return cfg.NATSubnetOversubscription }),
		floatKey("disk_overcommit_ratio", func() float64 { return cfg.DiskOvercommitRatio }),
		jsonKey("ksm_tuning", func() any { return cfg.KSMTuning }),
		textKey("schema_version", func() string { return "1" }),
		{
			key:   "updated_at",
			src:   func() any { return nowStr },
			build: func() (string, error) { return nowStr, nil },
		},

		// ---- 企业集成 / 运维集合 ----
		//
		// 以下 7 个集合此前**完全没有任何落库路径**（既不在本清单，也没有独立表），
		// 且 loadConfigFromDB 也不读：管理员加的 SSH 公钥、事件回调、脚本模板、定时
		// 启停、节点分组、集群、告警设置，在面板重启后全部凭空消失（典型假 UI：
		// 接口返回成功、界面照常显示，数据却只剩内存）。这里补上持久化。
		//
		// webhooks / notifications 内嵌凭据（HMAC 密钥 / SMTP 口令），用 jsonSecretKey
		// 单独加密落库。
		jsonKey("ssh_keys", func() any { return cfg.SSHKeys }),
		jsonSecretKey("webhooks", func() any { return cfg.Webhooks }, "secret"),
		jsonKey("recipes", func() any { return cfg.Recipes }),
		jsonKey("scheduled_actions", func() any { return cfg.ScheduledActions }),
		jsonKey("node_groups", func() any { return cfg.NodeGroups }),
		jsonKey("clusters", func() any { return cfg.Clusters }),
		jsonKey("cells", func() any { return cfg.Cells }),
		jsonSecretKey("notifications", func() any { return cfg.Notifications }, "smtp_password"),
	}
}

// seedMetaFingerprints 由「加载到的内存明文 + 库中实际存在的键」播种 app_meta 指纹。
//
// 只播种库里确实存在的键：库里没有的键（新版本新增 / 老库缺键）不播种，下一次保存
// 会写进去。库中存在但不再由 metaEntries 产出的历史键播种为 metaForceRewrite，
// 使下一次保存把它删掉（与旧「整表重写」等价）。
func seedMetaFingerprints(cfg *EyvescloudConfig, dbMeta map[string]string) map[string]string {
	entries := metaEntries(cfg)
	out := make(map[string]string, len(dbMeta))
	known := make(map[string]bool, len(entries))
	for i := range entries {
		known[entries[i].key] = true
	}
	for i := range entries {
		e := &entries[i]
		raw, inDB := dbMeta[e.key]
		if !inDB {
			continue
		}
		if e.storedSane != nil && !e.storedSane(raw) {
			// 存量历史明文：强制重写一次，完成透明重加密。
			out[e.key] = metaForceRewrite
			continue
		}
		out[e.key] = fingerprint(e.src())
	}
	for k := range dbMeta {
		if !known[k] {
			out[k] = metaForceRewrite
		}
	}
	return out
}

// diffMeta 逐键对比明文指纹：未变化的键不序列化、不加密、不写任何 SQL；
// 变化的键 upsert；本次不再产出的历史键 delete。
func diffMeta(tx *sql.Tx, next *rowFingerprints) error {
	entries := metaEntries(AppConfig)
	for i := range entries {
		e := &entries[i]
		fp := fingerprint(e.src())
		next.meta[e.key] = fp
		if persistedRows.meta[e.key] == fp {
			continue
		}
		v, err := e.build()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO app_meta(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, e.key, v); err != nil {
			return err
		}
	}
	for k := range persistedRows.meta {
		if _, ok := next.meta[k]; ok {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM app_meta WHERE key = ?`, k); err != nil {
			return err
		}
	}
	return nil
}
