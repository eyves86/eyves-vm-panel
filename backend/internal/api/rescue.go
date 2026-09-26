package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// KVM 救援模式（Rescue Mode）& ISO 挂载。
//
// 救援：将 VM 从指定 ISO 引导而非系统盘，用于修复启动失败、重置密码、恢复数据。
// 挂载 ISO：向 VM 加一个 CD-ROM 设备（sdb），不改变启动盘顺序，Windows VM 可单独使用。
// 两条路径均仅支持 KVM；LXC / Linux KVM 推荐救援模式。

// HandleContainerRescue 进入 / 退出 KVM 救援模式（顶层路由 /api/containers/rescue，向后兼容）。
// 请求体：
//   - container_id (int, 必填)
//   - enabled (bool)：true=进入救援，false=退出救援
//   - iso_id (string)：进入救援时必填
//
// 多节点：先把 body 完整缓存，转发给 agent 的 /api/agent/containers/{id}/rescue，
// 再决定是否执行本地逻辑。agent 不可达 fallback 到本机。
func HandleContainerRescue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Failed to read body"})
		return
	}
	var req struct {
		ContainerID int    `json:"container_id"`
		Enabled     bool   `json:"enabled"`
		ISOID       string `json:"iso_id"`
	}
	if err := json.NewDecoder(bytes.NewReader(buf)).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	if req.ContainerID <= 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container_id is required"})
		return
	}
	c := config.FindContainer(req.ContainerID)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	// 多节点转发：必须用缓存的 buf（不能再读 r.Body，它已经被 io.ReadAll 消耗完了）。
	if c.NodeID != "" {
		if node, ok := config.FindNode(c.NodeID); ok && node.Address != "" {
			data, status, pErr := proxyNodeRequest(r, node, http.MethodPost,
				fmt.Sprintf("/api/agent/containers/%d/rescue", c.ID),
				bytes.NewReader(buf))
			if pErr == nil && status < 500 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(data)
				return
			}
			// agent 不可达/5xx → 本地 fallback
		}
	}
	doRescue(w, r, c, req.Enabled, req.ISOID)
}

// doRescue 实际进入/退出救援模式的共享实现（给路径参数 handler 和 agent 端 handler 复用）。
func doRescue(w http.ResponseWriter, r *http.Request, c *config.Container, enabled bool, isoID string) {
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Rescue mode is only supported for KVM VMs"})
		return
	}
	if enabled {
		isoID = strings.TrimSpace(isoID)
		if isoID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "iso_id is required to enter rescue mode"})
			return
		}
		iso := findISOByID(isoID)
		if iso == nil || iso.Path == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "ISO not found"})
			return
		}
		if err := enterRescueByRuntime(c.ID, iso.ID, iso.Path); err != nil {
			auditRequest(r, "container.rescue", c.Name, "进入救援失败: "+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "container.rescue", c.Name, "进入救援模式，ISO="+iso.Name, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已进入救援模式"})
		return
	}
	if err := exitRescueByRuntime(c.ID); err != nil {
		auditRequest(r, "container.rescue_exit", c.Name, "退出救援失败: "+err.Error(), false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "container.rescue_exit", c.Name, "退出救援模式，恢复系统盘引导", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已退出救援模式"})
}

// handleContainerRescuePost 容器级路径 handler（POST /api/containers/{id}/rescue）。
// 请求体与顶层路由一致：{"enabled": true/false, "iso_id": "..."}。
// routeToAgent 在 handlers.go 入口已经调过；agent 端也走同一个 doRescue。
func handleContainerRescuePost(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if routeToAgent(w, r, c, "rescue") {
		return
	}
	var req struct {
		Enabled bool   `json:"enabled"`
		ISOID   string `json:"iso_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	doRescue(w, r, c, req.Enabled, req.ISOID)
}

// handleContainerISOActionPost 容器级路径 handler（POST /api/containers/{id}/iso）。
// 请求体：{"iso_id": "...", "attach": true/false}
// 多节点：主控把请求转发到容器所属 agent；agent 端收到后直接调用 runtime 的 attach/detach。
func handleContainerISOActionPost(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if routeToAgent(w, r, c, "iso") {
		return
	}
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "ISO attach is only supported for KVM VMs"})
		return
	}
	var req struct {
		ISOID  string `json:"iso_id"`
		Attach bool   `json:"attach"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	if req.Attach {
		req.ISOID = strings.TrimSpace(req.ISOID)
		if req.ISOID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "iso_id is required when attach=true"})
			return
		}
		iso := findISOByID(req.ISOID)
		if iso == nil || iso.Path == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "ISO not found"})
			return
		}
		if err := attachISOByRuntime(c.ID, iso.Path); err != nil {
			auditRequest(r, "container.iso_attach", c.Name, "挂载 ISO "+iso.Name+" 失败: "+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "container.iso_attach", c.Name, "挂载 ISO "+iso.Name, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ISO attached"})
		return
	}
	// detach
	if err := detachISOByRuntime(c.ID); err != nil {
		auditRequest(r, "container.iso_detach", c.Name, "卸载 ISO 失败: "+err.Error(), false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "container.iso_detach", c.Name, "卸载 ISO", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ISO detached"})
}

// findISOByID 从全局配置里查 ISO 条目（主控节点有完整的 ISOFiles 列表）。
// agent 节点通常也会同步 ISOFiles 列表（通过配置下发或共享存储），所以
// agent 端调用这个函数能拿到 iso.Path 用于 virsh attach-disk。
func findISOByID(id string) *config.ISOFile {
	if id == "" {
		return nil
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i := range config.AppConfig.ISOFiles {
		if config.AppConfig.ISOFiles[i].ID == id {
			v := config.AppConfig.ISOFiles[i]
			return &v
		}
	}
	return nil
}

// 抑制未使用 fmt 告警（预留参数）
var _ = fmt.Sprintf