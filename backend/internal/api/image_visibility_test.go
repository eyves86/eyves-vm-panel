package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"eyvescloud/internal/config"
)

// TestImageEnabledOnPanelDefaultAndHidden 验证主控镜像可见性策略：EnabledImages 为
// 空时全部启用；显式设置后仅列表内镜像可见，其余一律视为「已隐藏」。
func TestImageEnabledOnPanelDefaultAndHidden(t *testing.T) {
	agentAdminTestConfig(t)

	if !imageEnabledOnPanel("ubuntu-noble") {
		t.Fatal("空 EnabledImages 应默认启用全部镜像")
	}

	config.AppConfigMu.Lock()
	config.AppConfig.EnabledImages = []string{"debian-bookworm"}
	config.AppConfigMu.Unlock()

	if imageEnabledOnPanel("ubuntu-noble") {
		t.Fatal("EnabledImages 未包含 ubuntu-noble 时必须视为已隐藏")
	}
	if !imageEnabledOnPanel("debian-bookworm") {
		t.Fatal("列表内的 debian-bookworm 必须保持启用")
	}
	if imageEnabledOnPanel("no-such-image-at-all") {
		t.Fatal("未知镜像不得被视为启用")
	}
}

// TestEnforceMasterReinstallImageAllowedGate 验证重装门槛：隐藏镜像 → 403 且返回
// false；启用镜像 → 放行且请求体可被下游再次读取；非法/空请求体交给下游处理。
func TestEnforceMasterReinstallImageAllowedGate(t *testing.T) {
	agentAdminTestConfig(t)
	config.AppConfigMu.Lock()
	config.AppConfig.EnabledImages = []string{"debian-bookworm"}
	config.AppConfigMu.Unlock()

	// 隐藏镜像 → 403
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/containers/1/reinstall",
		bytes.NewReader([]byte(`{"template_id":"ubuntu-noble"}`)))
	if enforceMasterReinstallImageAllowed(rec, req) {
		t.Fatal("隐藏镜像必须被拒绝")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// 启用镜像 → 放行，且请求体被复位供下游消费
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/containers/1/reinstall",
		bytes.NewReader([]byte(`{"template_id":"debian-bookworm"}`)))
	if !enforceMasterReinstallImageAllowed(rec, req) {
		t.Fatalf("启用镜像必须放行; body=%s", rec.Body.String())
	}
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != `{"template_id":"debian-bookworm"}` {
		t.Fatalf("请求体必须复位供下游读取, got %q", restored)
	}

	// 非法 JSON → 放行（交由下游按原逻辑返回 400）
	rec = httptest.NewRecorder()
	if !enforceMasterReinstallImageAllowed(rec, httptest.NewRequest(http.MethodPost, "/api/containers/1/reinstall",
		bytes.NewReader([]byte(`not-json`)))) {
		t.Fatal("非法请求体应放行交由下游处理")
	}

	// 空 template_id → 放行
	rec = httptest.NewRecorder()
	if !enforceMasterReinstallImageAllowed(rec, httptest.NewRequest(http.MethodPost, "/api/containers/1/reinstall",
		bytes.NewReader([]byte(`{}`)))) {
		t.Fatal("空 template_id 应放行交由下游处理")
	}
}

// TestHandleAgentImageDeleteValidation 验证被控镜像删除端点的入参校验与未知镜像语义。
func TestHandleAgentImageDeleteValidation(t *testing.T) {
	agentAdminTestConfig(t)

	// 非 POST → 405
	rec := httptest.NewRecorder()
	HandleAgentImageDelete(rec, httptest.NewRequest(http.MethodGet, "/api/agent/images/delete", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}

	// 缺 template_id → 400
	rec = httptest.NewRecorder()
	HandleAgentImageDelete(rec, httptest.NewRequest(http.MethodPost, "/api/agent/images/delete",
		bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// 未知镜像 → 404
	rec = httptest.NewRecorder()
	HandleAgentImageDelete(rec, httptest.NewRequest(http.MethodPost, "/api/agent/images/delete",
		bytes.NewReader([]byte(`{"template_id":"does-not-exist-xyz"}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
