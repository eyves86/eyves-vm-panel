package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// TestVerifyTOTPRejectsReplay 验证 TOTP 重放防护（审计 H-4）：
// 同一时间步的验证码只能被消费一次。
func TestVerifyTOTPRejectsReplay(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret: %v", err)
	}
	now := time.Now()
	code, err := totpCodeAt(secret, now)
	if err != nil {
		t.Fatalf("totpCodeAt: %v", err)
	}

	counter, ok := verifyTOTPWithCounter(secret, code, 0, now)
	if !ok {
		t.Fatal("first use of a valid code must be accepted")
	}
	if counter == 0 {
		t.Fatal("counter must be returned for persistence")
	}

	// 同一时间步再次使用同一验证码 → 拒绝（重放）。
	if _, ok := verifyTOTPWithCounter(secret, code, counter, now); ok {
		t.Fatal("replay of an already-consumed TOTP code must be rejected")
	}

	// 窗口内的其它时间步（若已消费步更大）同样拒绝。
	if _, ok := verifyTOTPWithCounter(secret, code, counter+10, now); ok {
		t.Fatal("code older than the last consumed step must be rejected")
	}

	// 错误验证码拒绝，且不消耗计数。
	if _, ok := verifyTOTPWithCounter(secret, "000000", 0, now); ok && code != "000000" {
		t.Fatal("invalid code must be rejected")
	}

	// 长度不符的输入直接拒绝。
	if _, ok := verifyTOTPWithCounter(secret, "123", 0, now); ok {
		t.Fatal("short code must be rejected")
	}
}

// TestTOTPWindowAllowsAdjacentStep 仍允许 ±1 个周期的时钟偏差。
func TestTOTPWindowAllowsAdjacentStep(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret: %v", err)
	}
	now := time.Now()
	prev, err := totpCodeAt(secret, now.Add(-totpPeriod*time.Second))
	if err != nil {
		t.Fatalf("totpCodeAt: %v", err)
	}
	if _, ok := verifyTOTPWithCounter(secret, prev, 0, now); !ok {
		t.Fatal("previous step code must still be accepted (clock skew tolerance)")
	}
}

// TestSessionCookieAuthAndCSRF 验证 Cookie 会话（审计 H-6）：
//  1. 登录 Cookie 为 HttpOnly + SameSite=Lax，HTTPS 下带 Secure；
//  2. 无 Authorization 头时令牌可从 Cookie 解析；
//  3. Cookie 认证的跨站状态变更被拒（CSRF 纵深防御），同站放行。
func TestSessionCookieAuthAndCSRF(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/login", nil)
	setSessionCookie(rec, req, "jwt-token-value")

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if !cookie.Secure {
		t.Fatal("session cookie must be Secure on HTTPS requests")
	}
	if cookie.MaxAge != sessionCookieMaxAge {
		t.Fatalf("cookie MaxAge = %d, want %d", cookie.MaxAge, sessionCookieMaxAge)
	}

	// 明文 HTTP 下不下发 Secure，否则浏览器会直接丢弃 Cookie。
	recPlain := httptest.NewRecorder()
	setSessionCookie(recPlain, httptest.NewRequest(http.MethodPost, "http://panel.example.com/api/login", nil), "t")
	if recPlain.Result().Cookies()[0].Secure {
		t.Fatal("session cookie must not be Secure on plain HTTP (browser would drop it)")
	}

	// 令牌解析：Cookie 回退生效。
	authed := httptest.NewRequest(http.MethodGet, "https://panel.example.com/api/containers", nil)
	authed.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-token"})
	if got := tokenFromRequest(authed); got != "cookie-token" {
		t.Fatalf("tokenFromRequest(cookie) = %q", got)
	}
	// Authorization 优先于 Cookie。
	authed.Header.Set("Authorization", "Bearer header-token")
	if got := tokenFromRequest(authed); got != "header-token" {
		t.Fatalf("Authorization must win over cookie, got %q", got)
	}

	// CSRF：Cookie 认证 + 跨站 Origin + 状态变更 → 拒绝。
	csrf := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/containers", nil)
	csrf.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-token"})
	csrf.Header.Set("Origin", "https://evil.example")
	if cookieCSRFGuard(csrf) {
		t.Fatal("cross-site cookie-authenticated POST must be rejected")
	}

	// 同站 Origin → 放行。
	sameSite := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/containers", nil)
	sameSite.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-token"})
	sameSite.Header.Set("Origin", "https://panel.example.com")
	if !cookieCSRFGuard(sameSite) {
		t.Fatal("same-site cookie-authenticated POST must be allowed")
	}

	// Bearer 客户端（无 Cookie）不受 CSRF 校验影响。
	bearer := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/containers", nil)
	bearer.Header.Set("Authorization", "Bearer header-token")
	bearer.Header.Set("Origin", "https://evil.example")
	if !cookieCSRFGuard(bearer) {
		t.Fatal("bearer-authenticated request must not be blocked by the cookie CSRF guard")
	}

	// GET 不受限制（读操作幂等）。
	getReq := httptest.NewRequest(http.MethodGet, "https://panel.example.com/api/containers", nil)
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-token"})
	getReq.Header.Set("Origin", "https://evil.example")
	if !cookieCSRFGuard(getReq) {
		t.Fatal("GET requests must not be blocked by the CSRF guard")
	}

	// 登出清理 Cookie。
	clearRec := httptest.NewRecorder()
	clearSessionCookie(clearRec)
	cleared := clearRec.Result().Cookies()[0]
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie, got value=%q maxAge=%d", cleared.Value, cleared.MaxAge)
	}
}

// TestCookieCSRFGuardUsesConfiguredOrigins 校验显式放行的 Origin 白名单同样生效。
func TestCookieCSRFGuardUsesConfiguredOrigins(t *testing.T) {
	config.AppConfigMu.Lock()
	prevCfg := config.AppConfig
	config.AppConfig = &config.EyvescloudConfig{WebSSHAllowedOrigins: []string{"https://billing.example.com"}}
	config.AppConfigMu.Unlock()
	defer func() {
		config.AppConfigMu.Lock()
		config.AppConfig = prevCfg
		config.AppConfigMu.Unlock()
	}()

	req := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/containers", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-token"})
	req.Header.Set("Origin", "https://billing.example.com")
	if !cookieCSRFGuard(req) {
		t.Fatal("allowlisted origin must be accepted")
	}
}
