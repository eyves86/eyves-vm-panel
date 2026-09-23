// Package rbac 实现 P8-1 角色权限模型：
//
//   - 权限点（Permission）按资源:动作 命名（例 container:read, snapshot:create）；
//   - 角色（Role）是命名权限点集合；
//   - 资源级授权（ResourceGrant）：对具体资源类型/ID 或通配的额外授权；
//   - 决策（Decision）一次性返回 Allow/Deny 与理由；
//
// RBAC 不直接对接 HTTP：调用方用 Authorize() 拿决策，HTTP 中间件
// 把 Deny 翻译成 403 + 审计。审计由调用方负责，不在本包职责内。
package rbac

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Permission 权限点（如 "container:read"）。
type Permission string

// 内置权限点常量——按 P8-1 提示词覆盖全部写操作 API。
const (
	// 容器生命周期
	PermContainerRead    Permission = "container:read"
	PermContainerPower   Permission = "container:power"
	PermContainerReinstall Permission = "container:reinstall"
	PermContainerPassword Permission = "container:password"
	PermContainerAccount Permission = "container:account"
	PermContainerNetwork Permission = "container:network"
	PermContainerDelete  Permission = "container:delete"
	PermContainerCreate  Permission = "container:create"
	// 镜像/模板
	PermImageRead    Permission = "image:read"
	PermImageManage  Permission = "image:manage"
	PermTemplateManage Permission = "template:manage"
	// 快照/备份
	PermSnapshotRead    Permission = "snapshot:read"
	PermSnapshotCreate  Permission = "snapshot:create"
	PermSnapshotDelete  Permission = "snapshot:delete"
	PermSnapshotRestore Permission = "snapshot:restore"
	PermSnapshotSchedule Permission = "snapshot:schedule"
	// 网络
	PermRoutingRead   Permission = "routing:read"
	PermRoutingManage Permission = "routing:manage"
	// 主机/节点
	PermHostRead   Permission = "host:read"
	PermHostManage Permission = "host:manage"
	// 终端
	PermTerminalSSH Permission = "terminal:ssh"
	PermTerminalVNC Permission = "terminal:vnc"
	// 审计 / 事件
	PermAuditRead    Permission = "audit:read"
	PermAuditSettings Permission = "audit:settings"
	// 租户 / 配额
	PermTenantRead   Permission = "tenant:read"
	PermTenantManage Permission = "tenant:manage"
	// API Key
	PermAPIKeyRead    Permission = "apikey:read"
	PermAPIKeyCreate  Permission = "apikey:create"
	PermAPIKeyUpdate  Permission = "apikey:update"
	PermAPIKeyDelete  Permission = "apikey:delete"
	// 系统
	PermSystemSettings Permission = "system:settings"
	// 管理面
	PermAdminAccess Permission = "admin:access"
	// 通配
	PermWildcard Permission = "*"
)

// AllPermissions 列出所有受 RBAC 管理的权限点；用于做权限矩阵测试。
func AllPermissions() []Permission {
	return []Permission{
		PermContainerRead, PermContainerPower, PermContainerReinstall,
		PermContainerPassword, PermContainerAccount, PermContainerNetwork,
		PermContainerDelete, PermContainerCreate,
		PermImageRead, PermImageManage, PermTemplateManage,
		PermSnapshotRead, PermSnapshotCreate, PermSnapshotDelete,
		PermSnapshotRestore, PermSnapshotSchedule,
		PermRoutingRead, PermRoutingManage,
		PermHostRead, PermHostManage,
		PermTerminalSSH, PermTerminalVNC,
		PermAuditRead, PermAuditSettings,
		PermTenantRead, PermTenantManage,
		PermAPIKeyRead, PermAPIKeyCreate, PermAPIKeyUpdate, PermAPIKeyDelete,
		PermSystemSettings, PermAdminAccess,
	}
}

// Role 角色：命名权限点集合。
type Role struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
}

// HasPermission 检查角色是否含某权限（含通配 *）。
func (r Role) HasPermission(p Permission) bool {
	for _, granted := range r.Permissions {
		if granted == PermWildcard {
			return true
		}
		if granted == p {
			return true
		}
		// 前缀通配：admin:* 等
		if strings.HasSuffix(string(granted), ":*") {
			prefix := strings.TrimSuffix(string(granted), "*")
			if strings.HasPrefix(string(p), prefix) {
				return true
			}
		}
	}
	return false
}

// ResourceGrant 资源级授权：除角色权限外，对指定资源类型/ID 的额外授权。
// 资源类型如 "container"；ID 为空表示该类型全部资源。
type ResourceGrant struct {
	Subject      string    `json:"subject"` // 用户/Key ID
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Permissions  []Permission `json:"permissions"`
}

// Engine RBAC 决策引擎。
type Engine struct {
	mu     sync.RWMutex
	roles  map[string]Role
	grants []ResourceGrant
}

// NewEngine 构造空引擎并加载内置角色。
func NewEngine() *Engine {
	e := &Engine{roles: map[string]Role{}}
	for _, r := range BuiltinRoles() {
		e.roles[r.ID] = r
	}
	return e
}

// BuiltinRoles 返回内置角色集合：
//   - admin：全权（*）
//   - owner：租户内全权（不含 admin:*）
//   - operator：容器读写/快照
//   - readonly：仅读
func BuiltinRoles() []Role {
	return []Role{
		{
			ID:   "admin",
			Name: "Administrator",
			Permissions: []Permission{
				PermWildcard,
			},
		},
		{
			ID:   "owner",
			Name: "Tenant owner",
			Permissions: []Permission{
				PermContainerRead, PermContainerPower, PermContainerReinstall,
				PermContainerPassword, PermContainerAccount, PermContainerNetwork,
				PermContainerDelete, PermContainerCreate,
				PermImageRead, PermTemplateManage,
				PermSnapshotRead, PermSnapshotCreate, PermSnapshotDelete,
				PermSnapshotRestore, PermSnapshotSchedule,
				PermRoutingRead, PermRoutingManage,
				PermHostRead, PermTerminalSSH, PermTerminalVNC,
				PermAPIKeyRead, PermAPIKeyCreate, PermAPIKeyUpdate, PermAPIKeyDelete,
				PermTenantRead,
			},
		},
		{
			ID:   "operator",
			Name: "Operator",
			Permissions: []Permission{
				PermContainerRead, PermContainerPower, PermContainerReinstall,
				PermContainerPassword, PermContainerAccount, PermContainerNetwork,
				PermImageRead,
				PermSnapshotRead, PermSnapshotCreate, PermSnapshotDelete,
				PermSnapshotRestore, PermSnapshotSchedule,
				PermRoutingRead,
				PermHostRead, PermTerminalSSH, PermTerminalVNC,
				PermTenantRead,
			},
		},
		{
			ID:   "readonly",
			Name: "Read only",
			Permissions: []Permission{
				PermContainerRead, PermImageRead, PermSnapshotRead,
				PermRoutingRead, PermHostRead,
				PermTerminalSSH, PermTerminalVNC,
				PermTenantRead,
			},
		},
	}
}

// UpsertRole 注册或更新角色。
func (e *Engine) UpsertRole(r Role) error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("rbac: role id is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roles[r.ID] = r
	return nil
}

// GetRole 按 ID 读取角色。
func (e *Engine) GetRole(id string) (Role, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.roles[id]
	return r, ok
}

// AddGrant 增加资源级授权。
func (e *Engine) AddGrant(g ResourceGrant) error {
	if strings.TrimSpace(g.Subject) == "" {
		return errors.New("rbac: grant subject required")
	}
	if strings.TrimSpace(g.ResourceType) == "" {
		return errors.New("rbac: grant resource type required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grants = append(e.grants, g)
	return nil
}

// Decision 授权决策结果。
type Decision struct {
	Allow  bool
	Reason string
	Source string // "role" / "grant" / "default-deny"
}

// Authorize 检查主体（角色 ID + 资源授权）是否对资源拥有权限。
//
// 参数：
//   - subject：调用方标识（用户/Key ID）
//   - roleID：主体当前角色
//   - resource：资源类型（如 "container"），空表示非资源型权限
//   - resourceID：具体资源 ID，可空
//   - perm：所需权限点
func (e *Engine) Authorize(subject, roleID, resource, resourceID string, perm Permission) Decision {
	e.mu.RLock()
	r, roleOK := e.roles[roleID]
	grants := append([]ResourceGrant(nil), e.grants...)
	e.mu.RUnlock()
	if roleOK && r.HasPermission(perm) {
		return Decision{Allow: true, Source: "role", Reason: fmt.Sprintf("role %q grants %s", roleID, perm)}
	}
	for _, g := range grants {
		if g.Subject != subject {
			continue
		}
		if g.ResourceType != resource {
			continue
		}
		if g.ResourceID != "" && g.ResourceID != resourceID {
			continue
		}
		for _, p := range g.Permissions {
			if p == perm || p == PermWildcard {
				return Decision{Allow: true, Source: "grant", Reason: fmt.Sprintf("grant on %s/%s", resource, resourceID)}
			}
		}
	}
	return Decision{Allow: false, Source: "default-deny", Reason: fmt.Sprintf("no permission for %s on %s/%s", perm, resource, resourceID)}
}

// ---- 管理端（管理员账号）角色 ----
//
// 与 BuiltinRoles 的“平台内置角色”区分：这里是**管理员账号**在管理端控制台里的
// 角色，供多管理员场景按最小权限分配。判定入口是 AdminConsoleRole。

const (
	AdminConsoleAdmin    = "admin"
	AdminConsoleOperator = "operator"
	AdminConsoleReadonly = "readonly"
)

// AdminConsoleRole 返回管理端角色定义：
//   - admin：全权（*），兼容旧行为（旧令牌无 role 即视为 admin）
//   - operator：日常运维（容器/快照/网络/终端/主机只读/审计只读），
//     禁平台级：账号与密钥、策略、租户、备份还原、SSL、系统设置、审计设置
//   - readonly：只读（无任何写权限）
//
// 未知取值返回 false，调用方必须 fail closed。
func AdminConsoleRole(id string) (Role, bool) {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case AdminConsoleAdmin, "":
		return Role{ID: AdminConsoleAdmin, Name: "Administrator", Permissions: []Permission{PermWildcard}}, true
	case AdminConsoleOperator:
		return Role{ID: AdminConsoleOperator, Name: "Operator", Permissions: []Permission{
			PermContainerRead, PermContainerPower, PermContainerReinstall, PermContainerPassword,
			PermContainerAccount, PermContainerNetwork, PermContainerCreate, PermContainerDelete,
			PermImageRead, PermTemplateManage,
			PermSnapshotRead, PermSnapshotCreate, PermSnapshotDelete, PermSnapshotRestore, PermSnapshotSchedule,
			PermRoutingRead, PermRoutingManage,
			PermHostRead, PermTerminalSSH, PermTerminalVNC,
			PermAuditRead, PermTenantRead,
		}}, true
	case AdminConsoleReadonly, "viewer", "read":
		return Role{ID: AdminConsoleReadonly, Name: "Read only", Permissions: []Permission{
			PermContainerRead, PermImageRead, PermSnapshotRead, PermRoutingRead,
			PermHostRead, PermAuditRead, PermTenantRead,
			PermTerminalSSH, PermTerminalVNC,
		}}, true
	}
	return Role{}, false
}

// MigrateLegacyRole 把历史 admin/sub-user role 字符串映射到新角色 ID。
//   - ""：旧管理员会话无 role 声明 → admin
//   - "admin"：平台管理员 → admin
//   - "owner"：租户所有者 → owner（**不得**映射为 admin：owner 无 admin:access，
//     误映射会造成租户角色越权为平台管理员）
//   - "operator"：operator
//   - "viewer" / "readonly" / "read"：readonly
//
// 未识别的取值一律降级为 readonly（fail closed），绝不默认放行。
func MigrateLegacyRole(legacy string) string {
	switch strings.ToLower(strings.TrimSpace(legacy)) {
	case "", "admin":
		return "admin"
	case "owner":
		return "owner"
	case "operator":
		return "operator"
	case "viewer", "readonly", "read":
		return "readonly"
	}
	return "readonly"
}