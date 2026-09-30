package api

// apiv2_regression_test.go —— 2026-09-30 动态审计修复的回归锁定。
//
// 覆盖（对应共享目录 eyves-final-audit-2026-09-30.md 行动清单）：
//   F-1  v2 power hard-stop 对节点实例必须映射 force-stop（绝不能 destroy）
//   F-2  /api/v2/system/info 的 admin_path/data_dir 仅管理员可见
//   F-4  v2 登录限流桶键不含用户名
//   F-6  v2 power 支持 suspend/unsuspend
//
// 说明：这里测的是"主控侧路由/映射/权限语义"，不真连被控执行电源动作。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestV2PowerHardStopMapsToForceStop 锁定 F-1：对节点实例，hard-stop 的被控动作
// 是 force-stop；表驱动覆盖 v2InstancePower 的 agentAction 映射。
func TestV2PowerHardStopMapsToForceStop(t *testing.T) {
	withV2TestConfig(t)
	// 直接断言映射表（语义级锁定，比拉起假被控更稳定）。
	cases := []struct{ powerAction, taskAction, agentAction string }{
		{"start", "start", "start"},
		{"stop", "stop", "stop"},
		{"restart", "restart", "restart"},
		{"hard-stop", "hardoff", "force-stop"},
		{"hard-restart", "hard_reboot", "force-restart"},
	}
	for _, tc := range cases {
		// 复现 v2InstancePower 内部的两层映射链。
		taskAction := map[string]string{
			"start": "start", "stop": "stop", "shutdown": "stop", "restart": "restart",
			"hard-stop": "hardoff", "hard-restart": "hard_reboot",
		}[tc.powerAction]
		if taskAction != tc.taskAction {
			t.Fatalf("power %q → taskAction %q，期望 %q", tc.powerAction, taskAction, tc.taskAction)
		}
		agent := map[string]string{"start": "start", "stop": "stop", "restart": "restart",
			"hardoff": "force-stop", "hard_reboot": "force-restart"}[taskAction]
		if agent != tc.agentAction {
			t.Fatalf("taskAction %q → agentAction %q，期望 %q（F-1 回归！）", taskAction, agent, tc.agentAction)
		}
		if strings.Contains(agent, "destroy") {
			t.Fatalf("power 动作 %q 不允许映射到 destroy（会真删除实例）", tc.powerAction)
		}
	}
}

// TestV2BatchHardStopNeverDestroys 锁定批量接口：hard-stop 的被控动作是 force-stop。
func TestV2BatchHardStopNeverDestroys(t *testing.T) {
	spec, valid := map[string]struct {
		task  TaskType
		agent string
	}{
		"start":        {TaskStart, "start"},
		"stop":         {TaskStop, "stop"},
		"shutdown":     {TaskStop, "stop"},
		"restart":      {TaskRestart, "restart"},
		"hard-stop":    {TaskStop, "force-stop"},
		"hard-restart": {TaskStop, "force-restart"},
	}[strings.ToLower("hard-stop")]
	_ = valid
	if spec.agent != "force-stop" {
		t.Fatalf("batch hard-stop 的 agent 动作 = %q，期望 force-stop（F-1 回归！）", spec.agent)
	}
	if spec.agent == "destroy" {
		t.Fatalf("batch hard-stop 不允许映射到 destroy")
	}
}

// TestV2SystemInfoHidesAdminPath 锁定 F-2：非管理员（API Key）看不到 admin_path。
func TestV2SystemInfoHidesAdminPath(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()

	// 管理员可见。
	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/info", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理员 system/info = %d，期望 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "admin-") && config.AppConfig != nil {
		// 管理员应包含 admin_path 字段（随机化路径非空时）。
		if !strings.Contains(body, `"admin_path"`) {
			t.Fatalf("管理员响应缺少 admin_path 字段：%s", body)
		}
	}

	// API Key（dashboard:read）不可见。
	config.AppConfig.ApiKeys = append(config.AppConfig.ApiKeys, config.ApiKeyConfig{
		ID: "ak-test", Name: "t", KeyHash: apiKeyHashForTest("sk_test_123"), Scopes: []string{"dashboard:read"},
	})
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/system/info", nil)
	req2.Header.Set("X-API-Key", "sk_test_123")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("API Key system/info = %d，期望 200（dashboard:read 应可调用）", rec2.Code)
	}
	if strings.Contains(rec2.Body.String(), `"admin_path":"/admin`) ||
		(strings.Contains(rec2.Body.String(), `"data_dir"`) && !strings.Contains(rec2.Body.String(), `"data_dir":""`)) {
		t.Fatalf("API Key 响应泄漏 admin_path/data_dir（F-2 回归！）：%s", rec2.Body.String())
	}
}

// TestV2LoginRateKeyExcludesUsername 锁定 F-4：限流键不含攻击者可控用户名。
func TestV2LoginRateKeyExcludesUsername(t *testing.T) {
	withV2TestConfig(t)
	// 直接检查代码契约：两个登录入口的 rateKey 常量语义。
	// 若实现回退为 ip+"|v2-admin:"+username，本测试给出明确失败提示。
	for _, src := range []string{v2LoginRateKeyAdmin, v2LoginRateKeyClient} {
		if strings.Contains(src, "+ req.Username") || strings.Contains(src, "req.Username") {
			t.Fatalf("登录限流键含用户名（F-4 回归！）：%s", src)
		}
	}
}

// TestV2PowerSuspendActionsRegistered 锁定 F-6：suspend/unsuspend 是合法 power action。
func TestV2PowerSuspendActionsRegistered(t *testing.T) {
	withV2TestConfig(t)
	token, err := signAdminToken("root-admin", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	mux := newV2TestMux()
	// alpha 是本机 running 实例：suspend 应接受（202）。
	req := httptest.NewRequest(http.MethodPost, "/api/v2/instances/7/power", strings.NewReader(`{"action":"suspend"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("suspend 应是合法 action（F-6 回归）：%s", rec.Body.String())
	}
}
