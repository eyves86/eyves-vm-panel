package api

// apiv2_auth.go —— API v2：认证与会话。
//
//	POST /api/v2/auth/login   登录（管理员或子用户），返回 access_token
//	POST /api/v2/auth/logout  退出（无状态 token，客户端丢弃即可）
//	GET  /api/v2/auth/me      当前身份、角色与权限点（前端按钮渲染依据）

import (
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"

	"golang.org/x/crypto/bcrypt"
)

func init() {
	registerV2("POST /api/v2/auth/login", v2AuthLogin)
	registerV2("POST /api/v2/auth/logout", v2Auth(v2AuthLogout))
	registerV2("GET /api/v2/auth/me", v2Auth(v2AuthMe))
}

// v2LoginRequest 登录请求体。
type v2LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Code 是管理员两步验证动态口令（未启用 2FA 时忽略）。
	Code string `json:"code"`
	// Type 指定登录身份：admin（默认，管理员）| client（子用户）。
	Type string `json:"type"`
}

func v2AuthLogin(w http.ResponseWriter, r *http.Request) {
	var req v2LoginRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"username": req.Username, "password": req.Password}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Type))
	if kind == "client" || kind == "user" || kind == "subuser" {
		v2LoginClient(w, r, req)
		return
	}
	v2LoginAdmin(w, r, req)
}

func v2LoginAdmin(w http.ResponseWriter, r *http.Request, req v2LoginRequest) {
	ip := clientIP(r)
	ua := r.UserAgent()
	// 限流桶键只用 IP：用户名是攻击者可控输入，含进键名（ip|v2-admin:username）
	// 允许攻击者轮换用户名绕过阈值（渗透测试经验：限流键不要含攻击者可控串）。
	rateKey := ip + "|v2-admin"
	if loginRateLimited(w, rateKey) {
		v2Error(w, r, http.StatusTooManyRequests, v2CodeRateLimited, "登录尝试过于频繁，请稍后再试", nil)
		return
	}
	config.AppConfigMu.RLock()
	adminUser := config.AppConfig.AdminUser
	adminHash := config.AppConfig.AdminPassHash
	tokenVersion := config.AppConfig.AdminTokenVersion
	totpEnabled := config.AppConfig.AdminTOTPEnabled
	totpSecret := config.AppConfig.AdminTOTPSecret
	backupHashes := append([]string(nil), config.AppConfig.AdminBackupCodes...)
	config.AppConfigMu.RUnlock()

	if req.Username == adminUser {
		if bcrypt.CompareHashAndPassword([]byte(adminHash), []byte(req.Password)) != nil {
			loginLimiter.recordFail(rateKey)
			RecordLoginLog(req.Username, ip, ua, false)
			v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, "账号或密码错误", nil)
			return
		}
		if totpEnabled {
			passed, consumed := adminVerify2FA(totpSecret, totpEnabled, req.Code, backupHashes)
			if !passed {
				loginLimiter.recordFail(rateKey)
				RecordLoginLog(req.Username, ip, ua, false)
				v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, "两步验证码错误或已过期", nil)
				return
			}
			if consumed != nil && len(consumed) != len(backupHashes) {
				_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.AdminBackupCodes = consumed })
			}
		}
		token, err := v2IssueToken(adminUser, true, "", "", nil, "", tokenVersion)
		if err != nil {
			v2Internal(w, r, "签发 token 失败")
			return
		}
		loginLimiter.reset(rateKey)
		RecordLoginLog(adminUser, ip, ua, true)
		config.DeleteFirstBootCredentialsIfExists()
		v2OK(w, r, v2TokenPayload(token, adminUser, "admin", config.AdminRoleAdmin))
		return
	}

	if acct, ok := config.FindAdminAccount(req.Username); ok {
		if acct.Disabled || bcrypt.CompareHashAndPassword([]byte(acct.PassHash), []byte(req.Password)) != nil {
			loginLimiter.recordFail(rateKey)
			RecordLoginLog(req.Username, ip, ua, false)
			v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, "账号或密码错误", nil)
			return
		}
		token, err := v2IssueToken(acct.Username, true, acct.ID, acct.Role, nil, "", acct.TokenVersion)
		if err != nil {
			v2Internal(w, r, "签发 token 失败")
			return
		}
		loginLimiter.reset(rateKey)
		RecordLoginLog(acct.Username, ip, ua, true)
		v2OK(w, r, v2TokenPayload(token, acct.Username, "admin", acct.Role))
		return
	}

	loginLimiter.recordFail(rateKey)
	RecordLoginLog(req.Username, ip, ua, false)
	v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, "账号或密码错误", nil)
}

func v2LoginClient(w http.ResponseWriter, r *http.Request, req v2LoginRequest) {
	ip := clientIP(r)
	ua := r.UserAgent()
	// 同上：桶键只用 IP，防止轮换用户名绕过限流。
	rateKey := ip + "|v2-client"
	if loginRateLimited(w, rateKey) {
		v2Error(w, r, http.StatusTooManyRequests, v2CodeRateLimited, "登录尝试过于频繁，请稍后再试", nil)
		return
	}
	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	config.AppConfigMu.RUnlock()
	for _, su := range subUsers {
		if !strings.EqualFold(su.Username, req.Username) {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(req.Password)) != nil {
			break
		}
		uuids := activeSubUserContainerUUIDs(&su)
		if len(uuids) == 0 {
			v2Error(w, r, http.StatusForbidden, v2CodePermissionDenied, "该账号没有已分配的实例", nil)
			return
		}
		token, err := v2IssueToken(su.Username, false, "", "", uuids, su.Role, su.TokenVersion)
		if err != nil {
			v2Internal(w, r, "签发 token 失败")
			return
		}
		loginLimiter.reset(rateKey)
		RecordLoginLog(su.Username, ip, ua, true)
		v2OK(w, r, v2TokenPayload(token, su.Username, "client", subUserRole(su.Role)))
		return
	}
	loginLimiter.recordFail(rateKey)
	RecordLoginLog(req.Username, ip, ua, false)
	v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, "账号或密码错误", nil)
}

// v2TokenPayload 登录响应体（字段稳定，不含冗余别名）。
func v2TokenPayload(token, username, kind, role string) map[string]interface{} {
	expiresAt := time.Now().Add(v2TokenTTL)
	return map[string]interface{}{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(v2TokenTTL.Seconds()),
		"expires_at":   expiresAt.Format(time.RFC3339),
		"username":     username,
		"type":         kind, // admin | client
		"role":         role,
	}
}

func v2AuthLogout(w http.ResponseWriter, r *http.Request) {
	ctx := v2AuthContext(r)
	auditRequest(r, "api.v2.auth.logout", ctx.Username, "退出登录", true, "")
	v2NoContent(w, r)
}

func v2AuthMe(w http.ResponseWriter, r *http.Request) {
	ctx := v2AuthContext(r)
	scopes := defaultScopesForType(ctx)
	features := map[string]bool{
		"instance_create":    v2ScopeAllowedForCtx(ctx, "container:create"),
		"instance_update":    v2ScopeAllowedForCtx(ctx, "container:account"),
		"instance_delete":    v2ScopeAllowedForCtx(ctx, "container:create"),
		"instance_power":     v2ScopeAllowedForCtx(ctx, "container:power"),
		"instance_reinstall": v2ScopeAllowedForCtx(ctx, "container:reinstall"),
		"instance_password":  v2ScopeAllowedForCtx(ctx, "container:password"),
		"instance_console":   v2ScopeAllowedForCtx(ctx, "terminal:ssh"),
		"instance_vnc":       v2ScopeAllowedForCtx(ctx, "terminal:vnc"),
		"instance_snapshot":  v2ScopeAllowedForCtx(ctx, "snapshot:read"),
		"node_manage":        ctx.Type == authTypeAdmin,
		"image_manage":       ctx.Type == authTypeAdmin,
		"security_manage":    ctx.Type == authTypeAdmin,
		"user_manage":        ctx.Type == authTypeAdmin,
		"system_manage":      ctx.Type == authTypeAdmin,
		"audit_read":         ctx.Type == authTypeAdmin,
		"monitor_read":       v2ScopeAllowedForCtx(ctx, "dashboard:read"),
	}
	data := map[string]interface{}{
		"type":             ctx.Type,
		"username":         ctx.Username,
		"role":             ctx.Role,
		"scopes":           scopes,
		"features":         features,
		"container_uuids":  ctx.ContainerUUIDs,
		"api_version":      "v2",
		"panel_version":    versionString(),
		"server_time":      time.Now().Format(time.RFC3339),
	}
	if ctx.Type == authTypeAdmin {
		config.AppConfigMu.RLock()
		data["is_founder"] = strings.EqualFold(ctx.Username, config.AppConfig.AdminUser)
		data["admin_path"] = config.CurrentAdminPath()
		config.AppConfigMu.RUnlock()
	}
	v2OK(w, r, data)
}
