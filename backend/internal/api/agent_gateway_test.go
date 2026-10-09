package api

// agent_gateway_test.go —— 锁定独立 Agent 网关的**暴露面**：
// 只有节点心跳能入站，管理员/面板接口在网关上必须不可达（防成为第二入口）。

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// setupAgentGatewayTest 初始化临时配置库并注册一个带 token 的节点。
func setupAgentGatewayTest(t *testing.T) {
	t.Helper()
	requirePGTest(t)
	resetNodeReportedForTest()
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.AppConfigMu.Lock()
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:       "root-admin",
		AdminPassHash:   "x",
		JWTSecret:       "s",
		NextContainerID: 1,
		Nodes:           []config.Node{{ID: "node-gw", Name: "gw", Token: "gw-token"}},
	}
	config.AppConfigMu.Unlock()
	if err := config.SaveConfig(); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestAgentGatewayOnlyExposesHeartbeat(t *testing.T) {
	setupAgentGatewayTest(t)
	mux := AgentGatewayMux()

	// 心跳（正确 token）→ 200
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-gw/heartbeat", strings.NewReader(`{"version":"2.2.59"}`))
	req.Header.Set("Authorization", "Bearer gw-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("心跳应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}

	// 心跳（错误 token）→ 401：鉴权在网关侧仍生效
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-gw/heartbeat", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", rec.Code)
	}

	// 管理员/面板接口在网关上必须不可达（404），避免网关成为第二入口
	for _, path := range []string{
		"/api/nodes/node-gw/containers",
		"/api/nodes/node-gw/install-script",
		"/api/v1/dashboard",
		"/api/agent/containers",
		"/api/v2/instances/1/metrics",
		"/",
	} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("网关不应暴露 %s，实际状态 %d", path, rec.Code)
		}
	}

	// 存活探针
	req = httptest.NewRequest(http.MethodGet, "/api/agent/gateway/health", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("健康探针应 200，实际 %d", rec.Code)
	}
}
