package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
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
	// 对接密钥（本面板作为被控时，主控「对接已有面板」表单用它发起注册）。
	PairingKey        string `json:"pairing_key"`
	PairingKeyExpiry string `json:"pairing_key_expiry"`
	PairingKeyValid   bool   `json:"pairing_key_valid"`
}

// agentPairingKeyValid 校验对接密钥（存在 + 未过 24h 有效期）。
func agentPairingKeyValid() (string, bool) {
	config.AppConfigMu.RLock()
	key, expiry := config.AppConfig.AgentPairingKey, config.AppConfig.AgentPairingKeyExpiry
	config.AppConfigMu.RUnlock()
	if key == "" {
		return "", false
	}
	if t, err := time.Parse(time.RFC3339, expiry); err != nil || time.Now().After(t) {
		return "", false
	}
	return key, true
}

// consumeAgentPairingKey 注册成功后焚毁对接密钥（一次性）。
func consumeAgentPairingKey() {
	config.AppConfigMu.Lock()
	config.AppConfig.AgentPairingKey = ""
	config.AppConfig.AgentPairingKeyExpiry = ""
	config.AppConfigMu.Unlock()
	_ = config.SaveConfig()
}

// scheduleAgentRestart 延迟 restartDelay 后重启 eyvescloud-agent 服务：
// 先让 HTTP 响应完整落回客户端，再执行 systemctl。
func scheduleAgentRestart() {
	go func() {
		time.Sleep(2 * time.Second)
		_ = exec.Command("systemctl", "restart", "eyvescloud-agent").Run()
	}()
}

// HandleAgentStatus GET /api/agent/status（管理员）：读取本机 agent.json，
// 在「节点管理 → 节点接入」查看当前接入的主控地址、节点身份与 token；
// 同时返回对接密钥状态（用于主控「对接已有面板」反向拉取本节点）。
func HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	pairingKey, pairingValid := agentPairingKeyValid()
	resp := AgentRegistration{
		PairingKey:      pairingKey,
		PairingKeyValid: pairingValid,
	}
	if pairingValid {
		resp.PairingKeyExpiry = config.AppConfig.AgentPairingKeyExpiry
	}
	ac := config.LoadAgentConfig()
	if ac != nil {
		resp.Registered = true
		resp.Controller = ac.Controller
		resp.NodeID = ac.NodeID
		resp.Token = ac.Token
		resp.Name = ac.Name
		resp.Address = ac.Address
		resp.AllowInsecureHTTP = ac.AllowInsecureHTTP
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ok", Data: resp})
}

// HandleAgentPairingKey POST /api/agent/pairing-key（管理员）：生成/重置对接密钥。
// 一次性、24h 有效；填到目标主控「节点管理 → 对接已有面板」表单即可发起对接。
func HandleAgentPairingKey(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "生成密钥失败"})
		return
	}
	key := hex.EncodeToString(buf)
	expiry := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	config.AppConfigMu.Lock()
	config.AppConfig.AgentPairingKey = key
	config.AppConfig.AgentPairingKeyExpiry = expiry
	config.AppConfigMu.Unlock()
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "保存失败: " + err.Error()})
		return
	}
	auditRequest(r, "agent.pairing_key", "", "生成对接密钥（24h 有效，一次性）", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "对接密钥已生成（24h 内有效，使用一次后作废）", Data: map[string]string{
		"pairing_key": key,
		"expiry":      expiry,
	}})
}

// HandleAgentRegister POST /api/agent/register：接入/切换主控。
// 两条认证路径：
//  1. 面板管理员会话（「节点管理 → 节点接入」表单，前端随后调用 /api/agent/restart）；
//  2. 对接密钥（主控「对接已有面板」服务端调用，凭 pairing_key 认证——注册成功
//     即焚毁密钥并自动重启本机 agent 服务）。
func HandleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Controller        string `json:"controller"`
		InstallKey        string `json:"install_key"`
		PairingKey        string `json:"pairing_key"`
		Name              string `json:"name"`
		Address           string `json:"address"`
		AllowInsecureHTTP bool   `json:"allow_insecure_http"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	// 认证：管理员会话，或有效对接密钥（主控服务端调用路径）。
	ctx, hasAuth := authContextFromRequest(r)
	isAdmin := hasAuth && ctx.Type == authTypeAdmin
	viaPairingKey := false
	if !isAdmin {
		expected, ok := agentPairingKeyValid()
		if !ok || strings.TrimSpace(req.PairingKey) != expected {
			auditRequest(r, "agent.register", "", "对接密钥无效或已过期", false, "")
			jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "对接密钥无效或已过期"})
			return
		}
		viaPairingKey = true
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
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "主控使用明文 http 将导致节点 token 明文传输；请改用 https://，或显式允许明文 http"})
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
	if viaPairingKey {
		// 主控服务端调用路径：密钥即焚 + 自动重启（面板会话路径由前端调 /api/agent/restart）。
		consumeAgentPairingKey()
		scheduleAgentRestart()
	}
	auditRequest(r, "agent.register", name, "controller "+controller, true, "registered/switched")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "已接入主控 " + controller, Data: map[string]bool{"restart_required": true}})
}

// HandleAgentRestart POST /api/agent/restart（管理员）：重启 eyvescloud-agent
// systemd 服务（切换主控后生效）。先回复、延迟执行 systemctl，
// 避免响应被自身重启打断；无 systemctl 的环境返回手动重启指引。
func HandleAgentRestart(w http.ResponseWriter, r *http.Request) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "未找到 systemctl；请手动重启 agent 服务（systemctl restart eyvescloud-agent）"})
		return
	}
	scheduleAgentRestart()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "重启指令已下发，服务约 3-5 秒后恢复", Data: map[string]bool{"restart_initiated": true}})
}
