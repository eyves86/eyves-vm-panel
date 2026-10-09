package server

import (
	"compress/gzip"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

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

		setSecurityHeaders(w)

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

// setSecurityHeaders 统一写入 XSS / 点击劫持 / MIME 嗅探 / Referrer 泄露防护头。
// API（corsMiddleware）与静态页面（SPA/资源）共用，保证 HTML 文档同样受 CSP 约束：
// Turnstile 需要放行 challenges.cloudflare.com 的 script / frame / connect。
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"font-src 'self' data:; connect-src 'self' ws: wss: https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; "+
			"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
	// Browsers only honor HSTS on HTTPS responses; the header is harmless on HTTP.
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
}

// setupRoutes configures API and static routes
func setupRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("/api/login", corsMiddleware(api.HandleLogin))
	mux.HandleFunc("/api/language", corsMiddleware(api.HandleLanguage))
	// 登录页底部版权栏（自定义文字/隐藏）：GET 公开（登录页未认证需读取），PUT 仅管理员。
	mux.HandleFunc("/api/login-footer", corsMiddleware(api.HandleLoginFooter))
	// 白标品牌：GET 公开（登录页/前端壳启动拉取）；POST 管理员更新。
	mux.HandleFunc("/api/brand", corsMiddleware(api.HandleBrand))
	mux.HandleFunc("/api/check-auth", corsMiddleware(api.AuthMiddleware(api.HandleCheckAuth)))
	// 登出：清除服务端下发的 HttpOnly 会话 Cookie（审计 H-6）。
	mux.HandleFunc("/api/logout", corsMiddleware(api.HandleLogout))
	mux.HandleFunc("/api/change-password", corsMiddleware(api.AdminSessionMiddleware(api.HandleAdminPasswordChange)))
	mux.HandleFunc("/api/change-username", corsMiddleware(api.AdminSessionMiddleware(api.HandleAdminUsernameChange)))
	mux.HandleFunc("/api/login-logs", corsMiddleware(api.AdminMiddleware(api.HandleLoginLogs)))
	mux.HandleFunc("/api/ssl", corsMiddleware(api.AdminMiddleware(api.HandleSSLSettings)))
	mux.HandleFunc("/api/webssh-origins", corsMiddleware(api.AdminMiddleware(api.HandleWebSSHOriginSettings)))
	mux.HandleFunc("/api/access-policy", corsMiddleware(api.AdminMiddleware(api.HandlePanelAccessPolicy)))
	// 管理员入口路径（可自定义）：仅主管理员会话可读写。
	mux.HandleFunc("/api/admin-path", corsMiddleware(api.AuthMiddleware(api.HandleAdminPathSettings)))
	// 面板绑定域名（对外 URL 基准）：仅管理员。
	mux.HandleFunc("/api/panel-domain", corsMiddleware(api.AdminMiddleware(api.HandlePanelDomainSettings)))
	// Cloudflare Turnstile 人机验证：config 公开（登录页），settings/verify 仅管理员。
	mux.HandleFunc("/api/turnstile/config", corsMiddleware(api.HandleTurnstileConfig))
	mux.HandleFunc("/api/turnstile/settings", corsMiddleware(api.AdminMiddleware(api.HandleTurnstileSettings)))
	mux.HandleFunc("/api/turnstile/verify", corsMiddleware(api.AdminMiddleware(api.HandleTurnstileVerify)))
	// 管理员两步验证（TOTP）
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
	mux.HandleFunc("/api/tasks/history", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTaskHistory))))
	mux.HandleFunc("/api/tasks/stats", corsMiddleware(api.AuthMiddleware(api.HandleTaskStats)))
	mux.HandleFunc("/api/tasks/compat", corsMiddleware(api.AuthMiddleware(api.HandleTaskCompat)))
	mux.HandleFunc("/api/tasks/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTaskSubRoutes))))
	mux.HandleFunc("/api/task-queue/settings", corsMiddleware(api.AdminMiddleware(api.HandleTaskQueueSettings)))
	mux.HandleFunc("/api/backup-plans", corsMiddleware(api.AuthMiddleware(api.HandleBackupPlans)))
	mux.HandleFunc("/api/backup-plans/", corsMiddleware(api.AuthMiddleware(api.HandleBackupPlanSubRoutes)))
	mux.HandleFunc("/api/batch-create", corsMiddleware(api.AdminMiddleware(api.HandleBatchCreate)))
	mux.HandleFunc("/api/batch-action", corsMiddleware(api.AdminMiddleware(api.HandleBatchAction)))
	mux.HandleFunc("/api/sub-user/create", corsMiddleware(api.AdminMiddleware(api.HandleSubUserCreate)))
	mux.HandleFunc("/api/sub-user/login", corsMiddleware(api.HandleSubUserLogin))
	mux.HandleFunc("/api/sub-user/access", corsMiddleware(api.HandleSubUserAccessCode))
	mux.HandleFunc("/api/sub-user/change-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserChangePassword)))
	mux.HandleFunc("/api/sub-user/profile", corsMiddleware(api.AuthMiddleware(api.HandleSubUserProfile)))
	mux.HandleFunc("/api/sub-user/rotate-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserSelfRotatePassword)))
	mux.HandleFunc("/api/sub-user/rotate-access-code-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserSelfRotateAccessCodePassword)))
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

	// 企业化：异地（远程）备份目标（SSH/SCP）：设置读写与连通性测试
	mux.HandleFunc("/api/backup/remote-settings", corsMiddleware(api.AdminMiddleware(api.HandleRemoteBackupSettings)))
	mux.HandleFunc("/api/backup/remote-test", corsMiddleware(api.AdminMiddleware(api.HandleRemoteBackupTest)))

	// 集成：内置 WHMCS 9.0 服务器开通模块（元信息 + zip 下载，仅管理员）
	mux.HandleFunc("/api/integrations/whmcs", corsMiddleware(api.AdminMiddleware(api.HandleWHMCSModule)))
	mux.HandleFunc("/api/integrations/whmcs/download", corsMiddleware(api.AdminMiddleware(api.HandleWHMCSDownload)))

	// SSH 密钥管理（端用户级：admin + subuser 均可管理自己的 key）
	mux.HandleFunc("/api/ssh-keys", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSSHKeys))))
	mux.HandleFunc("/api/ssh-keys/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSSHKeyItem))))

	// 事件订阅 Webhooks（仅管理员：企业集成回调端点管理）
	mux.HandleFunc("/api/webhooks", corsMiddleware(api.AdminMiddleware(api.HandleWebhooks)))
	mux.HandleFunc("/api/webhooks/", corsMiddleware(api.AdminMiddleware(api.HandleWebhookItem)))

	// Recipes（用户自定义 bash 脚本模板，可在容器上执行）
	mux.HandleFunc("/api/recipes", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleRecipes))))
	mux.HandleFunc("/api/recipes/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleRecipeItem))))

	// 安全组（Security Group）CRUD + 规则 + 容器绑定
	mux.HandleFunc("/api/security-groups", corsMiddleware(api.AuthMiddleware(api.HandleSecGroups)))
	// 安全组强制执行状态（如实告知：规则是否真的下发了）。
	mux.HandleFunc("/api/security-group-enforcement", corsMiddleware(api.AuthMiddleware(api.HandleSecGroupEnforcement)))
	mux.HandleFunc("/api/security-groups/", corsMiddleware(api.AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/security-groups/")
		if strings.Contains(path, "/rules") {
			api.HandleSecGroupRules(w, r)
			return
		}
		api.HandleSecGroupItem(w, r)
	})))

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
	mux.HandleFunc("/api/v1/backup/remote-settings", corsMiddleware(api.AdminMiddleware(api.HandleRemoteBackupSettings)))
	mux.HandleFunc("/api/v1/backup/remote-test", corsMiddleware(api.AdminMiddleware(api.HandleRemoteBackupTest)))
	mux.HandleFunc("/api/v1/health", corsMiddleware(api.HandleHealth))
	mux.HandleFunc("/api/v1/health/detail", corsMiddleware(api.AdminMiddleware(api.HandleHealthDetail)))
	mux.HandleFunc("/api/v1/openapi.json", corsMiddleware(api.HandleOpenAPI))
	mux.HandleFunc("/api/v1/rate-limit/settings", corsMiddleware(api.AdminMiddleware(api.HandleRateLimitSettings)))
	mux.HandleFunc("/api/v1/tenants", corsMiddleware(api.AdminMiddleware(api.HandleTenants)))
	mux.HandleFunc("/api/v1/tenants/", corsMiddleware(api.AdminMiddleware(api.HandleTenantItem)))

	// 主控（Controller）节点管理
	mux.HandleFunc("/api/nodes", corsMiddleware(api.AdminMiddleware(api.HandleNodes)))
	// 放置调度决策（创建容器时「自动选择节点」用）：仅做决策，不创建资源。
	mux.HandleFunc("/api/nodes/schedule", corsMiddleware(api.AdminMiddleware(api.HandleNodeSchedule)))
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
	mux.HandleFunc("/api/cells", corsMiddleware(api.AdminMiddleware(api.HandleCells)))
	mux.HandleFunc("/api/cells/", corsMiddleware(api.AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/cells/")
		if path == "" {
			api.HandleCells(w, r)
			return
		}
		api.HandleCellItem(w, r, path)
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
	// 回收站（v2 为主契约；v1 别名供旧集成使用）。
	mux.HandleFunc("/api/v1/recycle-bin", corsMiddleware(api.AuthMiddleware(api.HandleRecycleBin)))
	mux.HandleFunc("/api/recycle-bin", corsMiddleware(api.AuthMiddleware(api.HandleRecycleBin)))

	// 被控（Agent）专用 API：仅主控通过节点 token 调用
	mux.HandleFunc("/api/agent/containers", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentContainers)))
	mux.HandleFunc("/api/agent/containers/", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentContainerAction)))
	mux.HandleFunc("/api/agent/action", corsMiddleware(api.AgentTokenMiddleware(api.AgentContainerActionFromQuery)))
	mux.HandleFunc("/api/agent/images", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImages)))
	mux.HandleFunc("/api/agent/images/sync", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImageSync)))
	mux.HandleFunc("/api/agent/node-backup", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentNodeBackup)))
	mux.HandleFunc("/api/agent/ssh-ticket", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentSSHTicket)))
	mux.HandleFunc("/api/agent/vnc-ticket", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentVNCTicket)))
	// 主控「一键升级被控节点」下发入口（节点侧就地替换二进制并重启服务）。
	mux.HandleFunc("/api/agent/self-update", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentSelfUpdate)))
	// 镜像可用性查询与按需下载（主控开通实例前补齐目标节点镜像）。
	mux.HandleFunc("/api/agent/images/availability", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImageAvailability)))
	mux.HandleFunc("/api/agent/images/download", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImageDownload)))
	mux.HandleFunc("/api/agent/images/delete", corsMiddleware(api.AgentTokenMiddleware(api.HandleAgentImageDelete)))
	// 被控面板上的主控接入管理：查看本机注册信息（管理员）、接入/切换主控、重启 agent 服务。
	// register 允许两种认证：面板管理员会话，或对接密钥（主控「对接已有面板」服务端调用），
	// 因此挂 OptionalAuthMiddleware、由 handler 内部完成认证分支。
	mux.HandleFunc("/api/agent/status", corsMiddleware(api.AdminMiddleware(api.HandleAgentStatus)))
	mux.HandleFunc("/api/agent/pairing-key", corsMiddleware(api.AdminMiddleware(api.HandleAgentPairingKey)))
	mux.HandleFunc("/api/agent/register", corsMiddleware(api.OptionalAuthMiddleware(api.HandleAgentRegister)))
	mux.HandleFunc("/api/agent/restart", corsMiddleware(api.AdminMiddleware(api.HandleAgentRestart)))

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
	mux.HandleFunc("/api/v1/tasks/history", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTaskHistory))))
	mux.HandleFunc("/api/v1/tasks/stats", corsMiddleware(api.AuthMiddleware(api.HandleTaskStats)))
	mux.HandleFunc("/api/v1/tasks/compat", corsMiddleware(api.AuthMiddleware(api.HandleTaskCompat)))
	mux.HandleFunc("/api/v1/tasks/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleTaskSubRoutes))))
	mux.HandleFunc("/api/v1/task-queue/settings", corsMiddleware(api.AdminMiddleware(api.HandleTaskQueueSettings)))
	mux.HandleFunc("/api/v1/backup-plans", corsMiddleware(api.AuthMiddleware(api.HandleBackupPlans)))
	mux.HandleFunc("/api/v1/backup-plans/", corsMiddleware(api.AuthMiddleware(api.HandleBackupPlanSubRoutes)))
	mux.HandleFunc("/api/v1/batch-create", corsMiddleware(api.AuthMiddleware(api.HandleBatchCreate)))
	mux.HandleFunc("/api/v1/batch-action", corsMiddleware(api.AuthMiddleware(api.HandleBatchAction)))
	mux.HandleFunc("/api/v1/sub-user/create", corsMiddleware(api.AdminMiddleware(api.HandleSubUserCreate)))
	mux.HandleFunc("/api/v1/sub-user/change-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserChangePassword)))
	mux.HandleFunc("/api/v1/sub-user/profile", corsMiddleware(api.AuthMiddleware(api.HandleSubUserProfile)))
	mux.HandleFunc("/api/v1/sub-user/rotate-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserSelfRotatePassword)))
	mux.HandleFunc("/api/v1/sub-user/rotate-access-code-password", corsMiddleware(api.AuthMiddleware(api.HandleSubUserSelfRotateAccessCodePassword)))
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

	// v1 版本化：SSH 密钥管理（端用户级）
	mux.HandleFunc("/api/v1/ssh-keys", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSSHKeys))))
	mux.HandleFunc("/api/v1/ssh-keys/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleSSHKeyItem))))

	// v1 版本化：内置 WHMCS 9.0 服务器开通模块（元信息 + zip 下载，仅管理员）
	mux.HandleFunc("/api/v1/integrations/whmcs", corsMiddleware(api.AdminMiddleware(api.HandleWHMCSModule)))
	mux.HandleFunc("/api/v1/integrations/whmcs/download", corsMiddleware(api.AdminMiddleware(api.HandleWHMCSDownload)))

	// v1 版本化：Recipes（用户自定义 bash 脚本模板）
	mux.HandleFunc("/api/v1/recipes", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleRecipes))))
	mux.HandleFunc("/api/v1/recipes/", corsMiddleware(api.AuthMiddleware(api.SubUserMiddleware(api.HandleRecipeItem))))

	// v1 版本化：安全组（Security Group）CRUD + 规则 + 容器绑定
	mux.HandleFunc("/api/v1/security-groups", corsMiddleware(api.AuthMiddleware(api.HandleSecGroups)))
	mux.HandleFunc("/api/v1/security-groups/", corsMiddleware(api.AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/security-groups/")
		if strings.Contains(path, "/rules") {
			api.HandleSecGroupRules(w, r)
			return
		}
		api.HandleSecGroupItem(w, r)
	})))

	// v1 版本化：事件订阅 Webhooks（仅管理员）
	mux.HandleFunc("/api/v1/webhooks", corsMiddleware(api.AdminMiddleware(api.HandleWebhooks)))
	mux.HandleFunc("/api/v1/webhooks/", corsMiddleware(api.AdminMiddleware(api.HandleWebhookItem)))

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
			// 静态资源与 SPA 入口页同样带安全头（CSP / X-Frame-Options 等）。
			setSecurityHeaders(w)
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
			// 目录（含根路径 "/"）：http.FileServer 会直接吐出**未注入管理员
			// 入口路径**的原始 index.html（或目录列表）。根路径一旦走原始
			// index.html，占位符不会被替换，前端会把占位符当作“默认根路径”
			// 挂载管理端路由 —— 自定义管理入口即被 / 绕过。目录一律走
			// SPA 入口页按请求路径注入。
			if stat, serr := f.Stat(); serr == nil && stat.IsDir() {
				serveSPAIndex(w, r)
				return
			}
			fs.ServeHTTP(w, r)
		})
	}
}

// adminPathPlaceholder 是 frontend/index.html 里管理员入口路径的占位符。
// 注意不要与 JS 变量名 __EYVES_ADMIN_PATH__ 相同，否则会被一并替换掉。
const adminPathPlaceholder = "__EYVES_ADMIN_PATH_VALUE__"

// adminNoncePlaceholder 是 frontend/index.html 里内联脚本 nonce 属性的占位符。
// CSP 的 script-src 不含 unsafe-inline，注入的内联脚本必须携带一次性 nonce
// 才会被浏览器执行；否则 window.__EYVES_ADMIN_PATH__ 为 undefined，前端一律
// 按“默认根路径”挂载管理端路由，自定义管理入口形同虚设。
const adminNoncePlaceholder = "__EYVES_ADMIN_NONCE__"

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
	// 为注入的内联管理路径脚本签发一次性 nonce，并同步放宽本页 CSP：
	// 仅该响应的该脚本被放行，不影响其它静态资源的 CSP 策略。
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err == nil && strings.Contains(body, adminNoncePlaceholder) {
		nonce := base64.StdEncoding.EncodeToString(nonceRaw)
		body = strings.ReplaceAll(body, adminNoncePlaceholder, nonce)
		if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
			w.Header().Set("Content-Security-Policy", strings.Replace(csp, "script-src 'self'", "script-src 'self' 'nonce-"+nonce+"'", 1))
		}
	}
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
	const maxBodyBytes = 64 << 20                        // 64 MiB
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
// 同时输出限流透明度响应头（X-RateLimit-Limit / X-RateLimit-Remaining），
// 429 时附带精确的 Retry-After（基于滑动窗口最早请求的过期时间），
// 让企业集成方实现自适应退避而不是盲目重试。
func apiRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := config.GetAPIRateLimit()
		if cfg.Enabled && cfg.PerMinute > 0 && strings.HasPrefix(r.URL.Path, "/api/v1/") && clientAddress(r) != "" {
			allowed, limit, remaining, retryAfter := api.AllowVersionedRequestWithQuota(clientAddress(r), cfg.PerMinute)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !allowed {
				if retryAfter <= 0 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				http.Error(w, `{"success":false,"code":"RATE_LIMITED","message":"API rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// agentGatewayHandler 构造 Agent 网关的处理器链：恢复 panic + 限制请求体 + gzip 解压。
// 刻意不复用面板的 panelAccessMiddleware —— 那会强制校验管理入口随机路径/访问码，
// 而节点根本没有浏览器会话。网关只认节点令牌，由 handleNodeHeartbeat 内部做常量
// 时间比较，因此这里不需要（也不应该）套用面板的访问控制。
func agentGatewayHandler() http.Handler {
	return recoverPanicMiddleware(limitRequestBody(gzipMiddleware(api.AgentGatewayMux())))
}

// startAgentGatewayIfEnabled 在配置了 EYVESCLOUD_AGENT_GATEWAY_ADDR 时启动独立网关。
// 失败必须显式告警但绝不致命：面板自身仍要能起来（心跳继续走面板监听器兜底）。
func startAgentGatewayIfEnabled(tlsCfg *tls.Config) {
	addr := config.AgentGatewayAddr()
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("警告：Agent 网关未启动（%s）：%v；节点心跳仍由面板监听器承载", addr, err)
		return
	}
	serveAgentGateway(ln, tlsCfg)
}

// serveAgentGateway 用已建立的监听器提供节点入站服务（后台 goroutine，非阻塞）。
// 独立成函数以便测试：可在 127.0.0.1:0 上监听并拿到真实端口。
func serveAgentGateway(ln net.Listener, tlsCfg *tls.Config) {
	srv := &http.Server{
		Handler:   agentGatewayHandler(),
		TLSConfig: tlsCfg,
		// 与面板监听器同一套超时基线（审计 H-7）：节点入站同样是公网暴露面。
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	scheme := "http"
	if tlsCfg != nil {
		scheme = "https"
	}
	log.Printf("EyvesCloud Agent 网关监听 %s://%s（仅节点入站上报 /api/nodes/{id}/heartbeat，不暴露面板/管理接口）", scheme, ln.Addr().String())
	go func() {
		var serveErr error
		if tlsCfg != nil {
			serveErr = srv.ServeTLS(ln, "", "")
		} else {
			serveErr = srv.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("Agent 网关退出（%s）：%v", ln.Addr().String(), serveErr)
		}
	}()
}

// Run starts the HTTP server
func Run() error {
	// Use embedded frontend files
	webFS = GetEmbeddedFS()
	// 分布式租约（设计 §4.5）：多副本下全局维护循环只在持租约副本执行；
	// 未配置 Redis 时为空操作，所有循环照常运行。
	api.StartMaintenanceLease()
	api.StartHostMetricSampler()
	api.StartContainerMetricSampler()
	api.StartMetricRollup()
	api.StartPolicyEngine()
	api.StartAuditRetention()
	api.StartBackupScheduler()
	api.StartInstanceBackupScheduler()
	api.StartBackupPlanScheduler()
	api.StartUptimeTracking()
	// 容器级定时启停任务（对齐主流面板语义）。
	api.StartScheduledActionsWorker()
	// 回收站自动清理（同类商业面板 对齐）：每小时扫描超期软删除实例并入真删除任务。
	api.StartRecyclePurgeWorker()
	// 事件订阅引擎：容器状态变更 → Webhook 回调（幂等注册）。
	api.StartWebhookEngine()
	// 心跳落库批处理：把「一请求一事务」改为按窗口合并（见 config/store_batch.go）。
	// 30k 节点 × 10s 心跳 = 3000 次/秒，逐次提交会把单写者数据库变成墙。
	config.StartDeferredSaver()

	mux := http.NewServeMux()
	setupRoutes(mux)
	// API v2（集成专用，契约稳定）：/api/v2/...，见 docs/API-V2.md。
	api.RegisterAPIV2(mux)

	addr := fmt.Sprintf("0.0.0.0:%d", config.AppConfig.Port)
	log.Printf("EyvesCloud Web Server starting on http://0.0.0.0:%d", config.AppConfig.Port)
	log.Printf("Admin user: %s", config.AppConfig.AdminUser)

	server := &http.Server{
		Addr:    addr,
		Handler: recoverPanicMiddleware(limitRequestBody(panelAccessMiddleware(apiRateLimitMiddleware(gzipMiddleware(mux))))),
		// 超时基线（审计 H-7）：公网暴露面的面板必须有读头/读/空闲超时，否则
		// 慢速连接（Slowloris）可用极低成本占满连接与 goroutine。
		// WriteTimeout 不设置：WebSSH/VNC/终端中继与日志流是长连接，写超时会
		// 误杀正常会话；连接级保护由 ReadHeaderTimeout + IdleTimeout 提供。
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// 先显式监听：失败时给出可操作的提示。最常见的升级故障是"旧面板实例
	// （不受 systemd 管理）仍占用端口"，此前只会在日志里留一句 bind error，
	// 表现为服务无限重启（status=1/FAILURE），极难定位。
	listener, listenErr := net.Listen("tcp", addr)
	if listenErr != nil {
		return fmt.Errorf("无法监听 %s：%v\n"+
			"提示：端口可能被残留的 EyvesCloud 实例占用。请执行：\n"+
			"  systemctl stop eyvescloud && pkill -9 -f eyvescloud && systemctl start eyvescloud\n"+
			"（或在设置中改用其它端口）", addr, listenErr)
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
		// 可选：HTTP → HTTPS 跳转监听。
		//
		// 面板是单端口服务，启用 TLS 后原 HTTP 入口直接消失——用户的既有书签、
		// 监控探针、计费系统 Webhook 回调会立刻连接失败，表现为"配了证书反而
		// 打不开"。设了跳转端口就能把访问断裂变成透明升级。
		if redirectPort := config.AppConfig.SSL.HTTPRedirectPort; redirectPort > 0 && redirectPort != config.AppConfig.Port {
			go serveHTTPToHTTPSRedirect(redirectPort, config.AppConfig.Port)
		}
		// 独立 Agent 网关复用面板证书，节点入站同样走 TLS。
		startAgentGatewayIfEnabled(server.TLSConfig)
		log.Printf("EyvesCloud Web Server SSL enabled on https://0.0.0.0:%d", config.AppConfig.Port)
		return server.ServeTLS(listener, "", "")
	}

	// 明文部署（无 TLS）下网关同样只提供心跳入站。
	startAgentGatewayIfEnabled(nil)
	warnIfPlaintextExposed(addr)
	return server.Serve(listener)
}

// serveHTTPToHTTPSRedirect 在 redirectPort 上提供 HTTP → HTTPS 的 301 跳转。
func serveHTTPToHTTPSRedirect(redirectPort, panelPort int) {
	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", redirectPort),
		Handler:           httpToHTTPSRedirectHandler(panelPort),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("HTTP → HTTPS 跳转监听已启动：http://0.0.0.0:%d → https://<host>:%d", redirectPort, panelPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP → HTTPS 跳转监听退出（端口 %d）：%v", redirectPort, err)
	}
}

// httpToHTTPSRedirectHandler 构造跳转处理器（独立成函数以便测试）。
//
// 跳转目标显式带上面板 HTTPS 端口：两个端口不同，不带端口浏览器会回落到 443，
// 结果跳到一个没人监听的地址。
func httpToHTTPSRedirectHandler(panelPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSpace(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "" {
			host = "localhost"
		}
		target := fmt.Sprintf("https://%s:%d%s", host, panelPort, r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// warnIfPlaintextExposed 在未启用 TLS 时打出显式告警。
//
// 面板默认监听 0.0.0.0，且没有反向代理时管理凭据、会话 Cookie、容器 root 口令
// 全部以明文过网。此前这条状态只在"用户自己想起来"时才会被发现。
func warnIfPlaintextExposed(addr string) {
	log.Printf("WARNING: TLS 未启用，面板正以明文 HTTP 监听 %s。", addr)
	log.Printf("WARNING:   管理凭据、会话 Cookie、容器 root 口令都会以明文传输。")
	log.Printf("WARNING:   启用方式：面板「设置 → SSL」签发证书（自签 / Let's Encrypt / 上传自定义证书），")
	log.Printf("WARNING:   并可同时设置 http_redirect_port，把既有 HTTP 访问透明跳转到 HTTPS。")
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
