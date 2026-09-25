package server

import (
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"eyvescloud/internal/api"
	"eyvescloud/internal/config"
)

// clientAddress 返回真实客户端 IP，用于 API 治理限流。
// 与 api.clientIP 完全同逻辑：仅当直接对端落在 TrustedProxies 范围内时
// 才信任 X-Forwarded-For / X-Real-IP / CF-Connecting-IP。
func clientAddress(r *http.Request) string {
	direct := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(direct); err == nil {
		direct = host
	}
	direct = strings.TrimPrefix(strings.TrimSuffix(direct, "]"), "[")

	if forwarded, ok := config.ResolveForwardedIPForRequest(direct, r); ok {
		return forwarded
	}
	return direct
}

// webFS holds embedded frontend files
var webFS http.FileSystem

// corsMiddleware adds CORS and security headers
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && config.IsOriginAllowed(origin, r.Host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")

		// Security headers (XSS / clickjacking / MIME-sniffing / referrer leakage)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
				"font-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		// Browsers only honor HSTS on HTTPS responses; the header is harmless on HTTP.
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		if r.Method == http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && !config.IsOriginAllowed(origin, r.Host) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// setupRoutes configures API and static routes
func setupRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("/api/login", corsMiddleware(api.HandleLogin))
	mux.HandleFunc("/api/language", corsMiddleware(api.HandleLanguage))
	mux.HandleFunc("/api/check-auth", corsMiddleware(api.AuthMiddleware(api.HandleCheckAuth)))
	mux.HandleFunc("/api/change-password", corsMiddleware(api.AdminSessionMiddleware(api.HandleAdminPasswordChange)))
	mux.HandleFunc("/api/change-username", corsMiddleware(api.AdminSessionMiddleware(api.HandleAdminUsernameChange)))
	mux.HandleFunc("/api/login-logs", corsMiddleware(api.AdminMiddleware(api.HandleLoginLogs)))
	mux.HandleFunc("/api/ssl", corsMiddleware(api.AdminMiddleware(api.HandleSSLSettings)))
	mux.HandleFunc("/api/webssh-origins", corsMiddleware(api.AdminMiddleware(api.HandleWebSSHOriginSettings)))
	mux.HandleFunc("/api/access-policy", corsMiddleware(api.AdminMiddleware(api.HandlePanelAccessPolicy)))
	// 管理员入口路径（可自定义）：仅主管理员会话可读写。
	mux.HandleFunc("/api/admin-path", corsMiddleware(api.AuthMiddleware(api.HandleAdminPathSettings)))
	// 管理员两步验证（TOTP / Google Authenticator）
	mux.HandleFunc("/api/2fa/status", corsMiddleware(api.AdminSessionMiddleware(api.Handle2FAStatus)))
	mux.HandleFunc("/api/2fa/setup", corsMiddleware(api.AdminSessionMiddleware(api.Handle2FASetup)))
	mux.HandleFunc("/api/2fa/enable", corsMiddleware(api.AdminSessionMiddleware(api.Handle2FAEnable)))
	mux.HandleFunc("/api/2fa/disable", corsMiddleware(api.AdminSessionMiddleware(api.Handle2FADisable)))
	mux.HandleFunc("/api/2fa/regenerate-backup-codes", corsMiddleware(api.AdminSessionMiddleware(api.Handle2FARegenerateBackupCodes)))
	mux.HandleFunc("/api/containers", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleContainers))))
	mux.HandleFunc("/api/containers/list", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleContainerListAlias))))
	mux.HandleFunc("/api/containers/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSingleContainer))))
	mux.HandleFunc("/api/templates", corsMiddleware(api.AuthMiddleware(api.HandleTemplates)))
	mux.HandleFunc("/api/images", corsMiddleware(api.AdminMiddleware(api.HandleImages)))
	mux.HandleFunc("/api/images/custom", corsMiddleware(api.AdminMiddleware(api.HandleCustomKVMImages)))
	mux.HandleFunc("/api/images/download", corsMiddleware(api.AdminMiddleware(api.HandleImageDownload)))
	mux.HandleFunc("/api/images/cancel", corsMiddleware(api.AdminMiddleware(api.HandleImageCancel)))
	mux.HandleFunc("/api/images/delete", corsMiddleware(api.AdminMiddleware(api.HandleImageDelete)))
	mux.HandleFunc("/api/images/toggle", corsMiddleware(api.AdminMiddleware(api.HandleImageToggle)))
	mux.HandleFunc("/api/images/enabled", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleEnabledImages))))
	mux.HandleFunc("/api/dashboard", corsMiddleware(api.AdminMiddleware(api.HandleDashboard)))
	mux.HandleFunc("/api/host-info", corsMiddleware(api.AdminMiddleware(api.HandleHostInfo)))
	mux.HandleFunc("/api/host-history", corsMiddleware(api.AdminMiddleware(api.HandleHostHistory)))
	mux.HandleFunc("/api/monitoring/containers", corsMiddleware(api.AdminMiddleware(api.HandleContainerMonitoring)))
	mux.HandleFunc("/api/host-report", corsMiddleware(api.AdminMiddleware(api.HandleHostReport)))
	mux.HandleFunc("/api/snapshots", corsMiddleware(api.AdminMiddleware(api.HandleSnapshots)))
	mux.HandleFunc("/api/routing/ipv4-scan", corsMiddleware(api.AdminMiddleware(api.HandleRoutingIPv4Scan)))
	mux.HandleFunc("/api/routing", corsMiddleware(api.AdminMiddleware(api.HandleRouting)))
	mux.HandleFunc("/api/storage", corsMiddleware(api.AdminMiddleware(api.HandleStorage)))
	mux.HandleFunc("/api/ipv6/status", corsMiddleware(api.AdminMiddleware(api.HandleIPv6Status)))
	mux.HandleFunc("/api/tasks", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTasks))))
	mux.HandleFunc("/api/tasks/", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleTaskDelete))))
	mux.HandleFunc("/api/task-queue/settings", corsMiddleware(api.AdminMiddleware(api.HandleTaskQueueSettings)))
	mux.HandleFunc("/api/batch-create", corsMiddleware(api.AdminMiddleware(api.HandleBatchCreate)))
	mux.HandleFunc("/api/batch-action", corsMiddleware(api.AdminMiddleware(api.HandleBatchAction)))
	mux.HandleFunc("/api/sub-user/create", corsMiddleware(api.AdminMiddleware(api.HandleSubUserCreate)))
	mux.HandleFunc("/api/sub-user/login", corsMiddleware(api.HandleSubUserLogin))
	mux.HandleFunc("/api/sub-user/access", corsMiddleware(api.HandleSubUserAccessCode))
	mux.HandleFunc("/api/sub-user/change-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserChangePassword)))
	mux.HandleFunc("/api/sub-users", corsMiddleware(api.AdminMiddleware(api.HandleSubUserList)))
	mux.HandleFunc("/api/sub-users/", corsMiddleware(api.AdminMiddleware(api.HandleSubUserAction)))
	mux.HandleFunc("/api/audit-logs", corsMiddleware(api.AdminMiddleware(api.HandleAuditLogs)))
	mux.HandleFunc("/api/security/alerts", corsMiddleware(api.AdminMiddleware(api.HandleSecurityAlerts)))
	mux.HandleFunc("/api/security/check", corsMiddleware(api.AdminMiddleware(api.HandleSecurityCheck)))
	mux.HandleFunc("/api/security/logs", corsMiddleware(api.AdminMiddleware(api.HandleSecurityLogs)))
	mux.HandleFunc("/api/security/summary", corsMiddleware(api.AdminMiddleware(api.HandleContainerSecuritySummary)))
	mux.HandleFunc("/api/security/abuse-summary", corsMiddleware(api.AdminMiddleware(api.HandleAbuseSummary)))
	mux.HandleFunc("/api/security/settings", corsMiddleware(api.AdminMiddleware(api.HandleSecuritySettings)))
	mux.HandleFunc("/api/notifications", corsMiddleware(api.AdminMiddleware(api.HandleNotificationSettings)))
	mux.HandleFunc("/api/notifications/test", corsMiddleware(api.AdminMiddleware(api.HandleNotificationTest)))
	mux.HandleFunc("/api/ssh-ticket", corsMiddleware(api.AuthMiddleware(api.HandleWebSSHTicket)))
	mux.HandleFunc("/api/ssh", api.HandleWebSSH) // WebSocket
	mux.HandleFunc("/api/vnc-ticket", corsMiddleware(api.AuthMiddleware(api.HandleVNCTicket)))
	mux.HandleFunc("/api/vnc", api.HandleVNCProxy) // WebSocket

	// API Key management
	mux.HandleFunc("/api/api-keys", corsMiddleware(api.AdminMiddleware(api.HandleApiKeys)))
	mux.HandleFunc("/api/api-keys/", corsMiddleware(api.AdminMiddleware(api.HandleApiKeyDelete)))
	// 管理员账号管理（多管理员）：仅管理员账号会话可用，角色在 handler 内按 rbac 校验。
	mux.HandleFunc("/api/admins", corsMiddleware(api.AuthMiddleware(api.HandleAdmins)))
	mux.HandleFunc("/api/admins/", corsMiddleware(api.AuthMiddleware(api.HandleAdminItem)))
	mux.HandleFunc("/api/policies", corsMiddleware(api.AdminMiddleware(api.HandlePolicies)))
	mux.HandleFunc("/api/policies/", corsMiddleware(api.AdminMiddleware(api.HandlePolicyItem)))
	mux.HandleFunc("/api/migrate/import", corsMiddleware(api.AdminMiddleware(api.HandleMigrateImport)))

	// 企业化：审计合规（导出默认不支持导出全量，保留期设置）
	mux.HandleFunc("/api/audit-logs/export", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleAuditLogExport))))
	mux.HandleFunc("/api/audit/settings", corsMiddleware(api.AdminMiddleware(api.HandleAuditSettings)))

	// 企业化：容灾恢复（配置备份）
	mux.HandleFunc("/api/backup/settings", corsMiddleware(api.AdminMiddleware(api.HandleBackupSettings)))
	mux.HandleFunc("/api/instance-backup/settings", corsMiddleware(api.AdminMiddleware(api.HandleInstanceBackupSettings)))
	mux.HandleFunc("/api/backup", corsMiddleware(api.AdminMiddleware(api.HandleBackupCreate)))
	mux.HandleFunc("/api/backup/list", corsMiddleware(api.AdminMiddleware(api.HandleBackupList)))
	mux.HandleFunc("/api/backup/download", corsMiddleware(api.AdminMiddleware(api.HandleBackupDownload)))
	mux.HandleFunc("/api/backup/restore", corsMiddleware(api.AdminMiddleware(api.HandleBackupRestore)))

	// 企业化：可观测性（健康检查）
	mux.HandleFunc("/api/health", corsMiddleware(api.HandleHealth))
	mux.HandleFunc("/api/health/detail", corsMiddleware(api.AdminMiddleware(api.HandleHealthDetail)))

	// 企业化：API 治理（契约 / 限流）
	mux.HandleFunc("/api/openapi.json", corsMiddleware(api.HandleOpenAPI))
	mux.HandleFunc("/api/rate-limit/settings", corsMiddleware(api.AdminMiddleware(api.HandleRateLimitSettings)))

	// 企业化：多租户（租户 / 配额）
	mux.HandleFunc("/api/tenants", corsMiddleware(api.AdminMiddleware(api.HandleTenants)))
	mux.HandleFunc("/api/tenants/", corsMiddleware(api.AdminMiddleware(api.HandleTenantItem)))

	// 也暴露到版本化命名空间便于外部集成
	mux.HandleFunc("/api/v1/audit-logs/export", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleAuditLogExport))))
	mux.HandleFunc("/api/v1/audit/settings", corsMiddleware(api.AdminMiddleware(api.HandleAuditSettings)))
	mux.HandleFunc("/api/v1/backup/settings", corsMiddleware(api.AdminMiddleware(api.HandleBackupSettings)))
	mux.HandleFunc("/api/v1/instance-backup/settings", corsMiddleware(api.AdminMiddleware(api.HandleInstanceBackupSettings)))
	mux.HandleFunc("/api/v1/backup", corsMiddleware(api.AdminMiddleware(api.HandleBackupCreate)))
	mux.HandleFunc("/api/v1/backup/list", corsMiddleware(api.AdminMiddleware(api.HandleBackupList)))
	mux.HandleFunc("/api/v1/backup/download", corsMiddleware(api.AdminMiddleware(api.HandleBackupDownload)))
	mux.HandleFunc("/api/v1/backup/restore", corsMiddleware(api.AdminMiddleware(api.HandleBackupRestore)))
	mux.HandleFunc("/api/v1/health", corsMiddleware(api.HandleHealth))
	mux.HandleFunc("/api/v1/health/detail", corsMiddleware(api.AdminMiddleware(api.HandleHealthDetail)))
	mux.HandleFunc("/api/v1/openapi.json", corsMiddleware(api.HandleOpenAPI))
	mux.HandleFunc("/api/v1/rate-limit/settings", corsMiddleware(api.AdminMiddleware(api.HandleRateLimitSettings)))
	mux.HandleFunc("/api/v1/tenants", corsMiddleware(api.AdminMiddleware(api.HandleTenants)))
	mux.HandleFunc("/api/v1/tenants/", corsMiddleware(api.AdminMiddleware(api.HandleTenantItem)))

	// 主控（Controller）节点管理
	mux.HandleFunc("/api/nodes", corsMiddleware(api.AdminMiddleware(api.HandleNodes)))
	mux.HandleFunc("/api/nodes/", corsMiddleware(api.HandleNodeSubRoutes))
	mux.HandleFunc("/api/node-groups", corsMiddleware(api.AdminMiddleware(api.HandleNodeGroups)))
	mux.HandleFunc("/api/node-groups/", corsMiddleware(api.AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/node-groups/")
		if path == "" {
			api.HandleNodeGroups(w, r)
			return
		}
		api.HandleNodeGroupItem(w, r, path)
	})))
	mux.HandleFunc("/api/clusters", corsMiddleware(api.AdminMiddleware(api.HandleClusters)))
	mux.HandleFunc("/api/clusters/", corsMiddleware(api.AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/clusters/")
		if path == "" {
			api.HandleClusters(w, r)
			return
		}
		api.HandleClusterItem(w, r, path)
	})))
	mux.HandleFunc("/api/nodes/binary", corsMiddleware(api.HandleNodeBinary))

	// 批次3 网络管理：区域 / IP组与故障切换 / ISO 目录
	mux.HandleFunc("/api/metrics/retention", corsMiddleware(api.AdminMiddleware(api.HandleMetricRetentionSettings)))
	mux.HandleFunc("/api/overcommit/settings", corsMiddleware(api.AdminMiddleware(api.HandleOvercommitSettings)))
	mux.HandleFunc("/api/regions", corsMiddleware(api.AdminMiddleware(api.HandleRegions)))
	mux.HandleFunc("/api/regions/", corsMiddleware(api.AdminMiddleware(api.HandleRegionItem)))
	mux.HandleFunc("/api/ip-groups", corsMiddleware(api.AdminMiddleware(api.HandleIPGroups)))
	mux.HandleFunc("/api/ip-groups/", corsMiddleware(api.AdminMiddleware(api.HandleIPGroupSubRoutes)))
	mux.HandleFunc("/api/isos", corsMiddleware(api.AdminMiddleware(api.HandleISOs)))
	mux.HandleFunc("/api/isos/upload", corsMiddleware(api.AdminMiddleware(api.HandleISOUpload)))
	mux.HandleFunc("/api/isos/", corsMiddleware(api.AdminMiddleware(api.HandleISOItem)))
	mux.HandleFunc("/api/isos/attach", corsMiddleware(api.AdminMiddleware(api.HandleContainerISOAction)))
	mux.HandleFunc("/api/containers/rescue", corsMiddleware(api.AdminMiddleware(api.HandleContainerRescue)))

	// 被控（Agent）专用 API：仅主控通过节点 token 调用
	mux.HandleFunc("/api/agent/containers", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentContainers)))
	mux.HandleFunc("/api/agent/containers/", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentContainerAction)))
	mux.HandleFunc("/api/agent/action", corsMiddleware(api.AgentTokenMiddleware(api.AgentContainerActionFromQuery)))
	mux.HandleFunc("/api/agent/images", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImages)))
	mux.HandleFunc("/api/agent/images/sync", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImageSync)))
	mux.HandleFunc("/api/agent/node-backup", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentNodeBackup)))
	mux.HandleFunc("/api/agent/ssh-ticket", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentSSHTicket)))
	mux.HandleFunc("/api/agent/vnc-ticket", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentVNCTicket)))

	// Versioned external API routes
	mux.HandleFunc("/api/v1/dashboard", corsMiddleware(api.AuthMiddleware(api.HandleDashboard)))
	mux.HandleFunc("/api/v1/language", corsMiddleware(api.HandleLanguage))
	mux.HandleFunc("/api/v1/containers", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleContainers))))
	mux.HandleFunc("/api/v1/containers/list", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleContainerListAlias))))
	mux.HandleFunc("/api/v1/containers/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSingleContainer))))
	mux.HandleFunc("/api/v1/templates", corsMiddleware(api.AuthMiddleware(api.HandleTemplates)))
	mux.HandleFunc("/api/v1/images", corsMiddleware(api.AuthMiddleware(api.HandleImages)))
	mux.HandleFunc("/api/v1/images/custom", corsMiddleware(api.AuthMiddleware(api.HandleCustomKVMImages)))
	mux.HandleFunc("/api/v1/images/download", corsMiddleware(api.AuthMiddleware(api.HandleImageDownload)))
	mux.HandleFunc("/api/v1/images/cancel", corsMiddleware(api.AuthMiddleware(api.HandleImageCancel)))
	mux.HandleFunc("/api/v1/images/delete", corsMiddleware(api.AuthMiddleware(api.HandleImageDelete)))
	mux.HandleFunc("/api/v1/images/toggle", corsMiddleware(api.AuthMiddleware(api.HandleImageToggle)))
	mux.HandleFunc("/api/v1/images/enabled", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleEnabledImages))))
	mux.HandleFunc("/api/v1/host-info", corsMiddleware(api.AuthMiddleware(api.HandleHostInfo)))
	mux.HandleFunc("/api/v1/host-history", corsMiddleware(api.AuthMiddleware(api.HandleHostHistory)))
	mux.HandleFunc("/api/v1/monitoring/containers", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("container:read", api.HandleContainerMonitoring))))
	mux.HandleFunc("/api/v1/host-report", corsMiddleware(api.AuthMiddleware(api.HandleHostReport)))
	mux.HandleFunc("/api/v1/snapshots", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("snapshot:read", api.HandleSnapshots))))
	mux.HandleFunc("/api/v1/routing/ipv4-scan", corsMiddleware(api.AuthMiddleware(api.HandleRoutingIPv4Scan)))
	mux.HandleFunc("/api/v1/routing", corsMiddleware(api.AuthMiddleware(api.HandleRouting)))
	mux.HandleFunc("/api/v1/storage", corsMiddleware(api.AdminMiddleware(api.HandleStorage)))
	mux.HandleFunc("/api/v1/ipv6/status", corsMiddleware(api.AuthMiddleware(api.HandleIPv6Status)))
	mux.HandleFunc("/api/v1/tasks", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTasks))))
	mux.HandleFunc("/api/v1/tasks/", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleTaskDelete))))
	mux.HandleFunc("/api/v1/task-queue/settings", corsMiddleware(api.AdminMiddleware(api.HandleTaskQueueSettings)))
	mux.HandleFunc("/api/v1/batch-create", corsMiddleware(api.AuthMiddleware(api.HandleBatchCreate)))
	mux.HandleFunc("/api/v1/batch-action", corsMiddleware(api.AuthMiddleware(api.HandleBatchAction)))
	mux.HandleFunc("/api/v1/sub-user/create", corsMiddleware(api.AdminMiddleware(api.HandleSubUserCreate)))
	mux.HandleFunc("/api/v1/sub-user/change-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserChangePassword)))
	mux.HandleFunc("/api/v1/usage", corsMiddleware(api.AuthMiddleware(api.HandleUsageExport)))
	mux.HandleFunc("/api/v1/smtp", corsMiddleware(api.AdminMiddleware(api.HandleSMTPSettings)))
	mux.HandleFunc("/api/v1/smtp/test", corsMiddleware(api.AdminMiddleware(api.HandleSMTPTest)))
	mux.HandleFunc("/api/v1/sub-users", corsMiddleware(api.AuthMiddleware(api.HandleSubUserList)))
	mux.HandleFunc("/api/v1/sub-users/", corsMiddleware(api.AuthMiddleware(api.HandleSubUserAction)))
	mux.HandleFunc("/api/v1/audit-logs", corsMiddleware(api.AuthMiddleware(api.HandleAuditLogs)))
	mux.HandleFunc("/api/v1/login-logs", corsMiddleware(api.AuthMiddleware(api.HandleLoginLogs)))
	mux.HandleFunc("/api/v1/ssl", corsMiddleware(api.AdminMiddleware(api.HandleSSLSettings)))
	mux.HandleFunc("/api/v1/webssh-origins", corsMiddleware(api.AdminMiddleware(api.HandleWebSSHOriginSettings)))
	mux.HandleFunc("/api/v1/access-policy", corsMiddleware(api.AdminMiddleware(api.HandlePanelAccessPolicy)))
	mux.HandleFunc("/api/v1/admin-path", corsMiddleware(api.AuthMiddleware(api.HandleAdminPathSettings)))
	mux.HandleFunc("/api/v1/security/alerts", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("security:read", api.HandleSecurityAlerts))))
	mux.HandleFunc("/api/v1/security/check", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("security:check", api.HandleSecurityCheck))))
	mux.HandleFunc("/api/v1/security/logs", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("security:read", api.HandleSecurityLogs))))
	mux.HandleFunc("/api/v1/security/summary", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("security:read", api.HandleContainerSecuritySummary))))
	mux.HandleFunc("/api/v1/security/abuse-summary", corsMiddleware(api.AuthMiddleware(api.ScopeMiddleware("security:read", api.HandleAbuseSummary))))
	mux.HandleFunc("/api/v1/security/settings", corsMiddleware(api.AuthMiddleware(api.HandleSecuritySettings)))
	mux.HandleFunc("/api/v1/notifications", corsMiddleware(api.AuthMiddleware(api.HandleNotificationSettings)))
	mux.HandleFunc("/api/v1/notifications/test", corsMiddleware(api.AuthMiddleware(api.HandleNotificationTest)))
	mux.HandleFunc("/api/v1/ssh-ticket", corsMiddleware(api.AuthMiddleware(api.HandleWebSSHTicket)))
	mux.HandleFunc("/api/v1/vnc-ticket", corsMiddleware(api.AuthMiddleware(api.HandleVNCTicket)))
	mux.HandleFunc("/api/v1/api-keys", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleApiKeys))))
	mux.HandleFunc("/api/v1/api-keys/", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleApiKeyDelete))))
	mux.HandleFunc("/api/v1/admins", corsMiddleware(api.AuthMiddleware(api.HandleAdmins)))
	mux.HandleFunc("/api/v1/admins/", corsMiddleware(api.AuthMiddleware(api.HandleAdminItem)))
	mux.HandleFunc("/api/v1/policies", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandlePolicies))))
	mux.HandleFunc("/api/v1/policies/", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandlePolicyItem))))
	mux.HandleFunc("/api/v1/migrate/import", corsMiddleware(api.AuthMiddleware(api.AdminMiddleware(api.HandleMigrateImport))))
	mux.HandleFunc("/api/v1/swap", corsMiddleware(api.AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			api.HandleSwapInfo(w, r)
			return
		}
		api.HandleSwapManage(w, r)
	})))

	// Version (public)
	mux.HandleFunc("/api/version", corsMiddleware(api.HandleVersion))

	// 面板版本检测：返回当前与最新版本（管理员；检测 + 缓存，不自动升级）
	mux.HandleFunc("/api/check-update", corsMiddleware(api.AdminMiddleware(api.HandleCheckUpdate)))
	mux.HandleFunc("/api/v1/check-update", corsMiddleware(api.AdminMiddleware(api.HandleCheckUpdate)))
	// 面板内直接升级（管理员）：下载→解压→备份→就地替换→重启，返回"已开始"。
	// 请求体可选 {repo, tag}：repo 支持 owner/name 第三方仓库，tag 支持指定版本（默认最新）。
	mux.HandleFunc("/api/update", corsMiddleware(api.AdminMiddleware(api.HandlePanelUpdate)))
	mux.HandleFunc("/api/v1/update", corsMiddleware(api.AdminMiddleware(api.HandlePanelUpdate)))
	// 可选版本列表（管理员）：供面板选择升级目标，repo 查询参数默认官方仓库。
	mux.HandleFunc("/api/update/releases", corsMiddleware(api.AdminMiddleware(api.HandleUpdateReleases)))
	mux.HandleFunc("/api/v1/update/releases", corsMiddleware(api.AdminMiddleware(api.HandleUpdateReleases)))

	// Static files
	if webFS != nil {
		fs := http.FileServer(webFS)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// API routes already handled above
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			// Try to serve file
			path := r.URL.Path
			f, err := webFS.Open(path)
			if err != nil {
				// SPA fallback: serve index.html（并按请求路径注入管理员入口路径）
				serveSPAIndex(w, r)
				return
			}
			defer f.Close()
			fs.ServeHTTP(w, r)
		})
	}
}

// adminPathPlaceholder 是 frontend/index.html 里管理员入口路径的占位符。
// 注意不要与 JS 变量名 __EYVES_ADMIN_PATH__ 相同，否则会被一并替换掉。
const adminPathPlaceholder = "__EYVES_ADMIN_PATH_VALUE__"

// serveSPAIndex 返回 SPA 入口页，并按请求路径注入「管理员入口路径」。
//
// 这样做的意义：只有访问到正确路径的请求才会拿到**包含管理端路由**的页面，
// 其它路径拿到的页面里没有任何管理端入口，攻击者无法通过枚举 /login、/admin
// 之类的常见路径发现管理入口。
func serveSPAIndex(w http.ResponseWriter, r *http.Request) {
	indexFile, err := webFS.Open("index.html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	defer indexFile.Close()
	raw, err := io.ReadAll(indexFile)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	body := strings.ReplaceAll(string(raw), adminPathPlaceholder, config.AdminPathForRequest(r.URL.Path))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 注入内容随请求路径变化，必须禁用缓存，否则不同路径会互相串味。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// recoverPanicMiddleware 是全局 panic 兜底：任何 handler 抛出的 panic 都会被
// 捕获并返回 500，而不是让整个 HTTP 服务进程崩掉。企业级服务不允许单个请求
// 的异常拖垮整个面板/主控-被控链路。
func recoverPanicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC recovered on %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, `{"success":false,"message":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// limitRequestBody 限制请求体大小，防止超大请求体导致内存占用（DoS）。
func limitRequestBody(next http.Handler) http.Handler {
	const maxBodyBytes = 64 << 20            // 64 MiB
	const maxStreamingUpload = (20 << 30) + (1024 << 20) // ~20 GiB ISO 上传/备份还原
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 大文件流式上传/还原端点不受通用 64 MiB 限制（受各自 handler 内部独立上限约束）。
		if strings.HasPrefix(r.URL.Path, "/api/isos/upload") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/isos/upload") {
			r.Body = http.MaxBytesReader(w, r.Body, maxStreamingUpload)
		} else {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// gzipResponseWriter wraps http.ResponseWriter to transparently gzip responses
// when the client advertises gzip support. It skips already compressed payloads
// and websocket/streaming connections.
type gzipResponseWriter struct {
	http.ResponseWriter
	writer      *gzip.Writer
	wroteHeader bool
	skipTee     bool // true when passing through (non-compressible response)
	path        string
}

// gzipMiddleware compresses HTML/CSS/JS/JSON text bodies — the dominant cause of
// the phone/weak-network "white screen" (huge uncompressed frontend bundle on a
// slow uplink). It also adds long-lived cache headers for content-hashed assets.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Skip compression for websocket upgrades (SSH / VNC proxies) and any
		// already-binary stream to avoid corrupting the protocol.
		if strings.HasPrefix(path, "/api/ssh") || strings.HasPrefix(path, "/api/vnc") ||
			strings.HasPrefix(path, "/api/v1/vnc") || strings.HasPrefix(path, "/api/v1/ssh") {
			next.ServeHTTP(w, r)
			return
		}

		// Long-lived cache for content-hashed static assets (assets/index-*.js|css).
		if isHashedAsset(path) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipResponseWriter{
			ResponseWriter: w,
			writer:         gzip.NewWriter(w),
			path:           path,
		}
		gw.Header().Set("Vary", "Accept-Encoding")
		next.ServeHTTP(gw, r)
		gw.finish()
	})
}

// finish finalizes the gzip stream and flushes remaining bytes to the client.
func (g *gzipResponseWriter) finish() {
	if g.skipTee {
		// Already passed raw bytes through to the underlying writer.
		return
	}
	if g.writer != nil {
		_ = g.writer.Close()
	}
}

// isHashedAsset reports whether path points at a content-hashed hashed asset.
func isHashedAsset(path string) bool {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	// Matches assets/index-xxxxx.js / .css / .svg 等
	return (strings.Contains(base, ".js") || strings.Contains(base, ".css")) &&
		strings.Contains(base, "-")
}

// shouldCompressPath decides compressibility from the URL path (works even when
// the underlying handler doesn't set a Content-Type header before writing).
func shouldCompressPath(path string) bool {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".js"),
		strings.HasSuffix(lower, ".css"), strings.HasSuffix(lower, ".json"),
		strings.HasSuffix(lower, ".svg"), strings.HasSuffix(lower, ".xml"),
		strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".md"),
		strings.HasSuffix(lower, ".woff2"), strings.HasSuffix(lower, ".woff"),
		strings.HasSuffix(lower, ".ttf"):
		return true
	}
	// JSON/HTML API responses fall under /api — always compress.
	if strings.HasPrefix(path, "/api/") {
		return true
	}
	// SPA fallback index.html.
	if path == "/" || !strings.Contains(lower, ".") {
		return true
	}
	return false
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	ct := g.Header().Get("Content-Type")
	if status == http.StatusNoContent || status == http.StatusNotModified {
		g.skipTee = true
	} else if !shouldCompressContentType(ct) && !shouldCompressPath(g.path) {
		g.skipTee = true
	}
	if g.skipTee {
		// No compression: drop the writer and write raw.
		if g.writer != nil {
			_ = g.writer.Close()
		}
	} else {
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Del("Content-Length")
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.skipTee {
		return g.ResponseWriter.Write(b)
	}
	return g.writer.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	if g.writer != nil && !g.skipTee {
		_ = g.writer.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func shouldCompressContentType(ct string) bool {
	switch {
	case strings.Contains(ct, "text/"),
		strings.Contains(ct, "application/json"),
		strings.Contains(ct, "application/javascript"),
		strings.Contains(ct, "application/x-javascript"),
		strings.Contains(ct, "application/xml"),
		strings.Contains(ct, "image/svg+xml"),
		strings.Contains(ct, "font/ttf"),
		strings.Contains(ct, "font/woff"):
		return true
	}
	// Default: don't compress (binaries like png/jpg/zip/gz would waste CPU).
	return false
}

// apiRateLimitMiddleware applies a per-client-IP rate limit to the versioned
// API (/api/v1/...) when API governance rate limiting is enabled.
func apiRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := config.GetAPIRateLimit()
		if cfg.Enabled && cfg.PerMinute > 0 && strings.HasPrefix(r.URL.Path, "/api/v1/") && clientAddress(r) != "" {
			if !api.AllowVersionedRequest(clientAddress(r), cfg.PerMinute) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"success":false,"message":"API rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Run starts the HTTP server
func Run() error {
	// Use embedded frontend files
	webFS = GetEmbeddedFS()
	api.StartHostMetricSampler()
	api.StartContainerMetricSampler()
	api.StartMetricRollup()
	api.StartPolicyEngine()
	api.StartAuditRetention()
	api.StartBackupScheduler()
	api.StartInstanceBackupScheduler()
	api.StartUptimeTracking()

	mux := http.NewServeMux()
	setupRoutes(mux)

	addr := fmt.Sprintf("0.0.0.0:%d", config.AppConfig.Port)
	log.Printf("EyvesCloud Web Server starting on http://0.0.0.0:%d", config.AppConfig.Port)
	log.Printf("Admin user: %s", config.AppConfig.AdminUser)

	server := &http.Server{
		Addr:    addr,
		Handler: recoverPanicMiddleware(limitRequestBody(panelAccessMiddleware(apiRateLimitMiddleware(gzipMiddleware(mux))))),
	}

	if sslEnabled() {
		certPath, keyPath, err := config.ResolveSSLConfigPaths(config.AppConfig.SSL)
		if err != nil {
			return err
		}
		server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				safeCertPath, err := config.ResolveSSLPath(certPath)
				if err != nil {
					return nil, err
				}
				safeKeyPath, err := config.ResolveSSLPath(keyPath)
				if err != nil {
					return nil, err
				}
				cert, err := tls.LoadX509KeyPair(safeCertPath, safeKeyPath)
				if err != nil {
					return nil, err
				}
				return &cert, nil
			},
		}
		log.Printf("EyvesCloud Web Server SSL enabled on https://0.0.0.0:%d", config.AppConfig.Port)
		return server.ListenAndServeTLS("", "")
	}

	return server.ListenAndServe()
}

func sslEnabled() bool {
	ssl := config.AppConfig.SSL
	if !ssl.Enabled {
		return false
	}
	certPath, keyPath, err := config.ResolveSSLConfigPaths(ssl)
	if err != nil {
		log.Printf("SSL paths are invalid, falling back to HTTP: %v", err)
		return false
	}
	safeCertPath, err := config.ResolveSSLPath(certPath)
	if err != nil {
		log.Printf("SSL certificate path is not allowed, falling back to HTTP: %v", err)
		return false
	}
	safeKeyPath, err := config.ResolveSSLPath(keyPath)
	if err != nil {
		log.Printf("SSL private key path is not allowed, falling back to HTTP: %v", err)
		return false
	}
	if _, err := config.ReadableFileStat(safeCertPath); err != nil {
		log.Printf("SSL certificate is not readable, falling back to HTTP: %v", err)
		return false
	}
	if _, err := config.ReadableFileStat(safeKeyPath); err != nil {
		log.Printf("SSL private key is not readable, falling back to HTTP: %v", err)
		return false
	}
	return true
}
