package api

// apiv2_contract_test.go —— API v2 契约测试（P2）。
//
// 目标：把"外部集成方依赖的约定"固化成测试，避免后续改动无意破坏契约：
//   1. 统一信封：成功 {success:true, code:"OK", data, request_id}；
//      失败 {success:false, code:<稳定错误码>, message, details?}
//   2. 认证：无凭据 401 UNAUTHENTICATED；有效管理员令牌可访问
//   3. 分页：page/page_size/all 语义与 data.pagination 结构
//   4. 状态枚举：内部状态 → v2 契约状态（running/stopped/creating/suspended/error/unknown）
//   5. 路由与权限：管理员端点对匿名/子用户拒绝，未知资源 404，非法参数 400

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// newV2TestMux 构造仅注册 v2 路由的测试用 mux。
func newV2TestMux() *http.ServeMux {
	mux := http.NewServeMux()
	RegisterAPIV2(mux)
	return mux
}

// withV2TestConfig 准备一个最小可用的全局配置（管理员 + JWT 密钥 + 空容器集）。
func withV2TestConfig(t *testing.T) string {
	t.Helper()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:     "root-admin",
		AdminPassHash: "unused",
		JWTSecret:     "v2-contract-secret",
		Containers: []config.Container{
			{ID: 7, UUID: "uuid-alpha", Name: "alpha", Status: "running", VCPU: 2, RAMMB: 1024, DiskGB: 20, Template: "debian-bookworm"},
			{ID: 8, UUID: "uuid-beta", Name: "beta", Status: "stopped", Suspended: true, VCPU: 1, RAMMB: 512, DiskGB: 10, Template: "alpine-3.21"},
		},
	}
	token, err := signAdminToken("root-admin", "", "", 0)
	if err != nil {
		t.Fatalf("signAdminToken: %v", err)
	}
	return token
}

// decodeEnvelope 解析 v2 信封并做基础结构断言。
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var envelope map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, rec.Body.String())
	}
	if _, ok := envelope["success"]; !ok {
		t.Fatalf("响应缺少 success 字段：%s", rec.Body.String())
	}
	if _, ok := envelope["code"]; !ok {
		t.Fatalf("响应缺少 code 字段：%s", rec.Body.String())
	}
	if _, ok := envelope["request_id"]; !ok {
		t.Fatalf("响应缺少 request_id 字段：%s", rec.Body.String())
	}
	return envelope
}

func TestV2EnvelopeAndAuthContract(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()

	t.Run("无凭据访问返回 401 UNAUTHENTICATED", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/instances", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
		envelope := decodeEnvelope(t, rec)
		if envelope["success"] != false || envelope["code"] != v2CodeUnauthenticated {
			t.Fatalf("信封不符：%v", envelope)
		}
	})

	t.Run("有效管理员令牌返回 200 且信封完整", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/instances", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200（body=%s）", rec.Code, rec.Body.String())
		}
		envelope := decodeEnvelope(t, rec)
		if envelope["success"] != true || envelope["code"] != v2CodeOK {
			t.Fatalf("信封不符：%v", envelope)
		}
		data, _ := envelope["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("data 不是对象：%v", envelope["data"])
		}
		if _, ok := data["items"]; !ok {
			t.Fatalf("列表响应缺少 data.items：%v", data)
		}
		pagination, _ := data["pagination"].(map[string]interface{})
		if pagination == nil {
			t.Fatalf("列表响应缺少 data.pagination：%v", data)
		}
		for _, key := range []string{"page", "page_size", "total", "pages"} {
			if _, ok := pagination[key]; !ok {
				t.Fatalf("pagination 缺少字段 %s：%v", key, pagination)
			}
		}
	})

	t.Run("未知实例返回 404 NOT_FOUND", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/instances/does-not-exist", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", rec.Code)
		}
		envelope := decodeEnvelope(t, rec)
		if envelope["code"] != v2CodeNotFound {
			t.Fatalf("错误码 = %v，期望 NOT_FOUND", envelope["code"])
		}
	})

	t.Run("非法参数返回 400 INVALID_ARGUMENT 且带字段级 details", func(t *testing.T) {
		// 先补齐必填字段，确保校验能走到 runtime 取值检查（否则会先报 template_id 必填）。
		body := strings.NewReader(`{"name":"x","template_id":"debian-bookworm","runtime":"hyperv"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/instances", body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400（body=%s）", rec.Code, rec.Body.String())
		}
		envelope := decodeEnvelope(t, rec)
		if envelope["code"] != v2CodeInvalidArgument {
			t.Fatalf("错误码 = %v，期望 INVALID_ARGUMENT", envelope["code"])
		}
		details, _ := envelope["details"].(map[string]interface{})
		if details == nil || details["runtime"] == nil {
			t.Fatalf("缺少字段级 details.runtime：%v", envelope)
		}
	})

	t.Run("系统信息端点可用且形态稳定", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/info", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
		envelope := decodeEnvelope(t, rec)
		data, _ := envelope["data"].(map[string]interface{})
		if data["api_version"] != "v2" {
			t.Fatalf("api_version = %v，期望 v2", data["api_version"])
		}
		if data["product"] != "EyvesCloud" {
			t.Fatalf("product = %v", data["product"])
		}
	})
}

func TestV2PaginationContract(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		total    int
		wantFrom int
		wantTo   int
	}{
		{"默认第一页", "", 50, 0, 20},
		{"第二页", "?page=2&page_size=10", 50, 10, 20},
		{"超出范围返回空区间", "?page=99&page_size=10", 50, 50, 50},
		{"all 返回全量", "?all=true", 50, 0, 50},
		{"page_size 上限 200", "?page_size=5000", 400, 0, 200},
		{"非法 page 回落 1", "?page=0&page_size=5", 50, 0, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/instances"+tc.query, nil)
			query := v2ParsePage(req)
			from, to := query.Slice(tc.total)
			if from != tc.wantFrom || to != tc.wantTo {
				t.Fatalf("Slice(%d) = (%d,%d)，期望 (%d,%d)", tc.total, from, to, tc.wantFrom, tc.wantTo)
			}
			if query.All && tc.wantTo-from != tc.total {
				t.Fatalf("all=true 应返回全量，得到 %d 条", to-from)
			}
		})
	}
}

func TestV2StatusVocabularyContract(t *testing.T) {
	cases := []struct {
		name string
		in   config.Container
		want string
	}{
		{"running", config.Container{Status: "running"}, "running"},
		{"stopped", config.Container{Status: "stopped"}, "stopped"},
		{"creating", config.Container{Status: "queued"}, "creating"},
		{"pending 视为 creating", config.Container{Status: "pending"}, "creating"},
		{"suspended 标记优先", config.Container{Status: "stopped", Suspended: true}, "suspended"},
		{"orphaned 视为 error", config.Container{Status: "orphaned"}, "error"},
		{"未知状态 → unknown", config.Container{Status: "???"}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v2InstanceStatus(tc.in); got != tc.want {
				t.Fatalf("v2InstanceStatus(%q) = %q，期望 %q", tc.in.Status, got, tc.want)
			}
		})
	}
}

// TestV2AdminOnlyEndpointsRejectAnonymous 锁定"管理类端点必须认证"这一契约。
func TestV2AdminOnlyEndpointsRejectAnonymous(t *testing.T) {
	withV2TestConfig(t)
	mux := newV2TestMux()
	for _, path := range []string{
		"/api/v2/nodes",
		"/api/v2/audit-logs",
		"/api/v2/users",
		"/api/v2/admins",
		"/api/v2/api-keys",
		"/api/v2/webhooks",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 状态码 = %d，期望 401", path, rec.Code)
		}
		envelope := decodeEnvelope(t, rec)
		if envelope["code"] != v2CodeUnauthenticated {
			t.Fatalf("%s 错误码 = %v，期望 UNAUTHENTICATED", path, envelope["code"])
		}
	}
}
