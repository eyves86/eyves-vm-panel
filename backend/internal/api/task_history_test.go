package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// decodeHistoryAPIResponse 解析统一响应结构，解析失败时终止测试。
func decodeHistoryAPIResponse(t *testing.T, rec *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// TestHandleTaskHistoryAdminLists 验证管理员可获取历史分页结构（含 total/items）。
func TestHandleTaskHistoryAdminLists(t *testing.T) {
	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/history", nil))
	rec := httptest.NewRecorder()

	HandleTaskHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp := decodeHistoryAPIResponse(t, rec)
	if !resp.Success {
		t.Fatalf("success = false; body=%s", rec.Body.String())
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data 类型异常: %T", resp.Data)
	}
	if _, ok := data["total"]; !ok {
		t.Fatalf("缺少 total 字段: %#v", data)
	}
	if _, ok := data["items"]; !ok {
		t.Fatalf("缺少 items 字段: %#v", data)
	}
}

// TestHandleTaskHistoryRejectsWithoutScope 验证无 task:read scope 的 API Key 被拒。
func TestHandleTaskHistoryRejectsWithoutScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/history", nil)
	req = withAuthContext(req, AuthContext{
		Type:       authTypeAPIKey,
		ApiKeyID:   "key-1",
		ApiKeyName: "limited",
		Scopes:     []string{"container:read"},
	})
	rec := httptest.NewRecorder()

	HandleTaskHistory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestHandleTaskDetailNotFound 验证不存在的任务返回 404。
func TestHandleTaskDetailNotFound(t *testing.T) {
	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-999", nil))
	rec := httptest.NewRecorder()

	HandleTaskDetail(rec, req, "task-999")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestHandleTaskStatsAuth 验证统计接口：子用户 403，管理员 200。
func TestHandleTaskStatsAuth(t *testing.T) {
	subReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/stats", nil)
	subReq = withAuthContext(subReq, AuthContext{
		Type:     authTypeSubUser,
		Username: "user-1",
		Actor:    "user:user-1",
		Role:     "operator",
	})
	subRec := httptest.NewRecorder()
	HandleTaskStats(subRec, subReq)
	if subRec.Code != http.StatusForbidden {
		t.Fatalf("子用户: status = %d, want %d; body=%s", subRec.Code, http.StatusForbidden, subRec.Body.String())
	}

	adminRec := httptest.NewRecorder()
	HandleTaskStats(adminRec, asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/stats", nil)))
	if adminRec.Code != http.StatusOK {
		t.Fatalf("管理员: status = %d, want %d; body=%s", adminRec.Code, http.StatusOK, adminRec.Body.String())
	}
}

// TestHandleTaskSubRoutesStripsBothPrefixes 验证两种前缀都能剥离并解析出 id（得到 404 而非 400/405）。
func TestHandleTaskSubRoutesStripsBothPrefixes(t *testing.T) {
	for _, path := range []string{"/api/v1/tasks/task-999", "/api/tasks/task-999"} {
		req := asAdminRequest(httptest.NewRequest(http.MethodGet, path, nil))
		rec := httptest.NewRecorder()

		HandleTaskSubRoutes(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %s: status = %d, want %d; body=%s", path, rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}
}

// TestHandleTaskSubRoutesCancelDispatch 验证 POST {id}/cancel 子路由分发到取消逻辑（不存在则为 404）。
func TestHandleTaskSubRoutesCancelDispatch(t *testing.T) {
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-999/cancel", nil))
	rec := httptest.NewRecorder()

	HandleTaskSubRoutes(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestHandleTaskSubRoutesUnknownMethod 验证不支持的子路由动作为 405。
func TestHandleTaskSubRoutesUnknownMethod(t *testing.T) {
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/v1/tasks/task-1", nil))
	rec := httptest.NewRecorder()

	HandleTaskSubRoutes(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

// TestSubUserMiddlewareTaskRoutes 回归测试：子用户可读任务历史/详情（handler 内按
// actor 与容器范围二次校验），但取消/删除等写操作仍被中间件拦截，避免越权操作任务。
func TestSubUserMiddlewareTaskRoutes(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		SubUsers:  []config.SubUser{{ID: "s1", Username: "u1"}},
	}

	token := newSubUserTokenWithRole("u1", nil, "operator", time.Now().Add(time.Hour), 0)

	cases := []struct {
		name     string
		method   string
		path     string
		wantNext bool
	}{
		{"history list", http.MethodGet, "/api/tasks/history", true},
		{"history list v1", http.MethodGet, "/api/v1/tasks/history", true},
		{"detail", http.MethodGet, "/api/tasks/task-1", true},
		{"detail v1", http.MethodGet, "/api/v1/tasks/task-1", true},
		{"cancel blocked", http.MethodPost, "/api/tasks/task-1/cancel", false},
		{"delete blocked", http.MethodDelete, "/api/tasks/task-1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled := false
			next := func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()

			SubUserMiddleware(next)(rec, req)

			if nextCalled != tc.wantNext {
				t.Fatalf("next called = %v, want %v; status=%d body=%s",
					nextCalled, tc.wantNext, rec.Code, rec.Body.String())
			}
		})
	}
}
