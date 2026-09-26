package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"eyvescloud/internal/config"
)

// hvmSettingsResponse KVM HVM 设置视图，对齐 Virtualizor act=hvmsettings。
//
// 字段含义：
//   - BootOrder：启动盘顺序，"cda"=从第一硬盘、"dca"=从光驱、"cd"=光盘优先
//   - NicDriver：网卡驱动，virtio/e1000/rtl8139
//   - VNCKeyMap：noVNC 键位，en-us/zh-cn/de 等
//   - EnableTuntap：TUN/TAP（VPN 客户端需要）
//   - EnablePPP：PPP 拨号（少数 L2TP 场景）
//   - Acceleration：KVM 硬件加速开关（host-passthrough / default / off）
type hvmSettingsResponse struct {
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name"`
	BootOrder     string `json:"boot_order"`
	NicDriver     string `json:"nic_driver"`
	VNCKeyMap     string `json:"vnc_keymap"`
	EnableTuntap  bool   `json:"enable_tuntap"`
	EnablePPP     bool   `json:"enable_ppp"`
	Acceleration  string `json:"acceleration"`
	AvailableKeyMaps []string `json:"available_keymaps"`
	AvailableNicDrivers []string `json:"available_nic_drivers"`
}

// handleContainerHVMSettingsGet 读取 HVM 设置（GET /api/containers/{id}/hvm-settings）。
func handleContainerHVMSettingsGet(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	if routeToAgent(w, r, c, "hvm-settings") {
		return
	}
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "HVM 设置仅适用于 KVM 容器",
		})
		return
	}
	// 从容器元数据读（如果有持久化字段）
	resp := hvmSettingsResponse{
		ContainerID:   c.ID,
		ContainerName: c.Name,
		BootOrder:     stringOrDefault(c.HVMBootOrder, "cda"),
		NicDriver:     stringOrDefault(c.HVMNicDriver, "virtio"),
		VNCKeyMap:     stringOrDefault(c.HVMVNCKeyMap, "en-us"),
		EnableTuntap:  c.HVMEnableTuntap,
		EnablePPP:     c.HVMEnablePPP,
		Acceleration:  stringOrDefault(c.HVMAcceleration, "default"),
		AvailableKeyMaps: []string{
			"en-us", "zh-cn", "de", "fr", "ja", "ko", "ru", "es",
		},
		AvailableNicDrivers: []string{
			"virtio", "e1000", "rtl8139", "ne2k_pci", "vmxnet3",
		},
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

func stringOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// hvmSettingsRequest HVM 设置写入请求体（PUT /api/containers/{id}/hvm-settings）。
type hvmSettingsRequest struct {
	BootOrder    *string `json:"boot_order,omitempty"`
	NicDriver    *string `json:"nic_driver,omitempty"`
	VNCKeyMap    *string `json:"vnc_keymap,omitempty"`
	EnableTuntap *bool   `json:"enable_tuntap,omitempty"`
	EnablePPP    *bool   `json:"enable_ppp,omitempty"`
	Acceleration *string `json:"acceleration,omitempty"`
}

// handleContainerHVMSettingsPut 更新 HVM 设置（PUT /api/containers/{id}/hvm-settings）。
// 对齐 Virtualizor act=hvmsettings POST。
//
// 权限：container:power scope。
//
// 安全：
//   - BootOrder 白名单 [cda dca cd]
//   - NicDriver 白名单 [virtio e1000 rtl8139 ne2k_pci vmxnet3]
//   - VNCKeyMap 白名单（与 GET AvailableKeyMaps 同步）
//   - Acceleration 白名单 [default host-passthrough off]
//
// 实现要点：在 MutateGlobal 内一次性修改 cfg.Containers，避免先写指针再持久化的
// "半提交" 状态（持久化失败时内存已被脏改，下次 list 看到错值）。
func handleContainerHVMSettingsPut(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !requireScope(w, r, "container:power") {
		return
	}
	if routeToAgent(w, r, c, "hvm-settings") {
		return
	}
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false, Message: "HVM 设置仅适用于 KVM 容器",
		})
		return
	}
	var req hvmSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	bootWhitelist := map[string]bool{"cda": true, "dca": true, "cd": true}
	nicWhitelist := map[string]bool{
		"virtio": true, "e1000": true, "rtl8139": true,
		"ne2k_pci": true, "vmxnet3": true,
	}
	keymapWhitelist := map[string]bool{
		"en-us": true, "zh-cn": true, "de": true, "fr": true,
		"ja": true, "ko": true, "ru": true, "es": true,
	}
	accelWhitelist := map[string]bool{"default": true, "host-passthrough": true, "off": true}

	if req.BootOrder != nil {
		if !bootWhitelist[*req.BootOrder] {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false, Message: "boot_order 仅支持 cda/dca/cd",
			})
			return
		}
	}
	if req.NicDriver != nil {
		if !nicWhitelist[*req.NicDriver] {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false, Message: "nic_driver 不在白名单",
			})
			return
		}
	}
	if req.VNCKeyMap != nil {
		if !keymapWhitelist[*req.VNCKeyMap] {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false, Message: "vnc_keymap 不在白名单",
			})
			return
		}
	}
	if req.Acceleration != nil {
		if !accelWhitelist[*req.Acceleration] {
			jsonResponse(w, http.StatusBadRequest, APIResponse{
				Success: false, Message: "acceleration 不在白名单",
			})
			return
		}
	}
	var mutated bool
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].ID != c.ID {
				continue
			}
			if req.BootOrder != nil {
				cfg.Containers[i].HVMBootOrder = *req.BootOrder
			}
			if req.NicDriver != nil {
				cfg.Containers[i].HVMNicDriver = *req.NicDriver
			}
			if req.VNCKeyMap != nil {
				cfg.Containers[i].HVMVNCKeyMap = *req.VNCKeyMap
			}
			if req.Acceleration != nil {
				cfg.Containers[i].HVMAcceleration = *req.Acceleration
			}
			if req.EnableTuntap != nil {
				cfg.Containers[i].HVMEnableTuntap = *req.EnableTuntap
			}
			if req.EnablePPP != nil {
				cfg.Containers[i].HVMEnablePPP = *req.EnablePPP
			}
			mutated = true
			return
		}
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{
			Success: false, Message: "持久化失败: " + err.Error(),
		})
		return
	}
	if !mutated {
		jsonResponse(w, http.StatusNotFound, APIResponse{
			Success: false, Message: "容器在配置中找不到",
		})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "HVM 设置已更新（下次启动时生效）",
	})
	auditRequest(r, "container.hvm_settings", strconv.Itoa(c.ID),
		"HVM settings updated", true, "")
}