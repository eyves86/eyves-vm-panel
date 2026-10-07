package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// HandleAdminPathSettings 读取/修改「管理员入口路径」。
//
// 该路径用于隐藏管理入口：服务端只会在访问到正确路径时，才把管理端路由注入
// 到返回的前端页面里（见 server.serveSPAIndex）。用户门户固定为 /user。
//
// 仅限**主管理员会话**操作：改错路径会导致自己找不到管理入口，所以只允许主管理员
// 修改，并且响应里回带完整入口地址，方便确认。
func HandleAdminPathSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := authContextFromRequest(r)
	if !ok || ctx.Type != authTypeAdmin {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "仅管理员可配置管理员入口路径"})
		return
	}
	if !strings.EqualFold(ctx.Username, config.AppConfig.AdminUser) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "此操作仅限主管理员账号"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
			"admin_path": config.CurrentAdminPath(),
			"user_path":  "/user",
		}})

	case http.MethodPut, http.MethodPost:
		var req struct {
			AdminPath string `json:"admin_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		normalized, valid := config.NormalizeAdminPath(req.AdminPath)
		if !valid {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "路径不合法：每段只能包含字母/数字/-/_/./~，不能包含 ..，且不能占用 /api、/user、/assets、/favicon",
			})
			return
		}
		if err := config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
			cfg.AdminPath = normalized
		}); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "settings.admin_path", normalized, "user_path=/user", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
			"admin_path": normalized,
			"user_path":  "/user",
		}})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}
