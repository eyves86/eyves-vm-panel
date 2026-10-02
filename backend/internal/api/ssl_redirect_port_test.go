package api

// ssl_redirect_port_test.go —— TLS 的 HTTP 跳转端口配置校验。
//
// 背景：启用 TLS 后面板单端口只服务 HTTPS，既有 HTTP 访问会直接连接失败。
// http_redirect_port 把这次断裂变成透明跳转，这里锁定其输入校验。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func putSSLSettings(t *testing.T, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化请求失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/ssl", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	updateSSLSettings(rec, req)
	return rec
}

func TestSSLHTTPRedirectPortValidation(t *testing.T) {
	cfg := withSSLTestConfig(t)
	cfg.Port = 8999
	cfg.AdminUser = "admin"

	const target = "203.0.113.30"

	// 与面板端口相同 → 拒绝（否则会自己跟自己抢端口）
	rec := putSSLSettings(t, map[string]any{
		"enabled": true, "mode": "self_signed", "target": target,
		"http_redirect_port": 8999,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("跳转端口与面板端口相同时应 400，实际 %d（%s）", rec.Code, rec.Body.String())
	}

	// 超出端口范围 → 拒绝
	rec = putSSLSettings(t, map[string]any{
		"enabled": true, "mode": "self_signed", "target": target,
		"http_redirect_port": 70000,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("跳转端口越界时应 400，实际 %d（%s）", rec.Code, rec.Body.String())
	}

	// 合法端口 → 接受并持久化
	rec = putSSLSettings(t, map[string]any{
		"enabled": true, "mode": "self_signed", "target": target,
		"http_redirect_port": 8998,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("合法跳转端口应 200，实际 %d（%s）", rec.Code, rec.Body.String())
	}
	if got := cfg.SSL.HTTPRedirectPort; got != 8998 {
		t.Fatalf("跳转端口应持久化为 8998，实际 %d", got)
	}
	if !cfg.SSL.Enabled {
		t.Fatal("SSL 应处于启用状态")
	}
}

// TestSSLRedirectPortOmittedKeepsExistingValue 不传该字段时（nil 指针）不得清空既有配置。
func TestSSLRedirectPortOmittedKeepsExistingValue(t *testing.T) {
	cfg := withSSLTestConfig(t)
	cfg.Port = 8999
	cfg.AdminUser = "admin"

	const target = "203.0.113.31"

	rec := putSSLSettings(t, map[string]any{
		"enabled": true, "mode": "self_signed", "target": target,
		"http_redirect_port": 8997,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("首次设置应 200，实际 %d", rec.Code)
	}

	// 第二次请求完全不带该字段
	rec = putSSLSettings(t, map[string]any{
		"enabled": true, "mode": "self_signed", "target": target,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("再次保存应 200，实际 %d（%s）", rec.Code, rec.Body.String())
	}
	if got := cfg.SSL.HTTPRedirectPort; got != 8997 {
		t.Fatalf("未提供该字段时不应改动既有值，期望 8997，实际 %d", got)
	}
}
