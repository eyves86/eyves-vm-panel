package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

type APIResponse struct {
	Success bool        `json:"success"`
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
}

const (
	authTypeAdmin   = "admin"
	authTypeSubUser = "sub_user"
	authTypeAPIKey  = "api_key"

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

func requestActor(r *http.Request) string {
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
	case "container:power", "container:reinstall", "container:password", "container:network",
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
	jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Insufficient API key scope"})
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

func auditRequest(r *http.Request, action, target, detail string, success bool, errMsg string) {
	config.AddAuditLogFull(action, target, detail, requestActor(r), clientIP(r), r.UserAgent(), success, errMsg)
}

func jsonResponse(w http.ResponseWriter, status int, resp APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

func tokenFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
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
		foundSubUser := false
		config.AppConfigMu.RLock()
		for i := range config.AppConfig.SubUsers {
			if config.AppConfig.SubUsers[i].Username == subUser {
				foundSubUser = true
				stored := config.AppConfig.SubUsers[i].TokenVersion
				// If stored version > 0, require token_version to match exactly.
				// This also rejects legacy tokens that lack token_version entirely.
				if stored > 0 && (!hasVersion || tokenVersion != stored) {
					config.AppConfigMu.RUnlock()
					return nil, false
				}
				break
			}
		}
		config.AppConfigMu.RUnlock()
		if !foundSubUser {
			return nil, false
		}
		return claims, ok
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
	}

	return claims, ok
}

func claimsFromRequest(r *http.Request) (jwt.MapClaims, bool) {
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
	rateKey := ip + "|admin:" + req.Username
	if loginRateLimited(w, rateKey) {
		return
	}

	// Snapshot admin credentials under the read lock so concurrent password /
	// username changes cannot tear the values being compared and signed.
	config.AppConfigMu.RLock()
	adminUser := config.AppConfig.AdminUser
	adminPassHash := config.AppConfig.AdminPassHash
	adminTokenVersion := config.AppConfig.AdminTokenVersion
	jwtSecret := config.AppConfig.JWTSecret
	config.AppConfigMu.RUnlock()

	if req.Username != adminUser {
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
			_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.AdminBackupCodes = consumed })
		}
	}

	// Generate JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username":      req.Username,
		"token_version": adminTokenVersion,
		"iss":           jwtIssuer,
		"aud":           jwtAudience,
		"exp":           time.Now().Add(24 * time.Hour).Unix(),
		"iat":           time.Now().Unix(),
	})

	tokenString, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate token"})
		return
	}

	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data: LoginResponse{
			Token:    tokenString,
			Username: req.Username,
		},
	})
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

	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.AdminPassHash = string(hash)
		cfg.AdminTokenVersion++ // invalidate all previously issued admin tokens
	})
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
	if ctx.Type == authTypeSubUser {
		role = ctx.Role
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Authenticated",
		Data: map[string]interface{}{
			"type":              ctx.Type,
			"username":          ctx.Username,
			"sub_user":          ctx.Type == authTypeSubUser,
			"role":              role,
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
			return []string{"container:read", "container:power", "container:reinstall", "container:password", "container:network", "dashboard:read", "image:read", "snapshot:read", "snapshot:create", "snapshot:delete", "snapshot:restore", "snapshot:schedule", "terminal:ssh", "terminal:vnc"}
		}
	default:
		return append([]string(nil), ctx.Scopes...)
	}
}

// AuthMiddleware extracts JWT from cookies or Authorization header
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenString := tokenFromRequest(r)
		if claims, ok := claimsFromToken(tokenString); ok {
			if subUser, _ := claims["sub_user"].(string); subUser != "" {
				auth := AuthContext{Type: authTypeSubUser, Username: subUser, Actor: "user:" + subUser}
				if role, _ := claims["role"].(string); role != "" {
					auth.Role = role
				}
				if values, ok := claims["container_uuids"].([]interface{}); ok {
					for _, value := range values {
						if uuid, ok := value.(string); ok {
							auth.ContainerUUIDs = append(auth.ContainerUUIDs, uuid)
						}
					}
				}
				next(w, withAuthContext(r, auth))
				return
			}
			username, _ := claims["username"].(string)
			if username == "" {
				username = config.AppConfig.AdminUser
			}
			next(w, withAuthContext(r, AuthContext{Type: authTypeAdmin, Username: username, Actor: username}))
			return
		}

		if key, ok := validateApiKeyRequest(r); ok {
			next(w, withAuthContext(r, authContextFromAPIKey(key)))
			return
		}

		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Authentication required"})
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
