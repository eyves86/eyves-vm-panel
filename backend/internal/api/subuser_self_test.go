package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"eyvescloud/internal/config"
)

// subUserSelfRequest 构造一个带子用户会话上下文的请求（等价于 AuthMiddleware
// 解析 Bearer token 后注入的 AuthContext）。
func subUserSelfRequest(method, path string, body []byte) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	}
	return withAuthContext(r, AuthContext{Type: authTypeSubUser, Username: "u1", Actor: "u1", Role: "operator"})
}

// TestSubUserProfileRequiresSubUserSession 管理员/匿名会话不得访问子用户自助信息。
func TestSubUserProfileRequiresSubUserSession(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{JWTSecret: "s", AdminUser: "admin"}

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/sub-user/profile", nil), // 匿名
		asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/sub-user/profile", nil)),
	} {
		rec := httptest.NewRecorder()
		HandleSubUserProfile(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	}
}

// TestSubUserProfileReturnsShareInfo 子用户可读到自己的访问码与生效容器数（分享链接素材）。
func TestSubUserProfileReturnsShareInfo(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "s",
		SubUsers: []config.SubUser{{
			ID: "s1", Username: "u1", AccessCode: "code-abc", Role: "operator",
			ContainerUUIDs: []string{"uuid-1", "uuid-gone"},
		}},
		Containers: []config.Container{{Name: "web", UUID: "uuid-1"}},
	}

	rec := httptest.NewRecorder()
	HandleSubUserProfile(rec, subUserSelfRequest(http.MethodGet, "/api/sub-user/profile", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data["access_code"] != "code-abc" {
		t.Fatalf("access_code = %v, want code-abc", resp.Data["access_code"])
	}
	// uuid-gone 已不存在：生效容器数应为 1
	if count, _ := resp.Data["container_count"].(float64); count != 1 {
		t.Fatalf("container_count = %v, want 1", resp.Data["container_count"])
	}
}

// TestSubUserSelfRotatePassword 端到端：校验旧密码 → 服务端随机 16 位新密码一次性
// 返回 → 哈希更新、明文不落库、TokenVersion 递增（旧 token 失效）；
// 旧密码校验失败计入登录限流（防在线爆破），5 次后 429。
func TestSubUserSelfRotatePassword(t *testing.T) {
	previous := config.AppConfig
	rateKey := "192.0.2.1|user:u1" // httptest RemoteAddr 192.0.2.1 + 子用户名
	loginLimiter.reset(rateKey)    // 隔离其它测试留下的失败计数
	t.Cleanup(func() {
		config.AppConfig = previous
		loginLimiter.reset(rateKey)
	})
	hash, err := bcrypt.GenerateFromPassword([]byte("oldpass123456"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "s",
		SubUsers: []config.SubUser{{
			ID: "s1", Username: "u1", PassHash: string(hash), AccessCode: "code-abc",
			ContainerUUIDs: []string{"uuid-1"},
		}},
	}

	// 旧密码错误 → 401（计入限流）
	badBody, _ := json.Marshal(map[string]string{"old_password": "wrongpass"})
	rec := httptest.NewRecorder()
	HandleSubUserSelfRotatePassword(rec, subUserSelfRequest(http.MethodPost, "/api/sub-user/rotate-password", badBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong old password: status = %d, want 401", rec.Code)
	}

	// 连续失败到阈值（共 5 次）后 → 429
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		HandleSubUserSelfRotatePassword(rec, subUserSelfRequest(http.MethodPost, "/api/sub-user/rotate-password", badBody))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+2, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	HandleSubUserSelfRotatePassword(rec, subUserSelfRequest(http.MethodPost, "/api/sub-user/rotate-password", badBody))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures: status = %d, want 429", rec.Code)
	}
	loginLimiter.reset(rateKey)

	// 旧密码正确 → 返回随机新密码
	goodBody, _ := json.Marshal(map[string]string{"old_password": "oldpass123456"})
	rec = httptest.NewRecorder()
	HandleSubUserSelfRotatePassword(rec, subUserSelfRequest(http.MethodPost, "/api/sub-user/rotate-password", goodBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	newPassword := resp.Data.Password
	if len(newPassword) != 16 {
		t.Fatalf("generated password length = %d, want 16 (%q)", len(newPassword), newPassword)
	}

	// 状态断言：新哈希可验、明文不落库、TokenVersion 递增
	su := config.AppConfig.SubUsers[0]
	if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(newPassword)); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte("oldpass123456")); err == nil {
		t.Fatal("old password still verifies after rotation")
	}
	if su.Password != "" {
		t.Fatalf("plaintext must not be stored, got %q", su.Password)
	}
	if su.TokenVersion != 1 {
		t.Fatalf("TokenVersion = %d, want 1", su.TokenVersion)
	}

	// 旧 token_version 的 token 访问 access-code 登录端点时被拒绝（撤销生效）：
	// 直接走 AuthMiddleware 验证 token_version 检查。
	staleToken := newSubUserTokenWithRole("u1", []string{"uuid-1"}, "operator", time.Now().Add(time.Hour), 0)
	req := httptest.NewRequest(http.MethodGet, "/api/containers", nil)
	req.Header.Set("Authorization", "Bearer "+staleToken)
	rec = httptest.NewRecorder()
	called := false
	AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if called {
		t.Fatal("stale sub-user token (old token_version) must be rejected after rotation")
	}
}

// TestSubUserAccessCodeLoginStillWorksAfterRotation 访问码登录使用轮换后的新密码。
func TestSubUserAccessCodeLoginStillWorksAfterRotation(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	hash, _ := bcrypt.GenerateFromPassword([]byte("newpass-xyz-123"), bcrypt.DefaultCost)
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "s",
		SubUsers: []config.SubUser{{
			ID: "s1", Username: "u1", PassHash: string(hash), AccessCode: "code-abc",
			Role: "operator", ContainerUUIDs: []string{"uuid-1"},
		}},
		Containers: []config.Container{{Name: "web", UUID: "uuid-1"}},
	}

	body, _ := json.Marshal(map[string]string{"code": "code-abc", "password": "newpass-xyz-123"})
	rec := httptest.NewRecorder()
	HandleSubUserAccessCode(rec, httptest.NewRequest(http.MethodPost, "/api/sub-user/access", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("access-code login after self rotation: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"container_uuids":["uuid-1"]`) {
		t.Fatalf("access-code login should carry bound container uuids, body=%s", rec.Body.String())
	}
}
