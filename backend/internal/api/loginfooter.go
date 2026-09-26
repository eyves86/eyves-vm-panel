package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"eyvescloud/internal/config"
)

// HandleLoginFooter 读取/修改「登录页底部版权栏」。
//
// GET 是**公开端点**（登录页本身未认证，需要拉取版权配置）；
// PUT/POST 走 AdminMiddleware 鉴权（仅管理员可改品牌文案）。
//
// 配置语义：
//   - hidden=true  → 登录页底部版权栏完全不渲染；
//   - text 非空    → 显示自定义文字；
//   - text 为空    → 前端显示默认版权（© <年份> EyvesCloud. All rights reserved.）。
func HandleLoginFooter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		text := config.AppConfig.LoginFooterText
		hidden := config.AppConfig.LoginFooterHidden
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
			"text":   text,
			"hidden": hidden,
		}})

	case http.MethodPut, http.MethodPost:
		AdminMiddleware(handleLoginFooterUpdate)(w, r)

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func handleLoginFooterUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string `json:"text"`
		Hidden bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	text := strings.TrimSpace(req.Text)
	if utf8.RuneCountInString(text) > 120 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "版权文字不能超过 120 个字符"})
		return
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.LoginFooterText = text
		cfg.LoginFooterHidden = req.Hidden
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "settings.login_footer", "login_footer", "hidden="+boolStr(req.Hidden), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
		"text":   text,
		"hidden": req.Hidden,
	}})
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
