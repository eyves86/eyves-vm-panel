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

func loginAs(t *testing.T, username, password string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	HandleLogin(rec, req)
	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp.Data.Token
}

// TestExtraAdminLoginAndTokenValidation 锁定多管理员登录链路：
// 正确凭据签发带 admin_id/role 的令牌、按其自身 TokenVersion 校验；
// 错误口令、禁用账号、未知用户统一 401（防用户名枚举）；轮换版本后旧令牌失效。
func TestExtraAdminLoginAndTokenValidation(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	hash, err := bcrypt.GenerateFromPassword([]byte("Sup3rSecretPass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}

	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:     "root-admin",
		AdminPassHash: "unused-in-this-test",
		JWTSecret:     "test-secret",
		Admins: []config.AdminAccount{
			{ID: "a1", Username: "alice", PassHash: string(hash), Role: config.AdminRoleOperator},
			{ID: "a2", Username: "bob", PassHash: string(hash), Role: config.AdminRoleReadonly, Disabled: true},
		},
	}

	rec, token := loginAs(t, "alice", "Sup3rSecretPass")
	if rec.Code != http.StatusOK || token == "" {
		t.Fatalf("alice login failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	claims, ok := claimsFromToken(token)
	if !ok {
		t.Fatal("issued token must validate")
	}
	if claims["admin_id"] != "a1" {
		t.Fatalf("token admin_id = %v, want a1", claims["admin_id"])
	}
	if claims["role"] != config.AdminRoleOperator {
		t.Fatalf("token role = %v, want operator", claims["role"])
	}

	if rec, _ := loginAs(t, "alice", "wrong-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password must 401, got %d", rec.Code)
	}
	if rec, _ := loginAs(t, "bob", "Sup3rSecretPass"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled admin must 401, got %d", rec.Code)
	}
	if rec, _ := loginAs(t, "nobody", "whatever"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user must 401 (anti-enumeration), got %d", rec.Code)
	}

	// 轮换该账号的 TokenVersion → 已签发令牌立即失效（改口令即吊销）
	// 测试环境没有 sqlite，直接改内存配置（生产走 MutateGlobal 持久化）。
	config.AppConfigMu.Lock()
	for i := range config.AppConfig.Admins {
		if config.AppConfig.Admins[i].ID == "a1" {
			config.AppConfig.Admins[i].TokenVersion++
		}
	}
	config.AppConfigMu.Unlock()
	if _, ok := claimsFromToken(token); ok {
		t.Fatal("token must be invalid after token version rotation")
	}

	// 账号被删除 → 令牌失效
	config.AppConfig = &config.EyvescloudConfig{AdminUser: "root-admin", JWTSecret: "test-secret"}
	if _, ok := claimsFromToken(token); ok {
		t.Fatal("token must be invalid after admin account removal")
	}
}
