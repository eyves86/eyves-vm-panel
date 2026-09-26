package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestV1ItemRoutingStripsVersionPrefix 回归测试：同一批 handler 同时注册在
// /api/... 与 /api/v1/... 两个命名空间下，条目路由必须能正确剥离两种前缀。
// 若只剥离非版本化前缀，v1 路径会解析出空 ID 并返回 400；修复后应正常进入
// 资源查找流程并返回 404（资源不存在）。
func TestV1ItemRoutingStripsVersionPrefix(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{}

	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"ssh-key v1", "/api/v1/ssh-keys/missing-key", HandleSSHKeyItem},
		{"ssh-key legacy", "/api/ssh-keys/missing-key", HandleSSHKeyItem},
		{"recipe v1", "/api/v1/recipes/missing-recipe", HandleRecipeItem},
		{"recipe legacy", "/api/recipes/missing-recipe", HandleRecipeItem},
		{"secgroup v1", "/api/v1/security-groups/missing-group", HandleSecGroupItem},
		{"secgroup legacy", "/api/security-groups/missing-group", HandleSecGroupItem},
		{"secgroup rules v1", "/api/v1/security-groups/missing-group/rules", HandleSecGroupRules},
		{"secgroup rules legacy", "/api/security-groups/missing-group/rules", HandleSecGroupRules},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := asAdminRequest(httptest.NewRequest(http.MethodGet, tc.path, nil))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code == http.StatusBadRequest {
				t.Fatalf("%s: 前缀剥离失败，ID 被解析为空并返回 400; body=%s", tc.path, rec.Body.String())
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status = %d, want 404; body=%s", tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

func asSubUserRequest(r *http.Request, role string) *http.Request {
	return withAuthContext(r, AuthContext{Type: authTypeSubUser, Username: "u1", Actor: "user:u1", Role: role})
}

func asAPIKeyRequest(r *http.Request, scopes ...string) *http.Request {
	return withAuthContext(r, AuthContext{Type: authTypeAPIKey, ApiKeyID: "k1", ApiKeyName: "test-key", Actor: "apikey:k1", Scopes: scopes})
}

// TestSecGroupEndpointsRequireScope 回归测试 A1：安全组是全局主机防火墙配置，
// 只读子用户与不含 secgroup:* scope 的 API Key 都必须被拒绝，不能仅凭登录态放行。
func TestSecGroupEndpointsRequireScope(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{}

	denied := []struct {
		name   string
		method string
		path   string
		req    *http.Request
	}{
		{"operator list", http.MethodGet, "/api/v1/security-groups", asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups", nil), "operator")},
		{"operator create", http.MethodPost, "/api/v1/security-groups", asSubUserRequest(httptest.NewRequest(http.MethodPost, "/api/v1/security-groups", nil), "operator")},
		{"operator item", http.MethodGet, "/api/v1/security-groups/g1", asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups/g1", nil), "operator")},
		{"viewer list", http.MethodGet, "/api/v1/security-groups", asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups", nil), "viewer")},
		{"operator rules", http.MethodGet, "/api/security-groups/g1/rules", asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/security-groups/g1/rules", nil), "operator")},
		{"apikey unrelated scope", http.MethodGet, "/api/v1/security-groups", asAPIKeyRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups", nil), "container:read")},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var handler http.HandlerFunc
			if strings.Contains(tc.path, "/rules") {
				handler = HandleSecGroupRules
			} else if tc.path == "/api/v1/security-groups" {
				handler = HandleSecGroups
			} else {
				handler = HandleSecGroupItem
			}
			handler(rec, tc.req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 403; body=%s", tc.path, rec.Code, rec.Body.String())
			}
		})
	}

	allowed := []struct {
		name string
		req  *http.Request
	}{
		{"admin", asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups", nil))},
		{"apikey secgroup:read", asAPIKeyRequest(httptest.NewRequest(http.MethodGet, "/api/v1/security-groups", nil), "secgroup:read")},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			HandleSecGroups(rec, tc.req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRecipeAndSSHKeyScope 回归测试 A2：recipe/ssh-key 的读需 container:read、
// 写需 container:power；子用户写权限受角色约束（viewer 只读），API Key 需持有对应 scope。
func TestRecipeAndSSHKeyScope(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{}

	cases := []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
		want    int
	}{
		{"recipe list operator", HandleRecipes, asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/recipes", nil), "operator"), http.StatusOK},
		{"recipe list viewer", HandleRecipes, asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/recipes", nil), "viewer"), http.StatusOK},
		{"recipe list apikey without container:read", HandleRecipes, asAPIKeyRequest(httptest.NewRequest(http.MethodGet, "/api/v1/recipes", nil), "dashboard:read"), http.StatusForbidden},
		{"recipe create viewer", HandleRecipes, asSubUserRequest(httptest.NewRequest(http.MethodPost, "/api/v1/recipes", nil), "viewer"), http.StatusForbidden},
		{"recipe create operator", HandleRecipes, asSubUserRequest(httptest.NewRequest(http.MethodPost, "/api/v1/recipes", nil), "operator"), http.StatusBadRequest},
		{"ssh-key list operator", HandleSSHKeys, asSubUserRequest(httptest.NewRequest(http.MethodGet, "/api/v1/ssh-keys", nil), "operator"), http.StatusOK},
		{"ssh-key create viewer", HandleSSHKeys, asSubUserRequest(httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", nil), "viewer"), http.StatusForbidden},
		{"ssh-key create operator", HandleSSHKeys, asSubUserRequest(httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", nil), "operator"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, tc.req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
