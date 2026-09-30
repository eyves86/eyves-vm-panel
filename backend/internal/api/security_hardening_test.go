package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"

	"golang.org/x/crypto/bcrypt"
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

// subUserCreateTestEnv 构造一个带已绑定容器的子用户测试环境。
func subUserCreateTestEnv(t *testing.T, passHash string) {
	t.Helper()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		DataDir: t.TempDir(),
		Containers: []config.Container{
			{ID: 1, Name: "web-01", UUID: "uuid-web01", OwnerSubUserID: "sub-alice"},
		},
		SubUsers: []config.SubUser{
			{
				ID:             "sub-alice",
				Username:       "alice",
				Email:          "alice@example.com",
				PassHash:       passHash,
				Password:       "", // 用户已自助改密：落库明文已清空
				Role:           "operator",
				ContainerNames: []string{"web-01"},
				ContainerUUIDs: []string{"uuid-web01"},
				TokenVersion:   3,
			},
		},
	}
}

// TestSubUserCreateMergeKeepsUserPassword 防回归：容器已绑定到某子用户、且该用户
// 已自助修改过密码（PassHash 有效、落库明文已清空）时，再次对同一容器调用
// create 必须走幂等合并——不得重新生成密码覆盖用户改过的密码，也不得踢下线。
func TestSubUserCreateMergeKeepsUserPassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("UserSetPass123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	subUserCreateTestEnv(t, string(hash))

	body := `{"container_name":"web-01"}`
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/sub-user/create", strings.NewReader(body)))
	rec := httptest.NewRecorder()
	HandleSubUserCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Username != "alice" {
		t.Fatalf("merge should return the existing sub-user, got %q", resp.Data.Username)
	}
	if resp.Data.Password != "" {
		t.Fatalf("merge must not return a regenerated password, got %q", resp.Data.Password)
	}
	if !strings.Contains(rec.Body.String(), "password unchanged") && !strings.Contains(rec.Body.String(), "merged") {
		t.Fatalf("unexpected message: %s", rec.Body.String())
	}

	su := config.AppConfig.SubUsers[0]
	if su.PassHash != string(hash) {
		t.Fatal("merge must not overwrite PassHash of a user who changed their own password")
	}
	if su.TokenVersion != 3 {
		t.Fatalf("merge must not bump TokenVersion (would kick the user out), got %d", su.TokenVersion)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte("UserSetPass123")); err != nil {
		t.Fatal("the user's own password must still verify after merge")
	}
}

// TestSubUserCreateMergeRejectsUsernameMismatch 防回归：容器已属于 alice 时，
// 超管传不同 username 调 create 不得静默把 alice 改名，应 409 提示走 edit 端点。
func TestSubUserCreateMergeRejectsUsernameMismatch(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("SomeHash123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	subUserCreateTestEnv(t, string(hash))

	body := `{"container_name":"web-01","username":"bob"}`
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/sub-user/create", strings.NewReader(body)))
	rec := httptest.NewRecorder()
	HandleSubUserCreate(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if config.AppConfig.SubUsers[0].Username != "alice" {
		t.Fatalf("merge must not silently rename the existing sub-user, got %q", config.AppConfig.SubUsers[0].Username)
	}
}

// TestTOTPQRDataURL 保障设置两步验证时能生成可供 TOTP 验证器扫描的二维码数据 URL。
func TestTOTPQRDataURL(t *testing.T) {
	uri := totpSetupURI("JBSWY3DPEHPK3PXP", "admin")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("unexpected otpauth uri: %s", uri)
	}
	if !strings.Contains(uri, "issuer=EyvesCloud") || !strings.Contains(uri, "algorithm=SHA1") {
		t.Fatalf("otpauth uri missing standard TOTP params: %s", uri)
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