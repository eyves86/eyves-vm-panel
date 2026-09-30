package api

// brand.go —— 白标品牌配置（授权运营场景）。
//
// GET  /api/brand       公开：返回品牌展示字段（登录页/前端壳启动时拉取）。
//                       Logo/Favicon 只返回 data URL 前缀与长度信息？——不，
//                       前端需要完整 data URL 才能渲染，直接返回；单图 ≤2MB、
//                       静态资源走 gzip，可接受。
// POST /api/brand       管理员更新（AdminSessionMiddleware）。
//
// 语义：全部留空 = EyvesCloud 默认（开源/默认授权形态）。
// BrandPoweredHidden = true 时页脚 "Powered by EyvesCloud" 隐藏——这是
// 付费授权开关，与登录页 footer 文案独立。

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// maxBrandImageBytes 允许的 Logo/favicon data URL 上限（2MB）。
const maxBrandImageBytes = 2 * 1024 * 1024

// brandResponse GET 响应体（公开字段，无敏感信息）。
func brandResponse() map[string]any {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	name := strings.TrimSpace(config.AppConfig.BrandName)
	if name == "" {
		name = "EyvesCloud"
	}
	loginTitle := strings.TrimSpace(config.AppConfig.BrandLoginTitle)
	poweredHidden := config.AppConfig.BrandPoweredHidden
	return map[string]any{
		"name":           name,
		"logo":           config.AppConfig.BrandLogo,
		"favicon":        config.AppConfig.BrandFavicon,
		"login_title":    loginTitle,
		"powered_hidden": poweredHidden,
		"footer_text":    config.AppConfig.LoginFooterText,
		"footer_hidden":  config.AppConfig.LoginFooterHidden,
	}
}

// validateBrandImage 校验 Logo/Favicon data URL 形态与大小。
func validateBrandImage(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	// 格式：data:<mime>;base64,<payload>（mime 限 PNG/JPEG/SVG/ICO）。
	if !strings.HasPrefix(value, "data:image/") || !strings.Contains(value, ";base64,") {
		return "", &fieldError{field, "必须是 data URL（data:image/png;base64,... 形式）"}
	}
	mime := strings.SplitN(strings.TrimPrefix(value, "data:"), ";", 2)[0]
	switch mime {
	case "image/png", "image/jpeg", "image/svg+xml", "image/x-icon":
	default:
		return "", &fieldError{field, "仅支持 PNG/JPEG/SVG/ICO"}
	}
	raw, err := base64.StdEncoding.DecodeString(dataURLPayload(value))
	if err != nil {
		return "", &fieldError{field, "base64 解码失败"}
	}
	if len(raw) > maxBrandImageBytes {
		return "", &fieldError{field, "图片超过 2MB 上限"}
	}
	return value, nil
}

// dataURLPayload 取 data URL 的 base64 部分。
func dataURLPayload(dataURL string) string {
	for i := 0; i < len(dataURL); i++ {
		if dataURL[i] == ',' {
			return dataURL[i+1:]
		}
	}
	return ""
}

type fieldError struct {
	Field string
	Msg   string
}

func (e *fieldError) Error() string { return e.Field + ": " + e.Msg }

// currentBrandName 返回品牌名（空时默认 EyvesCloud）；邮件通知抬头等复用。
func currentBrandName() string {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	if n := strings.TrimSpace(config.AppConfig.BrandName); n != "" {
		return n
	}
	return "EyvesCloud"
}

// HandleBrand GET 公开 / POST 管理员。
func HandleBrand(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: brandResponse()})
	case http.MethodPost, http.MethodPut:
		AdminSessionMiddleware(handleBrandUpdate)(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func handleBrandUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Logo          string `json:"logo"`
		Favicon       string `json:"favicon"`
		LoginTitle    string `json:"login_title"`
		PoweredHidden *bool  `json:"powered_hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 60 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "品牌名称过长（≤60字符）"})
		return
	}
	loginTitle := strings.TrimSpace(req.LoginTitle)
	if len(loginTitle) > 200 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "登录页欢迎语过长（≤200字符）"})
		return
	}
	logo, err := validateBrandImage("logo", req.Logo)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	favicon, err := validateBrandImage("favicon", req.Favicon)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		if req.Name != "" || name == "" {
			cfg.BrandName = name
		}
		cfg.BrandLogo = logo
		cfg.BrandFavicon = favicon
		cfg.BrandLoginTitle = loginTitle
		if req.PoweredHidden != nil {
			cfg.BrandPoweredHidden = *req.PoweredHidden
		}
	})
	auditRequest(r, "settings.brand", name, "白标品牌更新", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "品牌设置已保存", Data: brandResponse()})
}
