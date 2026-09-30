package api

// apiv2_recycle_test.go —— 回收站 / 批量扩展 / EIP 绑定回归测试（NetJett 差距收口）。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

func TestV2DeleteRecyclesByDefault(t *testing.T) {
	// MutateContainerByID 持久化需要 DB（与 EIP 绑定测试同款初始化，先 DB 后换配置）。
	dir := t.TempDir()
	config.SetConfigPath(dir + "/config.json")
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	token := withV2TestConfig(t)
	mux := newV2TestMux()

	// alpha（ID 7，running）删除 → 应进回收站而非销毁。
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/instances/7", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("DELETE = %d，期望 202（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"recycled":true`) {
		t.Fatalf("响应应含 recycled:true：%s", rec.Body.String())
	}
	c := config.FindContainer(7)
	if c == nil || c.RecycledAt == "" {
		t.Fatal("实例应带 recycled_at 标记（数据面未动）")
	}

	// 默认列表不再出现它。
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/instances", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), `"name":"alpha"`) {
		t.Fatal("默认列表不应包含回收站实例")
	}

	// 回收站列表出现它。
	req3 := httptest.NewRequest(http.MethodGet, "/api/v2/recycle-bin", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)
	if !strings.Contains(rec3.Body.String(), `"name":"alpha"`) {
		t.Fatalf("回收站列表应包含 alpha：%s", rec3.Body.String())
	}

	// 恢复。
	req4 := httptest.NewRequest(http.MethodPost, "/api/v2/instances/7/restore", nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("restore = %d：%s", rec4.Code, rec4.Body.String())
	}
	if c := config.FindContainer(7); c == nil || c.RecycledAt != "" {
		t.Fatal("恢复后 recycled_at 应为空")
	}
}

func TestV2DeletePurgeSemantics(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()
	// ?purge=true 走任务队列真删除（本机实例入队）。
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/instances/8?purge=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("purge DELETE = %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"purged":true`) {
		t.Fatalf("purge 响应缺 purged:true：%s", rec.Body.String())
	}
}

func TestV2BatchExtendedActions(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()

	// 批量备注。
	req := httptest.NewRequest(http.MethodPost, "/api/v2/instances/batch",
		strings.NewReader(`{"action":"remark","ids":[7,8],"params":{"remark":"客户A"}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch remark = %d：%s", rec.Code, rec.Body.String())
	}
	if c := config.FindContainer(7); c == nil || c.Remark != "客户A" {
		t.Fatalf("remark 未生效：%+v", c)
	}

	// 批量到期。
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/instances/batch",
		strings.NewReader(`{"action":"expiry","ids":[7],"params":{"expires_at":"2027-01-01"}}`))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("batch expiry = %d：%s", rec2.Code, rec2.Body.String())
	}
	if c := config.FindContainer(7); c == nil || !strings.Contains(c.ExpiresAt, "2027") {
		t.Fatalf("expiry 未生效：%+v", c)
	}
}

func TestV2IPPoolAttachDetach(t *testing.T) {
	// MutateContainerByID 持久化需要 DB：用容器端点测试同款初始化（临时 sqlite）。
	// 顺序：先 InitConfig 再 withV2TestConfig（后者整体替换 AppConfig 并签发
	// 与其 JWTSecret 匹配的 token），否则 token 校验 401。
	dir := t.TempDir()
	config.SetConfigPath(dir + "/config.json")
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	token := withV2TestConfig(t)
	mux := newV2TestMux()
	config.AppConfig.PublicIPv4Pool = append(config.AppConfig.PublicIPv4Pool,
		config.PublicIPv4Assignment{Address: "203.0.113.10", Interface: "eth0"})

	// attach。
	req := httptest.NewRequest(http.MethodPost, "/api/v2/ip-pools/attach",
		strings.NewReader(`{"address":"203.0.113.10","instance_id":7}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach = %d：%s", rec.Code, rec.Body.String())
	}
	if c := config.FindContainer(7); c == nil || len(c.PublicIPv4s) != 1 || c.PublicIPv4s[0].Address != "203.0.113.10" {
		t.Fatal("attach 未生效")
	}

	// 重复 attach → 409。
	rec1b := httptest.NewRecorder()
	req1b := httptest.NewRequest(http.MethodPost, "/api/v2/ip-pools/attach",
		strings.NewReader(`{"address":"203.0.113.10","instance_id":7}`))
	req1b.Header.Set("Authorization", "Bearer "+token)
	req1b.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec1b, req1b)
	if rec1b.Code != http.StatusConflict {
		t.Fatalf("重复 attach = %d，期望 409", rec1b.Code)
	}

	// detach。
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/ip-pools/detach",
		strings.NewReader(`{"address":"203.0.113.10"}`))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("detach = %d：%s", rec2.Code, rec2.Body.String())
	}
	if c := config.FindContainer(7); c == nil || len(c.PublicIPv4s) != 0 {
		t.Fatal("detach 未生效")
	}
}

func TestV2InstancesExportCSV(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/instances/export.csv", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %s，期望 text/csv", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "eyvescloud") && !strings.Contains(body, "alpha") {
		t.Fatalf("CSV 应包含实例数据：%s", body)
	}
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatal("CSV 缺少 UTF-8 BOM（Excel 兼容）")
	}
}

// timeNowOld 返回 n 天前的 "2006-01-02 15:04:05"（测试辅助）。
func timeNowOld(days int) string {
	return time.Now().AddDate(0, 0, -days).Format("2006-01-02 15:04:05")
}

func TestRecyclePurgeDue(t *testing.T) {
	withV2TestConfig(t)
	// 构造一个 8 天前回收的实例。
	config.MutateContainerByID(7, func(target *config.Container) {
		target.RecycledAt = timeNowOld(8)
	})
	due := config.RecyclePurgeDue(7)
	found := false
	for _, id := range due {
		if id == 7 {
			found = true
		}
	}
	if !found {
		t.Fatal("8 天前回收的实例应到期（保留 7 天）")
	}
	// 保留 30 天则未到期。
	if due30 := config.RecyclePurgeDue(30); len(due30) > 0 {
		for _, id := range due30 {
			if id == 7 {
				t.Fatal("保留 30 天时 8 天前的实例不应到期")
			}
		}
	}
}
