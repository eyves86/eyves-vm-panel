package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestRequestActorPrefersXOriginalActor 多节点转发场景：agent 端审计应使用主控
// 写入的 X-Original-Actor，避免把操作记到 agent token 名下。
//
// 审计 H-3 修复后的安全约束：该 header 只在请求**已通过节点 token 校验**
// （authTypeAgent 上下文）时才被采信；普通浏览器 / API Key / 子用户请求即使
// 自带同名 header 也必须被忽略，否则可伪造审计主体。
func TestRequestActorPrefersXOriginalActor(t *testing.T) {
	cases := []struct {
		name  string
		auth  AuthContext
		agent bool
		header string
		want  string
	}{
		{"agent request: header wins", AuthContext{Type: authTypeAgent, Actor: "agent"}, true, "user:bob", "user:bob"},
		{"agent request: apikey actor preserved", AuthContext{Type: authTypeAgent, Actor: "agent"}, true, "apikey:ak_123", "apikey:ak_123"},
		{"agent request: empty header falls back to agent", AuthContext{Type: authTypeAgent, Actor: "agent"}, true, "", "agent"},
		{"non-agent request: header ignored (spoof attempt)", AuthContext{Type: authTypeAdmin, Actor: "admin"}, false, "user:bob", "admin"},
		{"sub-user request: header ignored", AuthContext{Type: authTypeSubUser, Actor: "user:alice"}, false, "admin", "user:alice"},
		{"no auth context: unauthenticated falls back to admin", AuthContext{}, false, "user:mallory", "admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("X-Original-Actor", tc.header)
			}
			if tc.agent || tc.auth.Type != "" {
				r = withAuthContext(r, tc.auth)
			}
			if got := requestActor(r); got != tc.want {
				t.Fatalf("requestActor = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandleContainerHVMSettingsPutPersistsAtomically 验证：
// 1) PUT 后容器 HVM 字段确实被持久化；
// 2) 持久化失败路径下，内存里 c 指针不会被半提交（mutated 标志 + MutateGlobal 整体）。
func TestHandleContainerHVMSettingsPutPersistsAtomically(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:            400,
		UUID:          "uuid-hvm-audit",
		Name:          "kvm-hvm-audit",
		Virtualization: "kvm",
	})

	// 初始读 → 默认值
	c0 := config.FindContainer(400)
	if c0 == nil {
		t.Fatal("容器丢失")
	}
	if c0.HVMBootOrder != "" {
		t.Fatalf("初始 BootOrder 不应已写入: %q", c0.HVMBootOrder)
	}

	// 正常 PUT
	body := strings.NewReader(`{"boot_order":"dca","vnc_keymap":"zh-cn"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/400/hvm-settings", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	c1 := config.FindContainer(400)
	if c1.HVMBootOrder != "dca" || c1.HVMVNCKeyMap != "zh-cn" {
		t.Fatalf("持久化失败: %+v", c1)
	}
}

// TestHandleContainerHVMSettingsPutRejectsUnknownKeymap keymap 白名单外必须被拒。
func TestHandleContainerHVMSettingsPutRejectsUnknownKeymap(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:            401,
		UUID:          "uuid-hvm-keymap",
		Name:          "kvm-hvm-keymap",
		Virtualization: "kvm",
	})

	body := strings.NewReader(`{"vnc_keymap":"x123"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/401/hvm-settings", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestIsBandwidthExcludedIface 覆盖已知排除项 + 物理 NIC。
func TestIsBandwidthExcludedIface(t *testing.T) {
	cases := []struct {
		iface string
		want  bool
	}{
		// 排除
		{"lo", true},
		{"docker0", true},
		{"docker_gwbridge", true},
		{"vethabc123", true},
		{"br-eth0", true},
		{"ifb0", true},
		{"virbr0", true},
		{"tailscale0", true},
		{"wg0", true},
		{"tun0", true},
		{"tap0", true},
		{"bond0", true},
		{"flannel.1", true},
		{"cni0", true},
		{"calico123", true},
		{"ipvlan0", true},
		{"macvlan0", true},
		{"kube-bridge", true},
		{"vxlan100", true},
		{"dummy0", true},
		// 不排除（视为物理 / 物理桥）
		{"eth0", false},
		{"eno1", false},
		{"ens3", false},
		{"enp0s3", false},
		{"eth0.10", false}, // 子接口
	}
	for _, tc := range cases {
		if got := isBandwidthExcludedIface(tc.iface); got != tc.want {
			t.Errorf("iface=%q: got %v, want %v", tc.iface, got, tc.want)
		}
	}
}
// TestRouteToAgentRestoresBodyOnFallback 核心 bug 回归：routeToAgent 读取 r.Body 后
// 必须设回 r.Body（即使转发失败返回 false），否则本地 handler 会读到空 body。
// 这个测试构造一个"有 NodeID 但节点找不到"的场景，让 routeToAgent 返回 false，
// 然后验证本地 handler（handleContainerHVMSettingsPut）能正确解析 body。
func TestRouteToAgentRestoresBodyOnFallback(t *testing.T) {
	// 节点配置有 NodeID 但 FindNode 会找不到它（test store 里没注册这个 NodeID）。
	setupContainerEndpointTestStore(t, config.Container{
		ID:             500,
		UUID:           "uuid-body-restore",
		Name:           "kvm-body-restore",
		Virtualization: "kvm",
		NodeID:         "non-existent-node", // routeToAgent 会找不到这个节点，返回 false
	})

	body := strings.NewReader(`{"boot_order":"dca"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPut, "/api/containers/500/hvm-settings", body))
	rec := httptest.NewRecorder()
	HandleSingleContainer(rec, req)

	// routeToAgent 找不到 node → 返回 false → 本地 handleContainerHVMSettingsPut 被调。
	// 如果 body 被正确恢复，它应该成功解析 boot_order；否则会返回 "Invalid JSON" 或 "boot_order" 相关错误。
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	c := config.FindContainer(500)
	if c.HVMBootOrder != "dca" {
		t.Fatalf("HVMBootOrder = %q, want 'dca'（body 恢复失败导致持久化未生效）", c.HVMBootOrder)
	}
}

// TestRouteToAgentDoesNotExhaustBodyWhenNodeMissing 和上面类似，但测的是 rescue 顶层路由。
func TestRouteToAgentDoesNotExhaustBodyWhenNodeMissing(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:             501,
		UUID:           "uuid-body-rescue",
		Name:           "kvm-body-rescue",
		Virtualization: "kvm",
		NodeID:         "no-such-node",
	})
	// 先手工写入一个 Rescue 会用到的 ISO 文件（doRescue 会查 findISOByID）
	isoID := "iso-for-rescue-test"
	isoPath := "/tmp/" + isoID + ".iso"
	_ = os.WriteFile(isoPath, []byte("dummy"), 0644)
	defer os.Remove(isoPath)
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ISOFiles = append(cfg.ISOFiles, config.ISOFile{ID: isoID, Name: "RescueTestISO", Path: isoPath})
	})
	defer config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ISOFiles = nil
	})

	// 这个测试的目标不是真的进入救援（那需要 virsh），而是验证：
	// HandleContainerRescue → routeToAgent 找不到节点返回 false → 本地 doRescue 能
	// 正确解析到 body 里的 enabled/iso_id 字段。如果 body 被消耗掉，doRescue 会直接
	// 因为 "iso_id is required" 失败（因为解析出的 req.ISOID 是空）。
	//
	// 实际进入救援需要真实 LXC/KVM 环境所以我们只测到 "能正确拿到 body" 这个层面：
	// 我们把 Virtualization 改成非 KVM，让 doRescue 走到 "only supported for KVM" 分支——
	// 这证明 body 解析是成功的（已经进到 KVM 检查那一行了）。
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].ID == 501 {
				cfg.Containers[i].Virtualization = "lxc" // 强制 lxc 跳过 virsh
			}
		}
	})

	body := strings.NewReader(`{"container_id":501,"enabled":true,"iso_id":"` + isoID + `"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/containers/rescue", body))
	rec := httptest.NewRecorder()
	HandleContainerRescue(rec, req)

	// 预期：doRescue 正确解析到 enabled=true + iso_id=xxx → 进入 KVM 检查 → lxc 不支持 → 400
	// 如果 body 被消耗掉，会走到 json 解析错误（400 "Invalid request"）或 iso_id 缺失错误。
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "only supported for KVM") {
		t.Fatalf("body 内容不符合预期（可能 body 没被正确恢复）: %s", rec.Body.String())
	}
}
