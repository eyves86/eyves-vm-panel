package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"eyvescloud/internal/config"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	TwoFACode string `json:"twofa_code"`
	// TurnstileToken 是 Cloudflare Turnstile 人机验证一次性 token；
	// 管理员登录启用 Turnstile 时必填（校验发生在密码比对之前）。
	TurnstileToken string `json:"turnstile_token"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

type APIResponse struct {
	Success bool `json:"success"`
	// Code 是机器可读错误码（企业级 API 契约）：仅在失败时出现，如
	// INVALID_REQUEST / NOT_FOUND / FORBIDDEN / RATE_LIMITED / BAD_GATEWAY /
	// INTERNAL_ERROR。成功响应省略该字段，向后兼容。
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type authContextKey struct{}

type AuthContext struct {
	Type           string
	Username       string
	ApiKeyID       string
	ApiKeyName     string
	Actor          string
	Scopes         []string
	ContainerUUIDs []string
	Role           string // 子用户角色 operator/viewer；仅登录凭据持有的写权限受其约束
	// Via 记录子用户会话的来源（account / access_code）。访问码会话据此
	// 被禁止修改账号密码（见 HandleSubUserChangePassword / SelfRotatePassword）。
	Via string
}

const (
	authTypeAdmin   = "admin"
	authTypeSubUser = "sub_user"
	authTypeAPIKey  = "api_key"
	// authTypeAgent 表示请求已通过节点 token 校验（主控 → 被控的 agent API）。
	// 仅该类型允许采信 X-Original-Actor（见 requestActor 的安全约束）。
	authTypeAgent = "agent"

	// jwtIssuer / jwtAudience 用于校验令牌签发方与用途，防止跨服务令牌重放。
	jwtIssuer   = "eyvescloud"
	jwtAudience = "eyvescloud-panel"
)

func withAuthContext(r *http.Request, auth AuthContext) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth))
}

func authContextFromRequest(r *http.Request) (AuthContext, bool) {
	ctx, ok := r.Context().Value(authContextKey{}).(AuthContext)
	return ctx, ok
}

// claimsContextKey 缓存鉴权中间件已验证的 JWT claims：一个请求内多个 handler
// （requestActor / isSubUserRequest / subuser / vnc）各自调 claimsFromRequest 时
// 不再重复做 HMAC 验签。缓存只存进请求 context，随请求结束失效——吊销语义不变。
type claimsContextKey struct{}

func withClaims(r *http.Request, claims jwt.MapClaims) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), claimsContextKey{}, claims))
}

func requestActor(r *http.Request) string {
	// 多节点转发场景：主控 → agent 转发时把原始请求的 actor 写到 X-Original-Actor。
	// agent 端审计时应优先使用这个值，避免把"agent 自身 token"记成操作人。
	//
	// 安全约束（审计 H-3）：该 header 只在**请求已通过节点 token 校验**
	// （AgentTokenMiddleware 注入标记）时才被采信——主控是唯一持有节点 token 的
	// 调用方，因此该值可信；其余路径（浏览器 / API Key / 未认证）一律忽略客户端
	// 传入的 header，防止把操作伪造成他人名下。
	if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeAgent {
		if orig := strings.TrimSpace(r.Header.Get("X-Original-Actor")); orig != "" {
			return orig
		}
	}
	if ctx, ok := authContextFromRequest(r); ok && ctx.Actor != "" {
		return ctx.Actor
	}
	if claims, ok := claimsFromRequest(r); ok {
		if subUser, _ := claims["sub_user"].(string); subUser != "" {
			return "user:" + subUser
		}
		if username, _ := claims["username"].(string); username != "" {
			return username
		}
	}
	return "admin"
}

func hasScope(r *http.Request, scope string) bool {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		// Fail closed: an unauthenticated request must never be granted a scope.
		return false
	}
	switch ctx.Type {
	case authTypeAdmin:
		return true
	case authTypeSubUser:
		return subUserScopeAllowed(scope, ctx.Role)
	case authTypeAPIKey:
		return scopeAllowed(ctx.Scopes, scope)
	default:
		return false
	}
}

// subUserScopeAllowed 判断子用户是否拥有某 scope。operator 拥有容器读写、
// 密码/网络/重装/快照等操作权限；viewer（只读）仅允许读类与连接类 scope，
// 拒绝密码重置、重装、电源控制等写操作，避免只读子用户越权修改服务器。
func subUserScopeAllowed(scope string, role string) bool {
	viewer := strings.EqualFold(strings.TrimSpace(role), "viewer")
	switch scope {
	case "container:read", "dashboard:read", "image:read", "task:read", "snapshot:read",
		"terminal:ssh", "terminal:vnc":
		return true
	case "container:power", "container:reinstall", "container:password", "container:account", "container:network",
		"container:ssh-key",
		"snapshot:create", "snapshot:delete", "snapshot:restore", "snapshot:schedule":
		return !viewer
	default:
		return false
	}
}

func hasAnyScope(r *http.Request, scopes ...string) bool {
	for _, scope := range scopes {
		if hasScope(r, scope) {
			return true
		}
	}
	return false
}

func scopeAllowed(scopes []string, required string) bool {
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "*" || scope == "admin:*" || scope == required {
			return true
		}
		if strings.HasSuffix(scope, ":*") {
			prefix := strings.TrimSuffix(scope, "*")
			if strings.HasPrefix(required, prefix) {
				return true
			}
		}
	}
	return false
}

func requireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if hasScope(r, scope) {
		return true
	}
	errResponse(w, http.StatusForbidden, "INSUFFICIENT_SCOPE", "Insufficient API key scope: "+scope)
	return false
}

func ScopeMiddleware(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, scope) {
			return
		}
		next(w, r)
	}
}

func AnyScopeMiddleware(scopes []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hasAnyScope(r, scopes...) {
			next(w, r)
			return
		}
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Insufficient API key scope"})
	}
}

var (
	auditPasswordRE      = regexp.MustCompile(`"password":"[^"]*"`)
	auditNewPasswordRE   = regexp.MustCompile(`"new_password":"[^"]*"`)
	auditTokenRE         = regexp.MustCompile(`"token":"[^"]*"`)
	auditAPIKeyRE        = regexp.MustCompile(`"api_key":"[^"]*"`)
	auditInstallKeyRE    = regexp.MustCompile(`"install_key":"[^"]*"`)
	auditSecretRE        = regexp.MustCompile(`"secret":"[^"]*"`)
	auditPasswordQueryRE = regexp.MustCompile(`password=[^&\s]*`)
)

func sanitizeAuditDetail(detail string) string {
	detail = auditPasswordRE.ReplaceAllString(detail, `"password":"***"`)
	detail = auditNewPasswordRE.ReplaceAllString(detail, `"new_password":"***"`)
	detail = auditTokenRE.ReplaceAllString(detail, `"token":"***"`)
	detail = auditAPIKeyRE.ReplaceAllString(detail, `"api_key":"***"`)
	detail = auditInstallKeyRE.ReplaceAllString(detail, `"install_key":"***"`)
	detail = auditSecretRE.ReplaceAllString(detail, `"secret":"***"`)
	detail = auditPasswordQueryRE.ReplaceAllString(detail, `password=***`)
	return detail
}

func auditRequest(r *http.Request, action, target, detail string, success bool, errMsg string) {
	detail = sanitizeAuditDetail(detail)
	config.AddAuditLogFull(action, target, detail, requestActor(r), clientIP(r), r.UserAgent(), success, errMsg)
	// 安全事件旁路：RBAC 拒绝在审计收口处统一转出站事件，三个 rbac.denied 调用点
	// （adminrbac / admins / apiv2_core）及各 v1 调用点无需各自挂 emit。
	if action == "rbac.denied" {
		emitEvent(webhookEventTypeAccessDenied, map[string]interface{}{
			"path":   r.URL.Path,
			"method": r.Method,
			"target": target,
			"detail": detail,
		})
	}
}

func jsonResponse(w http.ResponseWriter, status int, resp APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// errResponse 输出带机器可读错误码的失败响应（企业级 API 契约）。
// code 见 APIResponse.Code 注释。
func errResponse(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, APIResponse{Success: false, Code: code, Message: message})
}

func tokenFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	// 浏览器场景（审计 H-6）：会话令牌由服务端以 HttpOnly Cookie 承载，
	// JS 读不到、XSS 偷不走；CLI / API Key / 第三方集成仍走 Authorization。
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func isValidToken(tokenString string) bool {
	_, ok := claimsFromToken(tokenString)
	return ok
}

func claimsFromToken(tokenString string) (jwt.MapClaims, bool) {
	if tokenString == "" {
		return nil, false
	}
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(config.GetJWTSecret()), nil
	}, jwt.WithIssuer(jwtIssuer), jwt.WithAudience(jwtAudience))
	if err != nil || !token.Valid {
		return nil, false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false
	}

	// For sub-user tokens, check token_version against stored version (password rotation invalidation)
	if subUser, _ := claims["sub_user"].(string); subUser != "" {
		tokenVersionFloat, hasVersion := claims["token_version"].(float64)
		tokenVersion := int(tokenVersionFloat)
		config.AppConfigMu.RLock()
		i, found := config.FindSubUserIndexByNameUnlocked(subUser)
		if found {
			// If stored version > 0, require token_version to match exactly.
			// This also rejects legacy tokens that lack token_version entirely.
			stored := config.AppConfig.SubUsers[i].TokenVersion
			if stored > 0 && (!hasVersion || tokenVersion != stored) {
				config.AppConfigMu.RUnlock()
				return nil, false
			}
		}
		config.AppConfigMu.RUnlock()
		if !found {
			return nil, false
		}
		// 分布式模式：内存快照可能落后于其他副本的轮换/删除，用共享信号复核。
		// 不限 tokenVersion>0：删除墓碑（-1）必须能拦住 v0 遗留令牌。
		if c := sharedRedis(); c != nil {
			if !redisConfirmSharedVersion(c, redisRevSubKey(subUser), tokenVersion) {
				return nil, false
			}
		}
		return claims, ok
	}

	// 额外管理员（多管理员）：按账号自身的 TokenVersion 校验；账号被删除或禁用即失效。
	if adminID, _ := claims["admin_id"].(string); adminID != "" {
		acct, ok := config.FindAdminAccountByID(adminID)
		if !ok || acct.Disabled {
			return nil, false
		}
		tokenVersionFloat, hasVersion := claims["token_version"].(float64)
		if !hasVersion || int(tokenVersionFloat) != acct.TokenVersion {
			return nil, false
		}
		if c := sharedRedis(); c != nil {
			if !redisConfirmSharedVersion(c, redisRevAdminKey(acct.Username), acct.TokenVersion) {
				return nil, false
			}
		}
		return claims, true
	}

	// Admin tokens carry token_version so they can be revoked by rotating the
	// admin password or username. Once the stored version becomes non-zero,
	// every existing token (including legacy ones without the claim) is invalid.
	config.AppConfigMu.RLock()
	adminTokenVersion := config.AppConfig.AdminTokenVersion
	config.AppConfigMu.RUnlock()
	if adminTokenVersion > 0 {
		tokenVersionFloat, hasVersion := claims["token_version"].(float64)
		if !hasVersion || int(tokenVersionFloat) != adminTokenVersion {
			return nil, false
		}
		// 分布式模式：其他副本可能已轮换主管理员口令，用共享信号复核。
		if c := sharedRedis(); c != nil {
			if !redisConfirmSharedVersion(c, redisRevAdminGlobalKey(), adminTokenVersion) {
				return nil, false
			}
		}
	}

	return claims, ok
}

func claimsFromRequest(r *http.Request) (jwt.MapClaims, bool) {
	if claims, ok := r.Context().Value(claimsContextKey{}).(jwt.MapClaims); ok && claims != nil {
		return claims, true
	}
	return claimsFromToken(tokenFromRequest(r))
}

func isSubUserRequest(r *http.Request) bool {
	if ctx, ok := authContextFromRequest(r); ok {
		return ctx.Type == authTypeSubUser
	}
	claims, ok := claimsFromRequest(r)
	if !ok {
		return false
	}
	_, ok = claims["sub_user"]
	return ok
}

func isAuthenticatedRequest(r *http.Request) bool {
	return isValidToken(tokenFromRequest(r))
}

// HandleLogin processes login requests
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	ip := clientIP(r)
	ua := r.Header.Get("User-Agent")
	// 限流桶键只用 IP：用户名是攻击者可控输入，含进键名有两个后果——
	// ① 匹配语义一旦放宽（EqualFold/邮箱归一化）就能靠变体绕过阈值；
	// ② 攻击者可用无限变体名把限流器的内存桶表刷爆（DoS）。对齐 v2 的做法。
	rateKey := ip + "|admin"
	if loginRateLimited(w, rateKey) {
		return
	}

	// Turnstile 人机验证（若启用）：先于密码比对，失败同样计入限流，
	// 避免成为「密码正确性」探针。
	if !requireTurnstile(w, r, turnstileEnabledForAdmin(), rateKey, req.TurnstileToken) {
		return
	}

	// Snapshot admin credentials under the read lock so concurrent password /
	// username changes cannot tear the values being compared and signed.
	config.AppConfigMu.RLock()
	adminUser := config.AppConfig.AdminUser
	adminPassHash := config.AppConfig.AdminPassHash
	adminTokenVersion := config.AppConfig.AdminTokenVersion
	config.AppConfigMu.RUnlock()

	if req.Username != adminUser {
		// 非主管理员：尝试额外管理员账号（多管理员）。未命中则走通用“凭据无效”分支，
		// 避免通过响应差异枚举用户名。
		if handleExtraAdminLogin(w, r, req.Username, req.Password, ip, ua, rateKey) {
			return
		}
		loginLimiter.recordFail(rateKey)
		RecordLoginLog(req.Username, ip, ua, false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(adminPassHash), []byte(req.Password)); err != nil {
		loginLimiter.recordFail(rateKey)
		RecordLoginLog(req.Username, ip, ua, false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid credentials"})
		return
	}

	loginLimiter.reset(rateKey)
	RecordLoginLog(req.Username, ip, ua, true)

	// 两步验证：若已启用，必须在密码正确后提供动态口令或一次性备份码。
	config.AppConfigMu.RLock()
	totpSecret := config.AppConfig.AdminTOTPSecret
	totpEnabled := config.AppConfig.AdminTOTPEnabled
	backupHashes := append([]string(nil), config.AppConfig.AdminBackupCodes...)
	config.AppConfigMu.RUnlock()
	if totpEnabled {
		// 未提供验证码时，明确告知前端需进入两步验证步骤。
		// 同时记录一次失败以纳入登录限流，避免被利用为「密码正确性」探针。
		if strings.TrimSpace(req.TwoFACode) == "" {
			loginLimiter.recordFail(rateKey)
			jsonResponse(w, http.StatusUnauthorized, APIResponse{
				Success: false, Message: "Two-factor verification required",
				Data: map[string]bool{"twofa_required": true},
			})
			return
		}
		passed, consumed := adminVerify2FA(totpSecret, totpEnabled, req.TwoFACode, backupHashes)
		if !passed {
			loginLimiter.recordFail(rateKey)
			RecordLoginLog(req.Username, ip, ua, false)
			jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Two-factor verification failed"})
			return
		}
		// 若使用了备份码，持久化消费后的哈希列表。
		if len(consumed) != len(backupHashes) && consumed != nil {
			_ = config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) { cfg.AdminBackupCodes = consumed })
		}
	}

	// Generate JWT token
	tokenString, err := signAdminToken(req.Username, "", "", adminTokenVersion)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate token"})
		return
	}

	// 主管理员首次成功登录后，删除 DataDir 下的一次性首启凭据文件。
	// 额外管理员（handleExtraAdminLogin 分支）不会持有这份文件，所以只在
	// 命中主管理员凭据校验的分支里触发。
	if req.Username == adminUser {
		config.DeleteFirstBootCredentialsIfExists()
	}

	// 会话令牌同时以 HttpOnly Cookie 下发（审计 H-6）：浏览器不再需要把 JWT
	// 存进 localStorage；响应体里的 token 保留给 CLI / 旧客户端兼容。
	setSessionCookie(w, r, tokenString)
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data: LoginResponse{
			Token:    tokenString,
			Username: req.Username,
		},
	})
}

// signAdminToken 为主管理员（adminID 为空）或额外管理员签发管理端 JWT。
// 额外管理员会带上 admin_id 与 role，令牌有效性按其自身的 TokenVersion 校验。
func signAdminToken(username, adminID, role string, tokenVersion int) (string, error) {
	claims := jwt.MapClaims{
		"username":      username,
		"token_version": tokenVersion,
		"iss":           jwtIssuer,
		"aud":           jwtAudience,
		"exp":           time.Now().Add(24 * time.Hour).Unix(),
		"iat":           time.Now().Unix(),
	}
	if adminID != "" {
		claims["admin_id"] = adminID
		claims["role"] = config.NormalizeAdminRole(role)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GetJWTSecret()))
}

// handleExtraAdminLogin 处理额外管理员（非主管理员）的账号密码登录。
// 返回 true 表示已处理（成功或已明确拒绝）；false 表示不存在该账号，
// 由调用方统一返回“凭据无效”，以免泄露用户名是否存在。
func handleExtraAdminLogin(w http.ResponseWriter, r *http.Request, username, password, ip, ua, rateKey string) bool {
	acct, ok := config.FindAdminAccount(username)
	if !ok {
		return false
	}
	invalid := func() {
		loginLimiter.recordFail(rateKey)
		RecordLoginLog(acct.Username, ip, ua, false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid credentials"})
	}
	if acct.Disabled {
		invalid()
		return true
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acct.PassHash), []byte(password)); err != nil {
		invalid()
		return true
	}
	loginLimiter.reset(rateKey)
	RecordLoginLog(acct.Username, ip, ua, true)
	_ = config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Admins {
			if cfg.Admins[i].ID == acct.ID {
				cfg.Admins[i].LastLoginAt = time.Now().Format("2006-01-02 15:04:05")
			}
		}
	})
	tokenString, err := signAdminToken(acct.Username, acct.ID, acct.Role, acct.TokenVersion)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate token"})
		return true
	}
	setSessionCookie(w, r, tokenString)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: LoginResponse{Token: tokenString, Username: acct.Username}})
	return true
}

// HandleChangePassword processes password change requests
func HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	if err := validateStrongPassword(req.NewPassword); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(config.AppConfig.AdminPassHash), []byte(req.OldPassword)); err != nil {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Current password is incorrect"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
		return
	}

	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		cfg.AdminPassHash = string(hash)
		cfg.AdminTokenVersion++ // invalidate all previously issued admin tokens
	})
	notifyAdminGlobalRotated()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Password changed successfully"})
}

// HandleCheckAuth checks if the user is authenticated and returns the resolved
// auth context (type / role / bound containers). The frontend rebuilds its UI
// state from this server-authoritative response on refresh, so a stale JWT
// claim (e.g. an outdated role) is corrected immediately rather than trusted.
func HandleCheckAuth(w http.ResponseWriter, r *http.Request) {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Authentication required"})
		return
	}
	role := ""
	adminRole := ""
	if ctx.Type == authTypeSubUser {
		role = ctx.Role
	}
	if ctx.Type == authTypeAdmin {
		adminRole = config.NormalizeAdminRole(ctx.Role)
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Authenticated",
		Data: map[string]interface{}{
			"type":              ctx.Type,
			"username":          ctx.Username,
			"sub_user":          ctx.Type == authTypeSubUser,
			"role":              role,
			"admin_role":        adminRole,
			"container_uuids":   ctx.ContainerUUIDs,
			"permission_scopes": defaultScopesForType(ctx),
		},
	})
}

// defaultScopesForType returns the effective scope set for the given auth type
// so the frontend can reflect read-only / operator state consistently.
func defaultScopesForType(ctx AuthContext) []string {
	switch ctx.Type {
	case authTypeAdmin:
		return []string{"*"}
	case authTypeSubUser:
		switch strings.ToLower(strings.TrimSpace(ctx.Role)) {
		case "viewer":
			return []string{"container:read", "dashboard:read", "image:read", "snapshot:read", "terminal:ssh", "terminal:vnc"}
		default: // operator
			return []string{"container:read", "container:power", "container:reinstall", "container:password", "container:account", "container:network", "container:ssh-key", "dashboard:read", "image:read", "snapshot:read", "snapshot:create", "snapshot:delete", "snapshot:restore", "snapshot:schedule", "terminal:ssh", "terminal:vnc"}
		}
	default:
		return append([]string(nil), ctx.Scopes...)
	}
}

// authContextFromClaims 把已验证的 JWT claims 转换为认证上下文
// （子用户带 container_uuids/role；管理员带 admin 角色）。
func authContextFromClaims(claims jwt.MapClaims) AuthContext {
	if subUser, _ := claims["sub_user"].(string); subUser != "" {
		auth := AuthContext{Type: authTypeSubUser, Username: subUser, Actor: "user:" + subUser}
		if role, _ := claims["role"].(string); role != "" {
			auth.Role = role
		}
		if via, _ := claims["via"].(string); via != "" {
			auth.Via = via
		}
		if values, ok := claims["container_uuids"].([]interface{}); ok {
			for _, value := range values {
				if uuid, ok := value.(string); ok {
					auth.ContainerUUIDs = append(auth.ContainerUUIDs, uuid)
				}
			}
		}
		return auth
	}
	username, _ := claims["username"].(string)
	if username == "" {
		username = config.AppConfig.AdminUser
	}
	// 角色：额外管理员令牌带 role；旧令牌（主管理员）视为全权 admin。
	role := config.AdminRoleAdmin
	if claimRole, _ := claims["role"].(string); claimRole != "" {
		role = config.NormalizeAdminRole(claimRole)
	}
	return AuthContext{Type: authTypeAdmin, Username: username, Actor: username, Role: role}
}

// AuthMiddleware extracts JWT from cookies or Authorization header
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CSRF 纵深防御（审计 H-6）：Cookie 认证的状态变更请求必须来自本站来源。
		if !cookieCSRFGuard(r) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Cross-site request rejected"})
			return
		}
		tokenString := tokenFromRequest(r)
		if claims, ok := claimsFromToken(tokenString); ok {
			ctx := authContextFromClaims(claims)
			// 管理员角色（admin/operator/readonly）在鉴权入口即强制 rbac 权限点，
			// 覆盖仅挂 AuthMiddleware（未挂 AdminMiddleware）的少量路由，避免越权。
			if ctx.Type == authTypeAdmin && !enforceAdminRole(w, r, ctx.Role) {
				return
			}
			next(w, withClaims(withAuthContext(r, ctx), claims))
			return
		}

		if key, ok := validateApiKeyRequest(r); ok {
			// 单 key 限流必须在此强制：AuthMiddleware 是所有路由的唯一鉴权入口。
			if !enforceAPIKeyRateLimit(w, key) {
				return
			}
			next(w, withAuthContext(r, authContextFromAPIKey(key)))
			return
		}

		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Authentication required"})
	}
}

// OptionalAuthMiddleware 在请求携带有效凭据时注入认证上下文，但不强制认证。
// 用于 handler 内部自带认证逻辑的端点（如 install-script 的 X-Install-Key）：
// 匿名请求放行进入 handler，由其自行判定（requireScope 对缺失上下文 fail-closed，
// 仅持有效 X-Install-Key 的匿名请求可通过 handler 内的 key 校验分支）。
func OptionalAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tokenString := tokenFromRequest(r); tokenString != "" {
			if claims, ok := claimsFromToken(tokenString); ok {
				next(w, withClaims(withAuthContext(r, authContextFromClaims(claims)), claims))
				return
			}
		}
		if key, ok := validateApiKeyRequest(r); ok {
			if !enforceAPIKeyRateLimit(w, key) {
				return
			}
			next(w, withAuthContext(r, authContextFromAPIKey(key)))
			return
		}
		next(w, r)
	}
}

// AdminMiddleware requires a valid administrator token and rejects sub-user tokens.
func AdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := authContextFromRequest(r)
		if ctx.Type == authTypeSubUser {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Administrator permission required"})
			return
		}
		if ctx.Type == authTypeAPIKey && !scopeAllowed(ctx.Scopes, "admin:access") {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Administrator permission required"})
			return
		}
		// 管理员账号角色（admin/operator/readonly）按 rbac 权限点强制。
		if ctx.Type == authTypeAdmin && !enforceAdminRole(w, r, ctx.Role) {
			return
		}
		next(w, r)
	})
}

// AdminSessionMiddleware 要求**交互式管理员会话**（登录后的管理员 JWT），
// 拒绝子用户 token 与 API Key token。
//
// 用于高敏感、需本人亲自确认的操作（如两步验证/TOTP 的开启、关闭与备份码换发等）。
// 这类操作若对持 `*` 共享 scope 的 API Key 开放，会形成凭据接管面：非人工调用方可改写
// 管理员 2FA 状态、重设 TOTP 密钥或换发备份码，从而绕过管理员第二因素。
func AdminSessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := authContextFromRequest(r)
		if ctx.Type != authTypeAdmin {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "此操作仅限管理员本人会话（API Key 与子用户不可用）"})
			return
		}
		// 主管理员专属：这些操作（2FA、改主管理员密码/用户名）作用于**主管理员账号本身**，
		// 额外管理员不得操作，否则可关闭主管理员的两步验证。
		if !strings.EqualFold(ctx.Username, config.AppConfig.AdminUser) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "此操作仅限主管理员账号"})
			return
		}
		next(w, r)
	})
}

// validateStrongPassword 企业级密码策略：至少 10 位，且同时包含字母与数字。
func validateStrongPassword(password string) error {
	if len(password) < 10 {
		return fmt.Errorf("密码长度至少 10 位")
	}
	hasLetter := false
	hasDigit := false
	for _, r := range password {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("密码必须同时包含字母和数字")
	}
	return nil
}
