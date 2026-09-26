package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"eyvescloud/internal/config"
)

// TestInstallScriptAdminScopeFallback 回归验证：install-script 端点从路由直接调用
// （无 AdminMiddleware，见 55b5431）后，管理员 Bearer token 的 scope 回退路径
// 仍然可用。requireScope 依赖 AuthMiddleware 注入的认证上下文；若上下文缺失，
// 管理员从面板/API 直接下载安装脚本会得到 403。
func TestInstallScriptAdminScopeFallback(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:     "admin",
		JWTSecret:     "test-secret",
		AdminPath:     "/admin-x",
		Nodes:         []config.Node{{ID: "node-1", Name: "node-1"}},
	}
	config.AppConfig.AdminTokenVersion = 1

	token, err := signAdminToken("admin", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}

	// 管理员带 Bearer token、无 X-Install-Key → 应 200（node:write scope 对 admin 恒真）
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/install-script", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	HandleNodeSubRoutes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin token download status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// 无任何凭据 → 仍应被拒绝（401/403）
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/install-script", nil)
	HandleNodeSubRoutes(w2, r2)
	if w2.Code == http.StatusOK {
		t.Fatalf("anonymous download status = %d, want 401/403", w2.Code)
	}

	// sha256 端点同一路径
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/install-script/sha256", nil)
	r3.Header.Set("Authorization", "Bearer "+token)
	HandleNodeSubRoutes(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("admin token sha256 status = %d, want 200; body: %s", w3.Code, w3.Body.String())
	}
}
