package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestSuspendedContainerPowerOpsRejectedCrossNode F1 回归：跨节点容器
// （NodeID 非空）走 routeToAgent 代理路径不经过任务队列，主控必须在转发前
// 拦截挂起/到期/流量超限容器的电源操作。
func TestSuspendedContainerPowerOpsRejectedCrossNode(t *testing.T) {
	cases := []struct {
		name      string
		container config.Container
		action    string
		wantMsg   string
	}{
		{
			name:      "suspended start rejected",
			container: config.Container{ID: 501, UUID: "uuid-susp-1", Name: "c-susp-1", NodeID: "node-x", Suspended: true},
			action:    "start",
			wantMsg:   "容器已挂起",
		},
		{
			name:      "suspended restart rejected",
			container: config.Container{ID: 502, UUID: "uuid-susp-2", Name: "c-susp-2", NodeID: "node-x", Suspended: true},
			action:    "restart",
			wantMsg:   "容器已挂起",
		},
		{
			name:      "suspended reinstall rejected",
			container: config.Container{ID: 503, UUID: "uuid-susp-3", Name: "c-susp-3", NodeID: "node-x", Suspended: true},
			action:    "reinstall",
			wantMsg:   "容器已挂起",
		},
		{
			name:      "expired start rejected",
			container: config.Container{ID: 504, UUID: "uuid-exp-1", Name: "c-exp-1", NodeID: "node-x", ExpiresAt: "2000-01-01"},
			action:    "start",
			wantMsg:   "容器已到期",
		},
		{
			name: "traffic exceeded start rejected",
			container: config.Container{
				ID: 505, UUID: "uuid-traffic-1", Name: "c-traffic-1", NodeID: "node-x",
				MonthlyTrafficGB: 1, TrafficUsedRX: 2 * 1073741824,
			},
			action:  "start",
			wantMsg: "容器流量已超限",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupContainerEndpointTestStore(t, tc.container)
			req := asAdminRequest(httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/containers/%d/%s", tc.container.ID, tc.action), nil))
			rec := httptest.NewRecorder()
			HandleSingleContainer(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("body 应包含 %q, got: %s", tc.wantMsg, rec.Body.String())
			}
		})
	}
}

// TestSuspendedContainerManageActionsRejected F1a 回归：挂起容器的破坏性
// 管理操作（ISO/救援/脚本/快照/备份还原/NAT/防火墙）统一 403。
func TestSuspendedContainerManageActionsRejected(t *testing.T) {
	cases := []struct {
		name   string
		method string
		action string
	}{
		{"iso attach", http.MethodPost, "iso"},
		{"rescue enter", http.MethodPost, "rescue"},
		{"recipe execute", http.MethodPost, "recipes/execute"},
		{"process kill", http.MethodPost, "processes/kill"},
		{"service action", http.MethodPost, "services"},
		{"snapshot create", http.MethodPost, "snapshots"},
		{"snapshot restore", http.MethodPost, "snapshots/snap-1/restore"},
		{"backup restore", http.MethodPost, "backups/bk-1/restore"},
		{"port mapping create", http.MethodPost, "port-mappings"},
		{"port mapping delete", http.MethodDelete, "port-mappings/1"},
		{"firewall save", http.MethodPut, "firewall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupContainerEndpointTestStore(t, config.Container{
				ID: 510, UUID: "uuid-mgmt-susp", Name: "c-mgmt-susp", NodeID: "node-x", Suspended: true,
			})
			req := asAdminRequest(httptest.NewRequest(tc.method, "/api/containers/510/"+tc.action, nil))
			rec := httptest.NewRecorder()
			HandleSingleContainer(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "容器已挂起") {
				t.Fatalf("body 应包含挂起提示, got: %s", rec.Body.String())
			}
		})
	}
}

// TestSuspendedContainerExemptActionsNotRejected 豁免项（unsuspend/stop/
// 计费字段调整/读取）不得被挂起拦截误伤。
func TestSuspendedContainerExemptActionsNotRejected(t *testing.T) {
	cases := []struct {
		name   string
		method string
		action string
	}{
		{"unsuspend allowed", http.MethodPost, "unsuspend"},
		{"stop allowed", http.MethodPost, "stop"},
		{"traffic-limit allowed", http.MethodPut, "traffic-limit"},
		{"expiry allowed", http.MethodPut, "expiry"},
		{"stats read allowed", http.MethodGet, "stats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupContainerEndpointTestStore(t, config.Container{
				ID: 511, UUID: "uuid-susp-exempt", Name: "c-susp-exempt", Suspended: true,
			})
			req := asAdminRequest(httptest.NewRequest(tc.method, "/api/containers/511/"+tc.action, nil))
			rec := httptest.NewRecorder()
			HandleSingleContainer(rec, req)

			// 不允许出现挂起 403 拦截（后续 handler 可能因其他原因返回错误码）。
			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "容器已挂起") {
				t.Fatalf("豁免操作 %s 不应被挂起拦截: %s", tc.action, rec.Body.String())
			}
		})
	}
}

// TestAgentStartRejectsSuspendedContainer F1 防御纵深：agent 端电源操作
// 最后一道检查——即使主控漏检，挂起容器也无法在被控节点开机。
func TestAgentStartRejectsSuspendedContainer(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID: 512, UUID: "uuid-agent-susp", Name: "c-agent-susp", Suspended: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/containers/512/start", nil)
	rec := httptest.NewRecorder()
	HandleAgentContainerAction(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "容器已挂起") {
		t.Fatalf("body 应包含挂起提示, got: %s", rec.Body.String())
	}
}

// TestAgentUnsuspendStillWorks agent 端 unsuspend 分支（先清标记再开机）
// 不得被防御纵深检查拦截。
func TestAgentUnsuspendStillWorks(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID: 513, UUID: "uuid-agent-unsusp", Name: "c-agent-unsusp", Suspended: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/containers/513/unsuspend", nil)
	rec := httptest.NewRecorder()
	HandleAgentContainerAction(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("unsuspend 不应被 403 拦截: %s", rec.Body.String())
	}
	// 配置标记应被清除。
	c := config.FindContainer(513)
	if c == nil || c.Suspended {
		t.Fatal("unsuspend 后 Suspended 标记应已清除")
	}
}

// TestIsSuspendedBlockedManageAction helper 纯逻辑表驱动。
func TestIsSuspendedBlockedManageAction(t *testing.T) {
	cases := []struct {
		action string
		method string
		want   bool
	}{
		{"iso", http.MethodPost, true},
		{"iso", http.MethodGet, false},
		{"rescue", http.MethodPost, true},
		{"snapshots", http.MethodGet, false},
		{"snapshots", http.MethodPost, true},
		{"snapshots/snap-1/restore", http.MethodPost, true},
		{"snapshots/snap-1", http.MethodDelete, false}, // 删除快照释放配额，放行
		{"backups", http.MethodGet, false},
		{"backups/bk-1/restore", http.MethodPost, true},
		{"port-mappings", http.MethodPost, true},
		{"port-mappings/3", http.MethodDelete, true},
		{"port-mappings/3", http.MethodGet, false},
		{"firewall", http.MethodPut, true},
		{"firewall", http.MethodGet, false},
		{"start", http.MethodPost, false},    // 电源类由调用方单独处理
		{"unsuspend", http.MethodPost, false},
		{"expiry", http.MethodPut, false},    // 计费字段豁免
		{"hostname", http.MethodPost, false},
	}
	for _, tc := range cases {
		if got := isSuspendedBlockedManageAction(tc.action, tc.method); got != tc.want {
			t.Errorf("isSuspendedBlockedManageAction(%q, %q) = %v, want %v", tc.action, tc.method, got, tc.want)
		}
	}
}
