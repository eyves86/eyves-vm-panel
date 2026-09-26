package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// KVM 救援模式（Rescue Mode）。
//
// 进入救援：将 VM 从用户上传/管理的救援 ISO（多为 SystemRescue / live 系统）
// 引导而非系统盘，用于修复启动失败、重置密码、恢复数据等场景。
// 退出救援：恢复系统盘正常引导。仅支持 Linux KVM VM，Windows VM 请走 ISO 挂载。

// HandleContainerRescue 进入 / 退出 KVM 救援模式。
// 请求体：
//   - container_id (int, 必填)：容器 ID
//   - enabled (bool)：true=进入救援，false=退出救援
//   - iso_id (string)：进入救援时要使用的 ISO 目录条目 ID（enabled=true 时必填）
func HandleContainerRescue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		ContainerID int    `json:"container_id"`
		Enabled     bool   `json:"enabled"`
		ISOID       string `json:"iso_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Rescue mode is only supported for KVM VMs"})
		return
	}

	if req.Enabled {
		req.ISOID = strings.TrimSpace(req.ISOID)
		if req.ISOID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "iso_id is required to enter rescue mode"})
			return
		}
		var iso *config.ISOFile
		config.AppConfigMu.RLock()
		for i := range config.AppConfig.ISOFiles {
			if config.AppConfig.ISOFiles[i].ID == req.ISOID {
				v := config.AppConfig.ISOFiles[i]
				iso = &v
				break
			}
		}
		config.AppConfigMu.RUnlock()
		if iso == nil || iso.Path == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "ISO not found"})
			return
		}
		if err := enterRescueByRuntime(req.ContainerID, iso.ID, iso.Path); err != nil {
			auditRequest(r, "container.rescue", c.Name, "进入救援失败: "+err.Error(), false, err.Error())
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "container.rescue", c.Name, "进入救援模式，ISO="+iso.Name, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已进入救援模式"})
		return
	}

	if err := exitRescueByRuntime(req.ContainerID); err != nil {
		auditRequest(r, "container.rescue_exit", c.Name, "退出救援失败: "+err.Error(), false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "container.rescue_exit", c.Name, "退出救援模式，恢复系统盘引导", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已退出救援模式"})
}