package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// setupContainerEndpointTestStore 初始化临时配置库（Postgres）+ 单一容器，便于
// 调用 /api/containers/{id}/<action> 端点时的 MutateGlobal 落库。
func setupContainerEndpointTestStore(t *testing.T, container config.Container) {
	t.Helper()
	requirePGTest(t)
	dir := t.TempDir()
	previous := config.AppConfig
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		// 必须持锁恢复：后台任务队列 goroutine 仍在读写 AppConfig，
		// 裸写会与 SaveConfig 等构成数据竞争（-race 下必报）。
		config.AppConfigMu.Lock()
		config.AppConfig = previous
		config.AppConfigMu.Unlock()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Containers = []config.Container{container}
	}); err != nil {
		t.Fatalf("写入测试容器失败: %v", err)
	}
}

func decodeAPIData(t *testing.T, rec *httptest.ResponseRecorder, dst interface{}) {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("请求失败: code=%s message=%s", resp.Code, resp.Message)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("序列化 data 失败: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("反序列化 data 失败: %v; raw=%s", err, string(raw))
	}
}

// TestHandleContainerStatsKVMReturnsSpecLimits 验证 KVM 容器监控接口在没有
// qemu-guest-agent 时返回规格上限 + 提示，且不会 panic。
func TestHandleContainerStatsKVMReturnsSpecLimits(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:            100,
		UUID:          "uuid-stats-kvm",
		Name:          "kvm-stats",
		Virtualization: "kvm",
		VCPU:          4,
		RAMMB:         4096,
		DiskGB:        50,
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/100/stats", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	// KVM 无 guest-agent 时接口返回 success=false + 规格上限 data + 提示。
	if resp.Success {
		t.Fatalf("KVM 无 guest-agent 应返回 success=false; body=%s", rec.Body.String())
	}
	if !strings.Contains(resp.Message, "qemu-guest-agent") {
		t.Fatalf("提示未提及 guest-agent: %s", resp.Message)
	}
	raw, _ := json.Marshal(resp.Data)
	var got statsResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("解析 data 失败: %v", err)
	}
	if got.CPU.LimitCores != 4 {
		t.Fatalf("LimitCores = %d, want 4", got.CPU.LimitCores)
	}
	if got.RAM.LimitMB != 4096 {
		t.Fatalf("LimitMB = %d, want 4096", got.RAM.LimitMB)
	}
	if got.Disk.LimitGB != 50 {
		t.Fatalf("LimitGB = %v, want 50", got.Disk.LimitGB)
	}
	if got.Source != "unavailable" {
		t.Fatalf("Source = %q, want unavailable", got.Source)
	}
}

// TestHandleContainerBandwidthReturnsFallbackSource LXC 单机本地采集，
// 在主控上能拿到 Source=local/unavailable。
func TestHandleContainerBandwidthReturnsFallbackSource(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   101,
		UUID: "uuid-bw-lxc",
		Name: "lxc-bw",
		Virtualization: "lxc",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/101/bandwidth?period=hourly", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got bandwidthResponse
	decodeAPIData(t, rec, &got)
	if got.Period != "hourly" {
		t.Fatalf("Period = %q, want hourly", got.Period)
	}
	if got.Unit != "GB" {
		t.Fatalf("Unit = %q, want GB", got.Unit)
	}
}

// TestHandleContainerProcessesKVMRequiresGuestAgent KVM 进程接口要明确说明
// 依赖 qemu-guest-agent，而不是返回空数组。
func TestHandleContainerProcessesKVMRequiresGuestAgent(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   102,
		UUID: "uuid-proc-kvm",
		Name: "kvm-proc",
		Virtualization: "kvm",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/102/processes", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Fatalf("KVM 进程接口在无 guest-agent 时应返回 success=false; body=%s", rec.Body.String())
	}
	if !strings.Contains(resp.Message, "qemu-guest-agent") {
		t.Fatalf("提示未提及 guest-agent: %s", resp.Message)
	}
}

// TestParsePSOutput 解析 ps 输出，覆盖正常 / 空行 / 字段不足三种情况。
func TestParsePSOutput(t *testing.T) {
	in := "  100 root  3.5  1.2 12345 S /usr/sbin/sshd -D\n" +
		"\n" +
		"101 nobody  0.0  0.0 1024 R nginx: worker process\n" +
		"junk line\n"
	out := parsePSOutput(in)
	if len(out) != 2 {
		t.Fatalf("解析数量 = %d, want 2; out=%+v", len(out), out)
	}
	if out[0].PID != 100 || out[0].User != "root" || out[0].CPUPct != 3.5 {
		t.Fatalf("第一行解析错误: %+v", out[0])
	}
	if out[0].Command != "/usr/sbin/sshd -D" {
		t.Fatalf("Command 拼装错误: %q", out[0].Command)
	}
	if out[1].PID != 101 {
		t.Fatalf("第二行 PID 错误: %+v", out[1])
	}
}

// TestHandleContainerServicesKVMRequiresGuestAgent KVM 服务接口同样要求
// qemu-guest-agent，否则应返回 Source=unavailable 而非空 services。
func TestHandleContainerServicesKVMRequiresGuestAgent(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   103,
		UUID: "uuid-svc-kvm",
		Name: "kvm-svc",
		Virtualization: "kvm",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/103/services", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Fatalf("KVM 服务接口无 guest-agent 应返回 success=false; body=%s", rec.Body.String())
	}
}

// TestServiceActionRequestRejectsBadName 服务名非法字符必须被拒绝。
func TestServiceActionRequestRejectsBadName(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   104,
		UUID: "uuid-svc-action",
		Name: "lxc-svc-action",
		Virtualization: "lxc",
	})

	body := strings.NewReader(`{"service":"bad; rm -rf /","action":"start"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/104/services", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestServiceActionRequestRejectsBadAction 不在白名单的 action 必须被拒绝。
func TestServiceActionRequestRejectsBadAction(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   105,
		UUID: "uuid-svc-action2",
		Name: "lxc-svc-action2",
		Virtualization: "lxc",
	})

	body := strings.NewReader(`{"service":"nginx","action":"rm"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/105/services", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleContainerHVMSettingsRejectsNonKVM LXC 容器读 HVM 必须被拒绝。
func TestHandleContainerHVMSettingsRejectsNonKVM(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   106,
		UUID: "uuid-hvm-lxc",
		Name: "lxc-hvm",
		Virtualization: "lxc",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/106/hvm-settings", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleContainerHVMSettingsKVMGetReturnsDefaults KVM 读 HVM 应返回默认值。
func TestHandleContainerHVMSettingsKVMGetReturnsDefaults(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   107,
		UUID: "uuid-hvm-kvm",
		Name: "kvm-hvm",
		Virtualization: "kvm",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/107/hvm-settings", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got hvmSettingsResponse
	decodeAPIData(t, rec, &got)
	if got.BootOrder != "cda" {
		t.Fatalf("默认 boot_order = %q, want cda", got.BootOrder)
	}
	if got.NicDriver != "virtio" {
		t.Fatalf("默认 nic_driver = %q, want virtio", got.NicDriver)
	}
	if got.VNCKeyMap != "en-us" {
		t.Fatalf("默认 vnc_keymap = %q, want en-us", got.VNCKeyMap)
	}
	if len(got.AvailableKeyMaps) == 0 || len(got.AvailableNicDrivers) == 0 {
		t.Fatalf("Available* 列表不应为空")
	}
}

// TestHandleContainerHVMSettingsKVMUpdateWhitelist PUT 时不在白名单的值必须被拒。
func TestHandleContainerHVMSettingsKVMUpdateWhitelist(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   108,
		UUID: "uuid-hvm-kvm-update",
		Name: "kvm-hvm-update",
		Virtualization: "kvm",
	})

	body := strings.NewReader(`{"boot_order":"xyz","nic_driver":"virtio"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/108/hvm-settings", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleContainerHVMSettingsKVMUpdateOK 正常写入应落库并可重读。
func TestHandleContainerHVMSettingsKVMUpdateOK(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   109,
		UUID: "uuid-hvm-kvm-ok",
		Name: "kvm-hvm-ok",
		Virtualization: "kvm",
	})

	body := strings.NewReader(`{"boot_order":"dca","nic_driver":"e1000","vnc_keymap":"zh-cn"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/109/hvm-settings", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	c := config.FindContainer(109)
	if c == nil {
		t.Fatal("容器丢失")
	}
	if c.HVMBootOrder != "dca" || c.HVMNicDriver != "e1000" || c.HVMVNCKeyMap != "zh-cn" {
		t.Fatalf("HVM 设置未持久化: %+v", c)
	}
}

// TestScheduledActionsLifecycle 创建 → 列表 → 删除，校验字段与白名单。
func TestScheduledActionsLifecycle(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   200,
		UUID: "uuid-sca",
		Name: "lxc-sca",
		Virtualization: "lxc",
	})

	// 创建
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := strings.NewReader(`{"type":"stop","execute_at":"` + future + `","repeat":"daily"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/200/scheduled-actions", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var created config.ScheduledAction
	decodeAPIData(t, rec, &created)
	if created.ContainerID != 200 || created.Type != "stop" || created.Repeat != "daily" {
		t.Fatalf("返回字段错误: %+v", created)
	}
	if created.ID == "" || !strings.HasPrefix(created.ID, "sca-") {
		t.Fatalf("ID 格式错误: %q", created.ID)
	}

	// 列表
	req = asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/containers/200/scheduled-actions", nil))
	rec = httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list scheduledActionsResponse
	decodeAPIData(t, rec, &list)
	if list.Total != 1 {
		t.Fatalf("total = %d, want 1", list.Total)
	}

	// 删除
	req = asAdminRequest(httptest.NewRequest(http.MethodDelete, "/api/containers/200/scheduled-actions/"+created.ID, nil))
	rec = httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if remain := config.ListScheduledActions(200); len(remain) != 0 {
		t.Fatalf("删除后仍有 %d 条", len(remain))
	}
}

// TestScheduledActionCreateRejectsPastPast Past time 必须被拒。
func TestScheduledActionCreateRejectsPast(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   201,
		UUID: "uuid-sca-past",
		Name: "lxc-sca-past",
		Virtualization: "lxc",
	})

	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	body := strings.NewReader(`{"type":"stop","execute_at":"` + past + `"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/201/scheduled-actions", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestScheduledActionCreateRejectsBadType type 白名单外必须被拒。
func TestScheduledActionCreateRejectsBadType(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   202,
		UUID: "uuid-sca-type",
		Name: "lxc-sca-type",
		Virtualization: "lxc",
	})

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := strings.NewReader(`{"type":"destroy","execute_at":"` + future + `"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/202/scheduled-actions", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestScheduledActionCreateRejectsBadFormat execute_at 解析失败必须被拒。
func TestScheduledActionCreateRejectsBadFormat(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   203,
		UUID: "uuid-sca-fmt",
		Name: "lxc-sca-fmt",
		Virtualization: "lxc",
	})

	body := strings.NewReader(`{"type":"stop","execute_at":"tomorrow"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/203/scheduled-actions", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestScheduledActionDeleteRejectsBadPrefix actionID 格式错误必须被拒。
func TestScheduledActionDeleteRejectsBadPrefix(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   204,
		UUID: "uuid-sca-del",
		Name: "lxc-sca-del",
		Virtualization: "lxc",
	})

	req := asAdminRequest(httptest.NewRequest(http.MethodDelete, "/api/containers/204/scheduled-actions/bad-id", nil))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestProcessKillRejectsPIDZero PID 0 / 负数必须被拒。
func TestProcessKillRejectsPIDZero(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   205,
		UUID: "uuid-kill",
		Name: "lxc-kill",
		Virtualization: "lxc",
	})

	body := strings.NewReader(`{"pids":[0, -1]}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/205/processes/kill", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestParseDFBGRejectsEmpty 空输入必须返回错误而非 panic。
func TestParseDFBGRejectsEmpty(t *testing.T) {
	if _, _, err := parseDFBG(""); err == nil {
		t.Fatal("空输入应返回错误")
	}
	if _, _, err := parseDFBG("\n\n\n"); err == nil {
		t.Fatal("仅空行输入应返回错误")
	}
}

// TestParseDFInodesRejectsEmpty 同上。
func TestParseDFInodesRejectsEmpty(t *testing.T) {
	if _, _, err := parseDFInodes(""); err == nil {
		t.Fatal("空输入应返回错误")
	}
}