package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"eyvescloud/internal/config"
)

// HandlePanelDomainSettings 读取/保存「面板绑定域名」。
//
// PanelDomain 是所有对外 URL（节点安装命令、agent.json 的 controller、
// 邮件/Webhook 链接等）的统一基准：配置后不再依赖请求 Host 推导，
// 反代 / 多入口 / 内网 IP 访问场景下也能生成正确地址。
//
// 归一化规则：
//   - 允许 bare domain（自动补 https://）、http(s)://domain[:port]；
//   - 禁止 path / query / fragment（URL 基准不含路径）；
//   - 留空 = 清除绑定，回退按请求 Host 推导。
func HandlePanelDomainSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		domain := config.AppConfig.PanelDomain
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
			"panel_domain": domain,
		}})

	case http.MethodPut, http.MethodPost:
		handlePanelDomainUpdate(w, r)

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func handlePanelDomainUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelDomain string `json:"panel_domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	normalized, err := normalizePanelDomain(req.PanelDomain)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.PanelDomain = normalized
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "settings.panel_domain", "panel",
		fmt.Sprintf("panel_domain=%s", domainOrNone(normalized)), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
		"panel_domain": normalized,
	}})
}

func domainOrNone(domain string) string {
	if domain == "" {
		return "(未绑定)"
	}
	return domain
}

// normalizePanelDomain 校验并归一化管理员输入的域名：
// 返回 "" 表示清空；返回带 scheme 的 base URL（无尾斜杠）。
func normalizePanelDomain(input string) (string, error) {
	raw := strings.TrimSpace(input)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		return "", nil
	}
	// 未带 scheme 时默认 https（面板对外几乎必然是 TLS / CF 代理）。
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("域名格式无效")
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, " /?#") {
		return "", fmt.Errorf("域名格式无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("仅支持 http/https 域名")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("域名不能包含路径或查询参数，仅填写 https://域名[:端口]")
	}
	if len(raw) > 253 {
		return "", fmt.Errorf("域名过长")
	}
	return raw, nil
}

// externalBaseURL 返回对外 URL 基准：优先 PanelDomain，其次按请求推导。
// 所有生成对第三方可见 URL（节点安装命令、agent controller 等）的地方
// 必须经由本函数，保证反代环境下地址正确。
func externalBaseURL(r *http.Request) string {
	config.AppConfigMu.RLock()
	domain := config.AppConfig.PanelDomain
	config.AppConfigMu.RUnlock()
	if domain != "" {
		return strings.TrimSuffix(domain, "/")
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}
