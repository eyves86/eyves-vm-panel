package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

// AgentRegistration 面板「节点接入」展示/提交的被控注册信息。
type AgentRegistration struct {
	Registered        bool   `json:"registered"`
	Controller        string `json:"controller"`
	NodeID            string `json:"node_id"`
	Token             string `json:"token"`
	Name              string `json:"name"`
	Address           string `json:"address"`
	AllowInsecureHTTP bool   `json:"allow_insecure_http"`
}

// HandleAgentStatus GET /api/agent/status（管理员）：读取本机 agent.json，
// 在被控面板上直接查看当前接入的主控地址、节点身份与 token。
// 未注册（主控/独立运行）时 Registered=false。
func HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	ac := config.LoadAgentConfig()
	if ac == nil {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "未接入任何主控", Data: AgentRegistration{}})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ok", Data: AgentRegistration{
		Registered:        true,
		Controller:        ac.Controller,
		NodeID:            ac.NodeID,
		Token:             ac.Token,
		Name:              ac.Name,
		Address:           ac.Address,
		AllowInsecureHTTP: ac.AllowInsecureHTTP,
	}})
}

// HandleAgentRegister POST /api/agent/register（管理员）：接入/切换主控。
// 携带新主控地址与 install key 调用其 /api/nodes/register，成功后落盘
// agent.json。运行中的 agent 仍持有旧配置，需重启服务生效（配合
// /api/agent/restart，切换完成后前端自动触发并轮询状态）。
func HandleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Controller        string `json:"controller"`
		InstallKey        string `json:"install_key"`
		Name              string `json:"name"`
		Address           string `json:"address"`
		AllowInsecureHTTP bool   `json:"allow_insecure_http"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	controller := strings.TrimSuffix(strings.TrimSpace(req.Controller), "/")
	lower := strings.ToLower(controller)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "主控地址需以 http:// 或 https:// 开头"})
		return
	}
	installKey := strings.TrimSpace(req.InstallKey)
	if installKey == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "install_key required"})
		return
	}
	isHTTP := strings.HasPrefix(lower, "http://")
	if isHTTP && !req.AllowInsecureHTTP {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "主控使用明文 http 将导致节点 token 明文传输；请改用 https://，或勾选「允许明文 http（不安全）」"})
		return
	}

	// 名称默认沿用当前节点名（未注册过则用主机名），地址默认沿用已保存地址。
	existing := config.LoadAgentConfig()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if existing != nil && existing.Name != "" {
			name = existing.Name
		} else if host, err := os.Hostname(); err == nil && host != "" {
			name = host
		}
	}
	address := strings.TrimSpace(req.Address)
	if address == "" && existing != nil {
		address = existing.Address
	}

	payload, _ := json.Marshal(map[string]string{
		"install_key": installKey,
		"name":        name,
		"address":     address,
		"version":     version.Current(),
	})
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(controller+"/api/nodes/register", "application/json", bytes.NewReader(payload))
	if err != nil {
		auditRequest(r, "agent.register", name, "controller "+controller, false, err.Error())
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "连接主控失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			NodeID string `json:"node_id"`
			Token  string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "解析主控响应失败: " + err.Error()})
		return
	}
	if !out.Success || out.Data.NodeID == "" || out.Data.Token == "" {
		auditRequest(r, "agent.register", name, "controller "+controller, false, out.Message)
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "注册失败: " + out.Message})
		return
	}
	if err := config.SaveAgentConfig(&config.AgentConfig{
		Controller:        controller,
		NodeID:            out.Data.NodeID,
		Token:             out.Data.Token,
		Name:              name,
		Address:           address,
		AllowInsecureHTTP: isHTTP,
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "保存 agent.json 失败: " + err.Error()})
		return
	}
	auditRequest(r, "agent.register", name, "controller "+controller, true, "registered/switched")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已接入主控 " + controller + "，重启 agent 服务后生效", Data: map[string]bool{"restart_required": true}})
}

// HandleAgentRestart POST /api/agent/restart（管理员）：重启 eyvescloud-agent
// systemd 服务（切换主控后生效）。先回复、延迟 1 秒再执行 systemctl，
// 避免响应被自身重启打断；无 systemctl 的环境返回手动重启指引。
func HandleAgentRestart(w http.ResponseWriter, r *http.Request) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "未找到 systemctl；请手动重启 agent 服务（systemctl restart eyvescloud-agent）"})
		return
	}
	go func() {
		time.Sleep(1 * time.Second)
		_ = exec.Command("systemctl", "restart", "eyvescloud-agent").Run()
	}()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "重启指令已下发，服务约 3-5 秒后恢复", Data: map[string]bool{"restart_initiated": true}})
}
