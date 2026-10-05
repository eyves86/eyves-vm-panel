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

// TestSubUserProfileReturnsShareInfo 子用户可读到「当前会话可管理机器」的访问码凭据
// （机器级分享素材），生效容器数按会话授权范围统计。
func TestSubUserProfileReturnsShareInfo(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "s",
		SubUsers: []config.SubUser{{
			ID: "s1", Username: "u1", Role: "operator",
			ContainerUUIDs: []string{"uuid-1", "uuid-gone"},
		}},
		Containers: []config.Container{{ID: 1, Name: "web", UUID: "uuid-1"}},
	}

	rec := httptest.NewRecorder()
	HandleSubUserProfile(rec, subUserSelfRequest(http.MethodGet, "/api/sub-user/profile", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ContainerCount int `json:"container_count"`
			AccessLinks    []struct {
				ContainerUUID      string `json:"container_uuid"`
				AccessCode         string `json:"access_code"`
				AccessCodePassword string `json:"access_code_password"`
			} `json:"access_links"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// uuid-gone 已不存在：生效容器数应为 1
	if resp.Data.ContainerCount != 1 {
		t.Fatalf("container_count = %d, want 1", resp.Data.ContainerCount)
	}
	if len(resp.Data.AccessLinks) != 1 || resp.Data.AccessLinks[0].ContainerUUID != "uuid-1" {
		t.Fatalf("access_links = %+v, want single link for uuid-1", resp.Data.AccessLinks)
	}
	if len(resp.Data.AccessLinks[0].AccessCode) != 8 {
		t.Fatalf("access_code = %q, want 8 chars (auto-generated)", resp.Data.AccessLinks[0].AccessCode)
	}
	if len(resp.Data.AccessLinks[0].AccessCodePassword) != 16 {
		t.Fatalf("access_code_password = %q, want 16 chars (auto-generated)", resp.Data.AccessLinks[0].AccessCodePassword)
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

// TestSubUserAccessCodeLoginScopesToOneContainer 访问码是「机器级」凭据：
// 用甲机器的访问码登录只授权甲机器；账号密码在访问码端点必须被拒绝。
func TestSubUserAccessCodeLoginScopesToOneContainer(t *testing.T) {
	previous := config.AppConfig
	rateKey := "192.0.2.1|subuser-accesscode"
	loginLimiter.reset(rateKey)
	t.Cleanup(func() {
		config.AppConfig = previous
		loginLimiter.reset(rateKey)
	})
	hash, _ := bcrypt.GenerateFromPassword([]byte("account-pass-123"), bcrypt.DefaultCost)
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "s",
		SubUsers: []config.SubUser{{
			ID: "s1", Username: "u1", PassHash: string(hash), Role: "operator",
			ContainerUUIDs: []string{"uuid-1", "uuid-2"},
		}},
		Containers: []config.Container{
			{ID: 1, Name: "web", UUID: "uuid-1", OwnerSubUserID: "s1",
				AccessCode: "code-abc", AccessCodePassword: "share-pass-456"},
			{ID: 2, Name: "db", UUID: "uuid-2", OwnerSubUserID: "s1",
				AccessCode: "code-xyz", AccessCodePassword: "share-pass-789"},
		},
	}

	// 甲机器访问码 + 甲机器口令 → 200，且只授权 uuid-1（不含同属主的 uuid-2）。
	body, _ := json.Marshal(map[string]string{"code": "code-abc", "password": "share-pass-456"})
	rec := httptest.NewRecorder()
	HandleSubUserAccessCode(rec, httptest.NewRequest(http.MethodPost, "/api/sub-user/access", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("access-code login: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"container_uuids":["uuid-1"]`) {
		t.Fatalf("access-code login must scope to the single machine, body=%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "uuid-2") {
		t.Fatalf("access-code login must NOT leak the owner's other machines, body=%s", rec.Body.String())
	}

	// 交叉口令（甲机器的码 + 乙机器的口令）→ 401。
	body, _ = json.Marshal(map[string]string{"code": "code-abc", "password": "share-pass-789"})
	rec = httptest.NewRecorder()
	HandleSubUserAccessCode(rec, httptest.NewRequest(http.MethodPost, "/api/sub-user/access", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-machine password must be rejected: status = %d, want 401", rec.Code)
	}
	loginLimiter.reset(rateKey)

	// 账号密码：绝不能通过访问码端点登录。
	body, _ = json.Marshal(map[string]string{"code": "code-abc", "password": "account-pass-123"})
	rec = httptest.NewRecorder()
	HandleSubUserAccessCode(rec, httptest.NewRequest(http.MethodPost, "/api/sub-user/access", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("access-code login must reject the account password: status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}
