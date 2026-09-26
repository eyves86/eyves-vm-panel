package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/rbac"

	"golang.org/x/crypto/bcrypt"
)

// adminAccountView 是管理员账号的对外视图（不含口令哈希）。
type adminAccountView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	Primary     bool   `json:"primary"`
	CreatedAt   string `json:"created_at,omitempty"`
	LastLoginAt string `json:"last_login_at,omitempty"`
}

func adminViewOf(a config.AdminAccount) adminAccountView {
	return adminAccountView{
		ID:          a.ID,
		Username:    a.Username,
		Role:        config.NormalizeAdminRole(a.Role),
		Disabled:    a.Disabled,
		CreatedAt:   a.CreatedAt,
		LastLoginAt: a.LastLoginAt,
	}
}

// requireAdminConsole 要求调用方是**管理员账号会话**（拒绝子用户与 API Key）。
// 管理员管理是高敏感操作，不允许通过 API Key 或子用户身份执行。
func requireAdminConsole(w http.ResponseWriter, r *http.Request) (AuthContext, bool) {
	ctx, ok := authContextFromRequest(r)
	if !ok || ctx.Type != authTypeAdmin {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "仅管理员账号可管理管理员"})
		return AuthContext{}, false
	}
	return ctx, true
}

// requireAdminPerm 校验当前管理员角色是否具备某 rbac 权限点；被拒时写 403 + 审计。
func requireAdminPerm(w http.ResponseWriter, r *http.Request, ctx AuthContext, perm rbac.Permission) bool {
	if roleDef, ok := rbac.AdminConsoleRole(ctx.Role); ok && roleDef.HasPermission(perm) {
		return true
	}
	auditRequest(r, "rbac.denied", r.URL.Path,
		"role="+config.NormalizeAdminRole(ctx.Role)+" perm="+string(perm), false, "insufficient role permission")
	jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "当前管理员角色无权管理管理员账号"})
	return false
}

// HandleAdmins 管理员账号集合：GET 列表 / POST 创建。
func HandleAdmins(w http.ResponseWriter, r *http.Request) {
	ctx, ok := requireAdminConsole(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !requireAdminPerm(w, r, ctx, rbac.PermSystemSettings) {
			return
		}
		listAdmins(w, r)
	case http.MethodPost:
		if !requireAdminPerm(w, r, ctx, rbac.PermSystemSettings) {
			return
		}
		createAdmin(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleAdminItem 单个管理员账号：PATCH 更新 / DELETE 删除。
func HandleAdminItem(w http.ResponseWriter, r *http.Request) {
	ctx, ok := requireAdminConsole(w, r)
	if !ok {
		return
	}
	id := adminIDFromPath(r.URL.Path)
	if id == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "管理员 ID 必填"})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		updateAdmin(w, r, ctx, id)
	case http.MethodDelete:
		deleteAdmin(w, r, ctx, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func adminIDFromPath(path string) string {
	path = strings.TrimPrefix(path, "/api/v1/admins/")
	path = strings.TrimPrefix(path, "/api/admins/")
	return strings.Trim(path, "/")
}

func listAdmins(w http.ResponseWriter, r *http.Request) {
	views := make([]adminAccountView, 0)
	config.AppConfigMu.RLock()
	primaryUser := config.AppConfig.AdminUser
	accounts := append([]config.AdminAccount(nil), config.AppConfig.Admins...)
	config.AppConfigMu.RUnlock()

	// 主管理员（由 AdminUser 承载）作为只读条目一并返回，便于前端展示完整列表。
	views = append(views, adminAccountView{
		Username: primaryUser,
		Role:     config.AdminRoleAdmin,
		Primary:  true,
	})
	for _, a := range accounts {
		views = append(views, adminViewOf(a))
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: views})
}

func createAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "用户名必填"})
		return
	}
	if err := validateStrongPassword(req.Password); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	role := config.NormalizeAdminRole(req.Role)
	if _, ok := rbac.AdminConsoleRole(role); !ok {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的管理员角色"})
		return
	}
	if config.AdminUsernameTaken(username, "") {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "该用户名已被占用"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "口令哈希失败"})
		return
	}
	acct := config.AdminAccount{
		ID:        generateShortID(),
		Username:  username,
		PassHash:  string(hash),
		Role:      role,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Admins = append(cfg.Admins, acct)
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "admin.create", username, "role="+role, true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: adminViewOf(acct)})
}

func updateAdmin(w http.ResponseWriter, r *http.Request, ctx AuthContext, id string) {
	acct, found := config.FindAdminAccountByID(id)
	if !found {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "管理员不存在"})
		return
	}
	isSelf := strings.EqualFold(ctx.Username, acct.Username)
	// 修改他人（角色/禁用/重置口令）属平台级操作；改自己口令属自助，任何角色可用。
	if !isSelf && !requireAdminPerm(w, r, ctx, rbac.PermSystemSettings) {
		return
	}
	var req struct {
		Password string `json:"password"`
		Role     string `json:"role"`
		Disabled *bool  `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	newHash := acct.PassHash
	bumpToken := false
	if strings.TrimSpace(req.Password) != "" {
		if err := validateStrongPassword(req.Password); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "口令哈希失败"})
			return
		}
		newHash = string(hash)
		bumpToken = true // 改口令即吊销该账号已签发的令牌
	}

	// 自助更新只允许改口令：忽略越权的角色/禁用字段。
	newRole := acct.Role
	if !isSelf && strings.TrimSpace(req.Role) != "" {
		normalized := config.NormalizeAdminRole(req.Role)
		if _, ok := rbac.AdminConsoleRole(normalized); !ok {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的管理员角色"})
			return
		}
		newRole = normalized
	}
	newDisabled := acct.Disabled
	if !isSelf && req.Disabled != nil {
		newDisabled = *req.Disabled
	}

	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Admins {
			if cfg.Admins[i].ID != id {
				continue
			}
			cfg.Admins[i].PassHash = newHash
			cfg.Admins[i].Role = newRole
			cfg.Admins[i].Disabled = newDisabled
			if bumpToken {
				cfg.Admins[i].TokenVersion++
			}
			return
		}
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	updated, _ := config.FindAdminAccountByID(id)
	detail := "self=" + boolText(isSelf) + " role=" + newRole + " disabled=" + boolText(newDisabled) + " password_changed=" + boolText(bumpToken)
	auditRequest(r, "admin.update", acct.Username, detail, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: adminViewOf(updated)})
}

func deleteAdmin(w http.ResponseWriter, r *http.Request, ctx AuthContext, id string) {
	if !requireAdminPerm(w, r, ctx, rbac.PermSystemSettings) {
		return
	}
	acct, found := config.FindAdminAccountByID(id)
	if !found {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "管理员不存在"})
		return
	}
	if strings.EqualFold(ctx.Username, acct.Username) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "不能删除当前登录的账号"})
		return
	}
	removed := false
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		filtered := cfg.Admins[:0]
		for _, a := range cfg.Admins {
			if a.ID == id {
				removed = true
				continue
			}
			filtered = append(filtered, a)
		}
		cfg.Admins = filtered
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if !removed {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "管理员不存在"})
		return
	}
	auditRequest(r, "admin.delete", acct.Username, "", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "管理员已删除"})
}
