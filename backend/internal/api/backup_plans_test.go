package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// setupBackupPlanConfig 初始化最小配置（含一个运行中容器）用于备份计划测试。
func setupBackupPlanConfig(t *testing.T) {
	t.Helper()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		Containers: []config.Container{{ID: 7, Name: "web-1", Status: "running"}},
	}
}

func decodeBackupPlanData(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("success=false; body=%s", rec.Body.String())
	}
	return resp.Data
}

// TestBackupPlanCRUD 覆盖新建 → 列表 → 更新 → 删除全链路。
func TestBackupPlanCRUD(t *testing.T) {
	setupBackupPlanConfig(t)

	// 新建
	body := `{"name":"nightly","container_id":7,"cron":"0 3 * * *","keep":5}`
	rec := httptest.NewRecorder()
	HandleBackupPlans(rec, asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/backup-plans", strings.NewReader(body))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("新建: status=%d want 201; body=%s", rec.Code, rec.Body.String())
	}
	created := decodeBackupPlanData(t, rec)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("缺少 id: %#v", created)
	}
	if created["cron"] != "0 3 * * *" {
		t.Fatalf("cron = %v", created["cron"])
	}
	if created["keep"] != float64(5) {
		t.Fatalf("keep = %v, want 5", created["keep"])
	}
	if created["next_run_at"] == "" {
		t.Fatalf("next_run_at 未计算: %#v", created)
	}
	if created["enabled"] != true {
		t.Fatalf("默认应启用: %#v", created)
	}

	// 列表
	rec = httptest.NewRecorder()
	HandleBackupPlans(rec, asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/backup-plans", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("列表: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("解析列表失败: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("列表长度 = %d, want 1", len(listResp.Data))
	}

	// 更新（cron 改变应重算 next_run_at）
	updateBody := `{"name":"weekly","container_id":7,"cron":"30 4 * * 6","keep":9}`
	rec = httptest.NewRecorder()
	HandleBackupPlanSubRoutes(rec, asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/v1/backup-plans/"+id, strings.NewReader(updateBody))))
	if rec.Code != http.StatusOK {
		t.Fatalf("更新: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	updated := decodeBackupPlanData(t, rec)
	if updated["cron"] != "30 4 * * 6" || updated["keep"] != float64(9) {
		t.Fatalf("更新未生效: %#v", updated)
	}

	// 详情
	rec = httptest.NewRecorder()
	HandleBackupPlanSubRoutes(rec, asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/backup-plans/"+id, nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("详情: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	// 删除
	rec = httptest.NewRecorder()
	HandleBackupPlanSubRoutes(rec, asAdminRequest(httptest.NewRequest(http.MethodDelete, "/api/backup-plans/"+id, nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("删除: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	if len(config.BackupPlans()) != 0 {
		t.Fatalf("删除后仍有 %d 个计划", len(config.BackupPlans()))
	}
}

// TestBackupPlanRejectsInvalidInput 验证非法 cron / 不存在的容器被 400 拒绝。
func TestBackupPlanRejectsInvalidInput(t *testing.T) {
	setupBackupPlanConfig(t)

	cases := []struct {
		name string
		body string
	}{
		{"非法 cron", `{"cron":"not a cron"}`},
		{"cron 字段不足", `{"cron":"0 3 *"}`},
		{"容器不存在", `{"cron":"0 3 * * *","container_id":999}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			HandleBackupPlans(rec, asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/backup-plans", strings.NewReader(tc.body))))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestBackupPlanUpdatePreservesDisabled 验证更新时缺省 enabled 不会把已停用计划重新启用。
func TestBackupPlanUpdatePreservesDisabled(t *testing.T) {
	setupBackupPlanConfig(t)

	rec := httptest.NewRecorder()
	HandleBackupPlans(rec, asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/backup-plans",
		strings.NewReader(`{"cron":"0 3 * * *","enabled":false}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("新建: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	id, _ := decodeBackupPlanData(t, rec)["id"].(string)

	rec = httptest.NewRecorder()
	HandleBackupPlanSubRoutes(rec, asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/backup-plans/"+id,
		strings.NewReader(`{"cron":"0 5 * * *","keep":3}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("更新: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBackupPlanData(t, rec)["enabled"] != false {
		t.Fatalf("更新后不应被重新启用: %s", rec.Body.String())
	}
}

// TestBackupPlanRequiresWriteScope 验证只读 API Key 不能写。
func TestBackupPlanRequiresWriteScope(t *testing.T) {
	setupBackupPlanConfig(t)

	rec := httptest.NewRecorder()
	req := asAPIKeyRequest(
		httptest.NewRequest(http.MethodPost, "/api/backup-plans", strings.NewReader(`{"cron":"0 3 * * *"}`)),
		"backup:read",
	)
	HandleBackupPlans(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestBackupPlanSubRoutesUnknownID 验证未知计划返回 404。
func TestBackupPlanSubRoutesUnknownID(t *testing.T) {
	setupBackupPlanConfig(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/backup-plans/missing"},
		{http.MethodDelete, "/api/backup-plans/missing"},
		{http.MethodPost, "/api/backup-plans/missing/run"},
		{http.MethodPut, "/api/backup-plans/missing"},
	} {
		rec := httptest.NewRecorder()
		HandleBackupPlanSubRoutes(rec, asAdminRequest(httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"cron":"0 3 * * *"}`))))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status=%d want 404; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// TestExecuteBackupPlanNoTargets 验证目标不存在时同步返回 failed（不触发重活）。
func TestExecuteBackupPlanNoTargets(t *testing.T) {
	setupBackupPlanConfig(t)

	run := executeBackupPlan(config.BackupPlan{ID: "bp-x", ContainerID: 999, Cron: "0 3 * * *", Keep: 3})
	if run.Status != "failed" {
		t.Fatalf("status = %s, want failed", run.Status)
	}
	if run.Error == "" {
		t.Fatal("应带上失败原因")
	}
}

// TestBackupPlanNextFire 验证 cron 下次触发时间计算。
func TestBackupPlanNextFire(t *testing.T) {
	from := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	next, err := backupPlanNextFire("30 4 * * *", from)
	if err != nil {
		t.Fatalf("计算失败: %v", err)
	}
	parsed, ok := parseBackupPlanTime(next)
	if !ok {
		t.Fatalf("无法解析下次运行时间: %q", next)
	}
	if parsed.Hour() != 4 || parsed.Minute() != 30 {
		t.Fatalf("下次运行 = %v, want 04:30", parsed)
	}
	if !parsed.After(from) {
		t.Fatalf("下次运行必须晚于当前时间")
	}

	if _, err := backupPlanNextFire("bogus", from); err == nil {
		t.Fatal("非法 cron 应报错")
	}
}