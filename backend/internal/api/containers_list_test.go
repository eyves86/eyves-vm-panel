package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"eyvescloud/internal/config"
)

// TestListContainersPaginatedMaterialization 守卫指针视界管线的响应契约：
// 分页正确、回收站被排除、属主名按 ID 派生（查得到填充 / 查不到置空）、
// 登录口令绝不入列表响应、未分页遗留契约返回全量数组。
func TestListContainersPaginatedMaterialization(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	seed := make([]config.Container, 25)
	for i := range seed {
		c := &seed[i]
		c.ID = i + 1
		c.UUID = fmt.Sprintf("uuid-%02d", i+1)
		c.Name = fmt.Sprintf("ct-%02d", i+1)
		c.Status = "running"
		c.Template = "ubuntu-22.04"
		c.NodeID = "n1"
		c.SSHPassword = "secret-pw"
		if i == 24 {
			c.RecycledAt = "2026-10-08 00:00:00"
		}
		if i%2 == 0 {
			c.OwnerSubUserID = "su-1"
		} else if i%3 == 0 {
			c.OwnerSubUserID = "su-gone"
		}
	}
	config.AppConfig = &config.EyvescloudConfig{
		Containers: seed,
		SubUsers:   []config.SubUser{{ID: "su-1", Username: "alice"}},
	}

	type item struct {
		ID            int    `json:"id"`
		OwnerUsername string `json:"owner_username"`
		SSHPassword   string `json:"ssh_password"`
		Name          string `json:"name"`
	}
	type listResp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []item `json:"items"`
			Total int    `json:"total"`
			Page  int    `json:"page"`
		} `json:"data"`
	}

	// 分页：回收站排除后 total=24，第一页为自然顺序前 10 条。
	w := httptest.NewRecorder()
	listContainers(w, httptest.NewRequest(http.MethodGet, "/api/containers?page=1&page_size=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp listResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 24 || resp.Data.Page != 1 || len(resp.Data.Items) != 10 {
		t.Fatalf("total=%d page=%d items=%d，应为 24/1/10", resp.Data.Total, resp.Data.Page, len(resp.Data.Items))
	}
	for k, it := range resp.Data.Items {
		if it.ID != k+1 {
			t.Fatalf("第 %d 项 ID=%d，自然顺序应为 %d", k, it.ID, k+1)
		}
		if it.SSHPassword != "" {
			t.Fatalf("容器 %d 的 SSHPassword 泄漏进列表响应", it.ID)
		}
		wantOwner := ""
		if (k+1)%2 == 1 {
			wantOwner = "alice"
		}
		if it.OwnerUsername != wantOwner {
			t.Fatalf("容器 %d owner_username=%q，应为 %q", it.ID, it.OwnerUsername, wantOwner)
		}
	}

	// 过滤 + 排序：owner=su-1 且按名称倒序。
	w = httptest.NewRecorder()
	listContainers(w, httptest.NewRequest(http.MethodGet, "/api/containers?page=1&page_size=5&owner=su-1&sort=name&order=desc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	resp = listResp{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 12 {
		t.Fatalf("owner=su-1 过滤后 total=%d，应为 12（i=0..23 偶数下标）", resp.Data.Total)
	}
	if len(resp.Data.Items) != 5 || resp.Data.Items[0].Name != "ct-23" {
		t.Fatalf("倒序第一项应为 ct-23，got %+v", resp.Data.Items)
	}

	// 未分页遗留契约：返回全量数组（含回收站排除）。
	w = httptest.NewRecorder()
	listContainers(w, httptest.NewRequest(http.MethodGet, "/api/containers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var legacy struct {
		Success bool   `json:"success"`
		Data    []item `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(legacy.Data) != 24 {
		t.Fatalf("未分页应返回全量 24 条，got %d", len(legacy.Data))
	}
	for _, it := range legacy.Data {
		if it.SSHPassword != "" {
			t.Fatalf("容器 %d 的 SSHPassword 泄漏进列表响应", it.ID)
		}
	}
}

// TestFilterContainersForRequestPtrUnrestricted 无鉴权上下文的请求不受容器级
// 限制：指针版过滤必须原样返回输入（不缩小、不重排）。
func TestFilterContainersForRequestPtrUnrestricted(t *testing.T) {
	views := []*config.Container{{ID: 1}, {ID: 2}}
	got := filterContainersForRequestPtr(httptest.NewRequest(http.MethodGet, "/api/containers", nil), views)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("非受限请求必须原样返回，got %+v", got)
	}
}

// TestContainersROViewContract 守卫 #143 读锁契约：视界与全局共享底层数组；
// release 后写锁立即可获取（读锁确已释放）；LocalProbeContainers 只返回本机
// 容器且按运行时分列（节点容器绝不可被本地探测触碰，v2.2.36 生产踩坑）。
func TestContainersROViewContract(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		Containers: []config.Container{
			{ID: 1, Name: "ct-1", Template: "ubuntu-22.04"},
			{ID: 2, Name: "ct-2", Template: "ubuntu-22.04", NodeID: "n1"},
			{ID: 3, Name: "vm-1", Template: "kvm-win11", Virtualization: config.VirtualizationKVM},
		},
	}

	ro, release := config.ContainersROView()
	if len(ro) != 3 || &ro[0] != &config.AppConfig.Containers[0] {
		t.Fatalf("视界必须与全局共享底层数组")
	}
	lxcOnly, kvmOnly := config.LocalProbeContainers()
	if len(lxcOnly) != 1 || lxcOnly[0].ID != 1 {
		t.Fatalf("LocalProbeContainers 应只含本机 LXC ct-1，got %+v", lxcOnly)
	}
	if len(kvmOnly) != 1 || kvmOnly[0].ID != 3 {
		t.Fatalf("LocalProbeContainers 应只含本机 KVM vm-1，got %+v", kvmOnly)
	}
	release()
	if !config.AppConfigMu.TryLock() {
		t.Fatalf("release 后读锁仍未释放")
	}
	config.AppConfigMu.Unlock()
}

// TestListContainersNeverWritesSharedArray 守卫「列表语义必须只读」：带过滤的
// 列表请求（views[:0] 原地复用 + 排序换指针）绝不能写坏共享底层数组——
// 回收站视图曾触发被回收实例的记录被后续元素覆写消失（实测踩坑）。
// 容器一律挂 NodeID：单测绝不触发本机探测（测试机上可能存在真实 LXC 容器，
// 探测会读真机状态——#125 同类事故）。
func TestListContainersNeverWritesSharedArray(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	seed := make([]config.Container, 6)
	for i := range seed {
		seed[i] = config.Container{ID: i + 1, Name: fmt.Sprintf("ct-%d", i+1), Status: "running", Template: "ubuntu-22.04", NodeID: "n1"}
	}
	seed[0].Tags = map[string]string{"env": "prod"}
	seed[2].Tenant = "t-a"
	seed[5].RecycledAt = "2026-10-08 00:00:00"
	config.AppConfig = &config.EyvescloudConfig{Containers: seed}

	// tag-key 过滤（views[:0] 原地复用路径）+ 排序 + 回收站排除 + 分页同时压上。
	w := httptest.NewRecorder()
	listContainers(w, httptest.NewRequest(http.MethodGet, "/api/containers?page=1&page_size=2&tag-key=env&sort=name&order=desc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d，body = %s", w.Code, w.Body.String())
	}
	if len(config.AppConfig.Containers) != 6 {
		t.Fatalf("共享数组长度被写坏：len=%d", len(config.AppConfig.Containers))
	}
	for i, c := range config.AppConfig.Containers {
		if c.ID != i+1 || c.Name != fmt.Sprintf("ct-%d", i+1) {
			t.Fatalf("共享数组元素被覆写：AppConfig.Containers[%d] = %+v", i, c)
		}
	}
}
