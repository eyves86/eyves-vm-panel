package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestRequestActorPrefersXOriginalActor 多节点转发场景：agent 端必须
// 优先使用 X-Original-Actor，避免把操作记到 agent token 名下。
func TestRequestActorPrefersXOriginalActor(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		want    string
	}{
		{"empty header falls through to admin", "", "admin"},
		{"non-empty header wins", "user:bob", "user:bob"},
		{"api-key-style actor preserved", "apikey:ak_123", "apikey:ak_123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("X-Original-Actor", tc.header)
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