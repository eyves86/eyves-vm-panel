package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// Cloudflare Turnstile 人机验证。
//
// 端点划分：
//   - GET  /api/turnstile/config   公开：登录页据此决定是否渲染 widget；
//   - GET  /api/turnstile/settings 管理员：读取完整配置（secret 打码回显）；
//   - PUT  /api/turnstile/settings 管理员：保存 site key / secret / 两处开关；
//   - POST /api/turnstile/verify   管理员：用「已保存或请求内携带」的密钥对做
//     siteverify 冒烟测试，供设置页「点击测试显示验证码」。
//
// 安全要点：
//   - SecretKey 绝不下发前端（settings 回显打码，config 端点只含 site key）；
//   - 登录端点在密码比对**之前**校验 Turnstile，失败同样计入限流；
//   - 密钥未配置齐全时对应开关自动失效（放行），避免把自己锁在门外。

const turnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileEffective 判断某处登录入口是否实际启用 Turnstile（密钥齐全 + 开关开）。
func turnstileEffective(siteKey, secretKey string, enabled bool) bool {
	return enabled && siteKey != "" && secretKey != ""
}

// turnstileEnabledForAdmin / turnstileEnabledForUser 读取当前生效状态（读锁快照）。
func turnstileEnabledForAdmin() bool {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	return turnstileEffective(config.AppConfig.TurnstileSiteKey, config.AppConfig.TurnstileSecretKey, config.AppConfig.TurnstileAdminLogin)
}

func turnstileEnabledForUser() bool {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	return turnstileEffective(config.AppConfig.TurnstileSiteKey, config.AppConfig.TurnstileSecretKey, config.AppConfig.TurnstileUserLogin)
}

// verifyTurnstileToken 调用 Cloudflare siteverify 校验一次性 token。
// remoteIP 可为空（CF 允许不带 remoteip）。超时 8s，网络失败按验证失败处理。
func verifyTurnstileToken(secretKey, token, remoteIP string) error {
	form := url.Values{}
	form.Set("secret", secretKey)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.PostForm(turnstileSiteverifyURL, form)
	if err != nil {
		return fmt.Errorf("无法连接 Turnstile 校验服务: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("读取 Turnstile 响应失败: %w", err)
	}
	var result struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("解析 Turnstile 响应失败: %w", err)
	}
	if !result.Success {
		codes := strings.Join(result.ErrorCodes, ",")
		if codes == "" {
			codes = "unknown"
		}
		return fmt.Errorf("人机验证未通过 (%s)", codes)
	}
	return nil
}

// requireTurnstile 是登录端点的前置校验：启用且未通过 → 401 并计入限流。
// 返回 false 表示已写响应，调用方应直接 return。
func requireTurnstile(w http.ResponseWriter, r *http.Request, enabled bool, rateKey, token string) bool {
	if !enabled {
		return true
	}
	if strings.TrimSpace(token) == "" {
		loginLimiter.recordFail(rateKey)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{
			Success: false, Message: "人机验证未完成，请先完成验证",
			Data:    map[string]bool{"turnstile_required": true},
		})
		return false
	}
	config.AppConfigMu.RLock()
	secret := config.AppConfig.TurnstileSecretKey
	config.AppConfigMu.RUnlock()
	if err := verifyTurnstileToken(secret, token, clientIP(r)); err != nil {
		loginLimiter.recordFail(rateKey)
		// 校验失败细节不回传（避免向探测者暴露错误码语义），统一提示。
		jsonResponse(w, http.StatusUnauthorized, APIResponse{
			Success: false, Message: "人机验证未通过，请重试",
			Data:    map[string]bool{"turnstile_required": true},
		})
		return false
	}
	return true
}

// HandleTurnstileConfig 公开端点：登录页拉取 Turnstile 生效状态。
// 只暴露 site key（本就是公开值）与两处开关，绝不包含 secret。
func HandleTurnstileConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	config.AppConfigMu.RLock()
	siteKey := config.AppConfig.TurnstileSiteKey
	adminEnabled := turnstileEffective(siteKey, config.AppConfig.TurnstileSecretKey, config.AppConfig.TurnstileAdminLogin)
	userEnabled := turnstileEffective(siteKey, config.AppConfig.TurnstileSecretKey, config.AppConfig.TurnstileUserLogin)
	config.AppConfigMu.RUnlock()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
		"site_key":      siteKey,
		"admin_enabled": adminEnabled,
		"user_enabled":  userEnabled,
	}})
}

// HandleTurnstileSettings 管理员端点：读取/保存 Turnstile 配置。
func HandleTurnstileSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		siteKey := config.AppConfig.TurnstileSiteKey
		secretKey := config.AppConfig.TurnstileSecretKey
		adminEnabled := config.AppConfig.TurnstileAdminLogin
		userEnabled := config.AppConfig.TurnstileUserLogin
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
			"site_key":      siteKey,
			"secret_key":    maskSecret(secretKey),
			"has_secret":    secretKey != "",
			"admin_enabled": adminEnabled,
			"user_enabled":  userEnabled,
		}})

	case http.MethodPut, http.MethodPost:
		handleTurnstileSettingsUpdate(w, r)

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func handleTurnstileSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SiteKey      string `json:"site_key"`
		SecretKey    string `json:"secret_key"`
		ClearSecret  bool   `json:"clear_secret"`
		AdminEnabled bool   `json:"admin_enabled"`
		UserEnabled  bool   `json:"user_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	siteKey := strings.TrimSpace(req.SiteKey)
	// 密钥语义：留空 = 保留已保存值（设置页回显的是打码串，不回传）；
	// clear_secret=true 才显式清空；填了新值则覆盖。
	config.AppConfigMu.RLock()
	oldSecret := config.AppConfig.TurnstileSecretKey
	config.AppConfigMu.RUnlock()
	secretKey := strings.TrimSpace(req.SecretKey)
	if secretKey == "" && !req.ClearSecret {
		secretKey = oldSecret
	}
	if siteKey != "" && len(siteKey) > 128 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Site Key 过长"})
		return
	}
	if secretKey != "" && len(secretKey) > 128 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Secret Key 过长"})
		return
	}
	// 密钥不全时强制关闭开关：半套配置只会把用户挡在门外，不会更安全。
	if siteKey == "" || secretKey == "" {
		if req.AdminEnabled || req.UserEnabled {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Site Key 与 Secret Key 均需配置后才能开启验证"})
			return
		}
	}
	if err := config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		cfg.TurnstileSiteKey = siteKey
		cfg.TurnstileSecretKey = secretKey
		cfg.TurnstileAdminLogin = req.AdminEnabled
		cfg.TurnstileUserLogin = req.UserEnabled
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	// 审计只记录密钥指纹（SHA-256 前 8 位）与是否更换，不落原值。
	secretChanged := secretKey != oldSecret
	auditRequest(r, "settings.turnstile", "turnstile",
		fmt.Sprintf("admin_login=%s user_login=%s site_key=%s secret_changed=%v", boolStr(req.AdminEnabled), boolStr(req.UserEnabled), turnstileKeyFingerprint(siteKey), secretChanged),
		true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
		"site_key":      siteKey,
		"secret_key":    maskSecret(secretKey),
		"has_secret":    secretKey != "",
		"admin_enabled": req.AdminEnabled,
		"user_enabled":  req.UserEnabled,
	}})
}

// HandleTurnstileVerify 管理员端点：设置页「测试」按钮。
// 请求携带 widget 产生的一次性 token，可选携带未保存的 site/secret 做「先测后存」。
func HandleTurnstileVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Token    string `json:"token"`
		SiteKey  string `json:"site_key"`
		Secret   string `json:"secret_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "缺少验证 token，请先完成人机验证"})
		return
	}
	// 测试密钥优先取请求内携带值（测试未保存的配置），否则用已保存的。
	secret := strings.TrimSpace(req.Secret)
	if secret == "" {
		config.AppConfigMu.RLock()
		secret = config.AppConfig.TurnstileSecretKey
		config.AppConfigMu.RUnlock()
	}
	if secret == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "未配置 Secret Key，请先填写并保存"})
		return
	}
	if err := verifyTurnstileToken(secret, token, clientIP(r)); err != nil {
		auditRequest(r, "settings.turnstile_test", "turnstile", "result=fail", false, err.Error())
		jsonResponse(w, http.StatusOK, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "settings.turnstile_test", "turnstile", "result=ok", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "验证通过，密钥配置有效"})
}

// maskSecret 打码回显：只保留前 4 位与长度，足够识别「是否已配置/是否换了 key」。
func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:4] + strings.Repeat("*", 8)
}

// turnstileKeyFingerprint 返回密钥的 SHA-256 前 8 位，供审计日志脱敏记录。
func turnstileKeyFingerprint(key string) string {
	if key == "" {
		return "(未配置)"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}
