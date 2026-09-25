package api

import (
	"encoding/json"
	"net/http"
	"time"

	"eyvescloud/internal/config"
)

// agent_terminal.go 提供 agent 端的 WebSSH/VNC 票据发行端点（node token 认证）。
//
// 跨节点控制台链路：
//
//	浏览器 ──WS── 主控 /api/ssh ──WS── agent /api/ssh（本机容器）
//
// 主控先通过 /api/agent/{ssh,vnc}-ticket（node token）拿到 agent 本地票据，
// 再以 WebSocket 客户端身份拨 agent 的 /api/ssh|/api/vnc。浏览器侧流程
// （主控票据 + subprotocol）完全不变，前端无需改动。
//
// 安全模型：agent 票据端点仅接受持有有效节点 token 的主控调用；发行的票据
// 不绑定 ClientIP/UserAgent（主控代理拨号时二者与发行请求不一致），一次性、
// 60 秒过期，与主控本机票据同等强度。

// HandleAgentSSHTicket 发行 agent 本机的 WebSSH 一次性票据（供主控级联拨号）。
func HandleAgentSSHTicket(w http.ResponseWriter, r *http.Request) {
	handleAgentTerminalTicket(w, r, "ssh")
}

// HandleAgentVNCTicket 发行 agent 本机的 VNC 一次性票据（供主控级联拨号）。
func HandleAgentVNCTicket(w http.ResponseWriter, r *http.Request) {
	handleAgentTerminalTicket(w, r, "vnc")
}

func handleAgentTerminalTicket(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		ContainerName string `json:"container_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Container name required"})
		return
	}
	c := config.FindContainerByName(req.ContainerName)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if c.Suspended {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "容器已挂起，控制台访问被暂停"})
		return
	}
	if c.Status != "running" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "container is not running"})
		return
	}
	if kind == "vnc" && !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "VNC console is only available for KVM VMs"})
		return
	}

	ticket := randomHex(32)
	expires := time.Now().Add(60 * time.Second)
	if kind == "ssh" {
		webSSHTickets.Lock()
		cleanupExpiredWebSSHTicketsLocked(time.Now())
		// ClientIP/UserAgent 留空：级联拨号来自主控，无法绑定浏览器指纹。
		// consumeWebSSHTicket 对空绑定字段跳过校验（见 ssh.go）。
		webSSHTickets.items[ticket] = webSSHTicket{
			ContainerName: c.Name,
			ExpiresAt:     expires,
		}
		webSSHTickets.Unlock()
	} else {
		webVNCTickets.Lock()
		cleanupExpiredWebVNCTicketsLocked(time.Now())
		webVNCTickets.items[ticket] = webVNCTicket{
			ContainerName: c.Name,
			ContainerUUID: c.UUID,
			ExpiresAt:     expires,
		}
		webVNCTickets.Unlock()
	}

	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]string{"ticket": ticket},
	})
}
