package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTurnstileEffective 开关 + 密钥齐全才视为生效（任一缺失/关闭均不生效）。
func TestTurnstileEffective(t *testing.T) {
	cases := []struct {
		name               string
		siteKey, secretKey string
		enabled            bool
		want               bool
	}{
		{"enabled and configured", "sk", "sec", true, true},
		{"switched off", "sk", "sec", false, false},
		{"missing site key", "", "sec", true, false},
		{"missing secret", "sk", "", true, false},
	}
	for _, c := range cases {
		if got := turnstileEffective(c.siteKey, c.secretKey, c.enabled); got != c.want {
			t.Fatalf("%s: turnstileEffective = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRequireTurnstileGates requireTurnstile 的两类免网络门控：
// 未启用直接放行；启用且 token 为空 → 401 + turnstile_required 标记 + 计入限流。
func TestRequireTurnstileGates(t *testing.T) {
	// 未启用 → 放行（不写响应）
	rec := httptest.NewRecorder()
	if !requireTurnstile(rec, httptest.NewRequest(http.MethodPost, "/login", nil), false, "rk", "") {
		t.Fatal("disabled turnstile must pass through")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("disabled turnstile must not write a response, got %d %q", rec.Code, rec.Body.String())
	}

	// 启用 + 空 token → 401，响应带 turnstile_required，且失败计入限流
	loginLimiter.reset("rk-t")
	rec = httptest.NewRecorder()
	if requireTurnstile(rec, httptest.NewRequest(http.MethodPost, "/login", nil), true, "rk-t", "   ") {
		t.Fatal("blank token must be rejected when enabled")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "turnstile_required") {
		t.Fatalf("response should carry turnstile_required flag: %s", rec.Body.String())
	}
	// 连续空 token 拒绝累计到阈值（共 5 次）后，登录限流器应进入封锁状态
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		if requireTurnstile(rec, httptest.NewRequest(http.MethodPost, "/login", nil), true, "rk-t", "") {
			t.Fatal("blank token must be rejected when enabled")
		}
	}
	if loginLimiter.allow("rk-t") {
		t.Fatal("5 blank-token rejections must trip the login limiter")
	}
	loginLimiter.reset("rk-t")
}
