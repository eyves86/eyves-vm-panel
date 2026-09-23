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