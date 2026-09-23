package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestSubUserListRedactsPassword 保障子用户列表不回显落库明文口令，
// 但访问码（用于生成管理分享链接，属产品设计）仍保留。
func TestSubUserListRedactsPassword(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	config.AppConfig = &config.EyvescloudConfig{
		SubUsers: []config.SubUser{
			{
				ID:            "sub-secret1",
				Username:      "user-abc",
				Password:      "SuperSecretPlaintext",
				PassHash:      "$2a$10$storedHashOnly",
				AccessCode:    "CODE12345!",
				Role:          "operator",
				ContainerNames: []string{"web-01"},
				CreatedAt:     "2026-09-23 00:00:00",
			},
		},
	}

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/sub-users", nil))
	rec := httptest.NewRecorder()
	HandleSubUserList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			AccessCode string `json:"access_code"`
			Password   string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 sub-user, got %d", len(resp.Data))
	}
	if resp.Data[0].Password != "" {
		t.Fatalf("list leaked plaintext password: %q", resp.Data[0].Password)
	}
	if resp.Data[0].AccessCode != "CODE12345!" {
		t.Fatalf("access_code should be retained for share links, got %q", resp.Data[0].AccessCode)
	}
	if strings.Contains(rec.Body.String(), "SuperSecretPlaintext") {
		t.Fatalf("plaintext password present anywhere in list response")
	}
}

// TestTOTPQRDataURL 保障设置两步验证时能生成可供 Google Authenticator 扫描的二维码数据 URL。
func TestTOTPQRDataURL(t *testing.T) {
	uri := totpSetupURI("JBSWY3DPEHPK3PXP", "admin")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("unexpected otpauth uri: %s", uri)
	}
	if !strings.Contains(uri, "issuer=EyvesCloud") || !strings.Contains(uri, "algorithm=SHA1") {
		t.Fatalf("otpauth uri missing Google Authenticator params: %s", uri)
	}
	qr := totpQRDataURL(uri)
	if !strings.HasPrefix(qr, "data:image/png;base64,") || len(qr) < len("data:image/png;base64,")+100 {
		t.Fatalf("qr data URL malformed (len=%d)", len(qr))
	}
	if got := totpQRDataURL(""); got != "" {
		t.Fatalf("empty uri should yield empty qr, got %q", got)
	}
}

// TestSanitizeMailHeaderBlocksSMTPInjection 锁定邮件头注入修复：容器名等用户
// 可控字段中的 CR/LF 必须被剥离，否则可注入额外邮件头（如 Bcc）篡改收件人。
func TestSanitizeMailHeaderBlocksSMTPInjection(t *testing.T) {
	evil := "web-01\r\nBcc: attacker@evil.com"
	got := sanitizeMailHeader(evil)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header value still contains CR/LF: %q", got)
	}
	if !strings.Contains(got, "Bcc:") {
		t.Fatal("test precondition: payload should be preserved as inert text")
	}
	// 折叠为单行后不得出现可被解析为新头的换行
	if got != "web-01 Bcc: attacker@evil.com" {
		t.Fatalf("unexpected sanitized value: %q", got)
	}
	if sanitizeMailHeader("normal") != "normal" {
		t.Fatal("normal value must be unchanged")
	}
}

// TestNeutralizeCSVFormula 锁定审计导出 CSV 公式注入修复。
func TestNeutralizeCSVFormula(t *testing.T) {
	for _, in := range []string{"=1+1", "+SUM(A1)", "-2+3", "@cmd", "\tx"} {
		got := neutralizeCSVFormula(in)
		if !strings.HasPrefix(got, "'") {
			t.Fatalf("field %q must be neutralized, got %q", in, got)
		}
	}
	if neutralizeCSVFormula("") != "" {
		t.Fatal("empty field must stay empty")
	}
	if neutralizeCSVFormula("2026-09-24 10:00:00") != "2026-09-24 10:00:00" {
		t.Fatal("normal value must be unchanged")
	}
}

// TestValidateWebhookURLBlocksSSRFTargets 锁定 webhook SSRF 加固：链路本地
// （云元数据 169.254.169.254）、未指定与组播地址必须被拒；正常公网/内网地址放行。
func TestValidateWebhookURLBlocksSSRFTargets(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/hook",
		"http://0.0.0.0/hook",
		"http://224.0.0.1/hook",
		"ftp://example.com/hook",
		"http://user:pass@example.com/hook",
	}
	for _, u := range blocked {
		if err := validateWebhookURL(u); err == nil {
			t.Fatalf("webhook URL %q must be rejected", u)
		}
	}
	allowed := []string{
		"https://hooks.example.com/notify",
		"http://10.0.0.5:9000/hook", // 内网自托管允许
		"http://127.0.0.1:8080/hook", // 本机接收端允许
		"",
	}
	for _, u := range allowed {
		if err := validateWebhookURL(u); err != nil {
			t.Fatalf("webhook URL %q must be allowed, got %v", u, err)
		}
	}
}