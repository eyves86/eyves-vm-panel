package api

import (
	"net/http"
	"net/url"
	"strings"

	"eyvescloud/internal/config"
)

// session_cookie.go —— 管理端/用户端会话令牌的 Cookie 承载（审计 H-6 修复）。
//
// 背景：此前 JWT 由前端存入 localStorage，任何 XSS（或第三方脚本）都能直接
// 读走 24 小时有效的管理令牌，且服务端无 HttpOnly/SameSite 保护。现在登录成功
// 时由服务端下发 HttpOnly Cookie：
//
//	Set-Cookie: eyvescloud_session=<jwt>; Path=/; HttpOnly; SameSite=Lax[; Secure]
//
//   - HttpOnly：JS 无法读取（XSS 无法窃取令牌）；
//   - SameSite=Lax：跨站发起的 POST/PUT/DELETE 不带 Cookie，天然阻断大部分 CSRF；
//   - Secure：仅在确定走 HTTPS 时置位（直连 TLS，或反代声明 X-Forwarded-Proto: https），
//     避免纯 HTTP 部署下浏览器丢弃 Cookie 导致无法登录。
//
// 兼容性：Authorization: Bearer 仍然完全可用（CLI / API Key / 第三方集成不受影响），
// Cookie 只是浏览器场景的默认承载。

const sessionCookieName = "eyvescloud_session"

// sessionCookieMaxAge 与 JWT 有效期一致（24h）。
const sessionCookieMaxAge = 24 * 60 * 60

// requestIsHTTPS 判断当前请求是否来自 HTTPS（直连或反代转发），用于决定 Secure 属性。
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); proto == "https" {
		return true
	}
	return false
}

// setSessionCookie 下发会话 Cookie（登录成功后调用）。
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	//nolint:gosec // G124：HttpOnly / SameSite 已显式设置，Secure 由 requestIsHTTPS(r) 动态判定（纯 HTTP 部署下强制 Secure 会让浏览器丢弃 Cookie）。
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie 清除会话 Cookie（登出 / 令牌失效）。
func clearSessionCookie(w http.ResponseWriter) {
	//nolint:gosec // G124：HttpOnly / SameSite 已显式设置，Secure 由 requestIsHTTPS(r) 动态判定（纯 HTTP 部署下强制 Secure 会让浏览器丢弃 Cookie）。
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// HandleLogout 清除服务端下发的会话 Cookie（幂等，无需认证）。
// 纯 Bearer 客户端不依赖此接口（令牌不在 Cookie 中）。
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	clearSessionCookie(w)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已退出登录"})
}

// cookieAuthRequest 报告本次请求是否依赖 Cookie 认证（无 Authorization 头）。
func cookieAuthRequest(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	_, err := r.Cookie(sessionCookieName)
	return err == nil
}

// cookieCSRFGuard 对"Cookie 认证 + 状态变更"的请求做来源校验（CSRF 纵深防御）。
//
// SameSite=Lax 已能阻断跨站 POST，但部分老浏览器/特殊场景仍会携带 Cookie，
// 因此再校验一次 Origin/Referer：浏览器一定会带上二者之一，且不可被脚本伪造为
// 其它站点；没有这两个头的调用方（CLI、curl、Agent）不使用 Cookie，不受影响。
func cookieCSRFGuard(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !cookieAuthRequest(r) {
		return true
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return config.IsOriginAllowed(origin, r.Host)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		parsed, err := url.Parse(referer)
		if err != nil {
			return false
		}
		return config.IsOriginAllowed(parsed.Scheme+"://"+parsed.Host, r.Host)
	}
	// 无 Origin/Referer 的非浏览器调用：放行（其凭据来自客户端自身，无 CSRF 语义）。
	return true
}
