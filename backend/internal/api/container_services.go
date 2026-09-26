package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// serviceInfo 单个服务条目，对齐 Virtualizor act=services 输出。
type serviceInfo struct {
	Name     string `json:"name"`
	State    string `json:"state"`     // running / stopped / failed / unknown
	Autostart bool  `json:"autostart"`
	Enabled  bool   `json:"enabled"`   // 是否 enable（开机自启）
	UnitType string `json:"unit_type"` // service / timer / socket …
}

type servicesResponse struct {
	ContainerID   int           `json:"container_id"`
	ContainerName string        `json:"container_name"`
	Total         int           `json:"total"`
	Services      []serviceInfo `json:"services"`
	SampledAt     string        `json:"sampled_at"`
	Source        string        `json:"source"`
}

// handleContainerServices 列出容器内 systemd 服务（GET /api/containers/{id}/services）。
// 对齐 Virtualizor act=services。
//
// 权限：container:read scope。
//
// 注意：只对 systemd 容器有意义；SysVinit / 无 init 容器返回空列表 + 提示。
func handleContainerServices(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "services") {
		return
	}
	resp := servicesResponse{
		ContainerID:   c.ID,
		ContainerName: c.Name,
		SampledAt:     time.Now().UTC().Format(time.RFC3339),
		Source:        "local",
	}
	if c.IsKVM() {
		resp.Source = "unavailable"
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: false,
			Message: "KVM 服务管理需 qemu-guest-agent",
			Data:    resp,
		})
		return
	}
	lxcName := c.LxcName()
	if lxcName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "容器 LXC 内部名缺失"})
		return
	}
	// systemctl list-units --type=service --all --no-legend --plain
	cmd := exec.Command("lxc-attach", "-n", lxcName, "--",
		"systemctl", "list-unit-files", "--type=service", "--no-legend", "--plain")
	out, err := cmd.Output()
	if err != nil {
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: false,
			Message: "systemctl 在容器内不可用（可能不是 systemd）",
			Data:    resp,
		})
		return
	}
	resp.Services = parseSystemctlUnitFiles(string(out))
	resp.Total = len(resp.Services)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

// parseSystemctlUnitFiles 解析 `systemctl list-unit-files --type=service` 输出：
//   NAME STATE
//   nginx.service enabled
//   ssh.service static
//   ...
func parseSystemctlUnitFiles(s string) []serviceInfo {
	var out []serviceInfo
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		// 仅保留 .service 单元
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		state := strings.ToLower(fields[1])
		out = append(out, serviceInfo{
			Name:     strings.TrimSuffix(name, ".service"),
			State:    "unknown",
			Enabled:  state == "enabled" || state == "static",
			UnitType: "service",
		})
	}
	return out
}

// serviceActionRequest 服务操作请求：start/stop/restart。
type serviceActionRequest struct {
	Service string `json:"service"`
	Action  string `json:"action"` // start / stop / restart / reload / enable / disable
}

// handleContainerServiceAction 单服务操作（POST /api/containers/{id}/services）。
// 对齐 Virtualizor act=services + start_x/stop_x/restart_x。
//
// 权限：container:power scope（写）。
//
// 安全：
//   - service 名仅允许字母数字 + . _ -（systemd 单元名规则子集），防止参数注入
//   - action 白名单 start/stop/restart/reload/enable/disable
//   - 单元名最大 128 字符
func handleContainerServiceAction(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:power") {
		return
	}
	if routeToAgent(w, r, c, "services/action") {
		return
	}
	var req serviceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	if req.Service == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "service 必填"})
		return
	}
	if len(req.Service) > 128 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "service 名过长（≤128）"})
		return
	}
	for _, c1 := range req.Service {
		ok := (c1 >= 'a' && c1 <= 'z') || (c1 >= 'A' && c1 <= 'Z') ||
			(c1 >= '0' && c1 <= '9') || c1 == '.' || c1 == '_' || c1 == '-'
		if !ok {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false, Message: "service 名含非法字符（仅允许字母数字 ._-）",
			})
			return
		}
	}
	switch req.Action {
	case "start", "stop", "restart", "reload", "enable", "disable":
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "action 必须为 start/stop/restart/reload/enable/disable",
		})
		return
	}
	if c.IsKVM() {
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false, Message: "KVM 服务管理需 qemu-guest-agent",
		})
		return
	}
	lxcName := c.LxcName()
	if lxcName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "容器 LXC 内部名缺失"})
		return
	}
	unit := req.Service + ".service"
	cmd := exec.Command("lxc-attach", "-n", lxcName, "--",
		"systemctl", req.Action, unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{
			Success: false,
			Message: req.Action + " 失败: " + strings.TrimSpace(string(out)),
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "已对 " + unit + " 执行 " + req.Action,
	})
}