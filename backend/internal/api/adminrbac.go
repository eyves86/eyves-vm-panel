package api

import (
	"net/http"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/rbac"
)

// adminRoutePolicy 把管理端路由前缀映射到 rbac 权限点（读/写分开）。
//
// 前缀按 canonicalAdminPath 归一化后匹配（/api/v1/xxx 与 /api/xxx 等价）。
// 未命中的路由 fail closed：写操作按平台级（PermSystemSettings）处理，
// 只有全权 admin 角色能执行；读操作按 container:read 处理。
var adminRoutePolicy = []struct {
	prefix string
	read   rbac.Permission
	write  rbac.Permission
}{
	{"/api/admins", rbac.PermSystemSettings, rbac.PermSystemSettings},
	{"/api/api-keys", rbac.PermAPIKeyRead, rbac.PermAPIKeyCreate},
	{"/api/audit", rbac.PermAuditRead, rbac.PermAuditSettings},
	{"/api/login-logs", rbac.PermAuditRead, rbac.PermAuditSettings},
	{"/api/tenants", rbac.PermTenantRead, rbac.PermTenantManage},
	{"/api/sub-user", rbac.PermTenantRead, rbac.PermTenantManage},
	{"/api/sub-users", rbac.PermTenantRead, rbac.PermTenantManage},
	{"/api/policies", rbac.PermSystemSettings, rbac.PermSystemSettings},
	{"/api/images", rbac.PermImageRead, rbac.PermImageManage},
	{"/api/snapshots", rbac.PermSnapshotRead, rbac.PermSnapshotCreate},
	{"/api/routing", rbac.PermRoutingRead, rbac.PermRoutingManage},
	{"/api/host", rbac.PermHostRead, rbac.PermHostManage},
	{"/api/storage", rbac.PermHostRead, rbac.PermHostManage},
	{"/api/containers", rbac.PermContainerRead, rbac.PermContainerPower},
	{"/api/backup", rbac.PermSnapshotRead, rbac.PermSnapshotCreate},
	{"/api/notifications", rbac.PermAuditRead, rbac.PermSystemSettings},
	{"/api/security", rbac.PermAuditRead, rbac.PermSystemSettings},
	{"/api/rate-limit", rbac.PermAuditRead, rbac.PermSystemSettings},
	{"/api/ssl", rbac.PermAuditRead, rbac.PermSystemSettings},
	{"/api/access-policy", rbac.PermAuditRead, rbac.PermSystemSettings},
	{"/api/migrate", rbac.PermAuditRead, rbac.PermSystemSettings},
}

// adminSelfServicePaths 所有管理员角色（含只读）都允许访问的自助路径。
// 注意：change-password / change-username 作用于**主管理员账号**，属平台级，
// 不在此列；额外管理员改自己的密码走 /api/v1/admins/{id}。
var adminSelfServicePaths = []string{
	"/api/check-auth",
	"/api/language",
	"/api/2fa/",
}

// canonicalAdminPath 把版本化前缀归一化，使 /api/v1/xxx 与 /api/xxx 命中同一策略。
func canonicalAdminPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/") {
		return "/api/" + strings.TrimPrefix(path, "/api/v1/")
	}
	return path
}

// adminPermissionFor 推导某请求所需的 rbac 权限点。
func adminPermissionFor(method, path string) rbac.Permission {
	write := method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
	p := canonicalAdminPath(path)
	for _, rule := range adminRoutePolicy {
		if strings.HasPrefix(p, rule.prefix) {
			if write {
				return rule.write
			}
			return rule.read
		}
	}
	if write {
		return rbac.PermSystemSettings
	}
	return rbac.PermContainerRead
}

// adminRoleAllowed 判定管理员角色能否访问该请求（基于 rbac 权限点，fail closed）。
func adminRoleAllowed(role, method, path string) bool {
	roleDef, ok := rbac.AdminConsoleRole(role)
	if !ok {
		return false
	}
	p := canonicalAdminPath(path)
	for _, prefix := range adminSelfServicePaths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return roleDef.HasPermission(adminPermissionFor(method, path))
}

// enforceAdminRole 在管理端中间件里强制角色权限；被拒时写 403 + 审计并返回 false。
func enforceAdminRole(w http.ResponseWriter, r *http.Request, role string) bool {
	normalized := config.NormalizeAdminRole(role)
	if adminRoleAllowed(normalized, r.Method, r.URL.Path) {
		return true
	}
	auditRequest(r, "rbac.denied", r.URL.Path,
		"role="+normalized+" method="+r.Method, false, "insufficient role permission")
	jsonResponse(w, http.StatusForbidden, APIResponse{
		Success: false,
		Message: "当前管理员角色无权执行该操作（role=" + normalized + "）",
	})
	return false
}
