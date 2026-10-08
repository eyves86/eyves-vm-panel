package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/notify"
)

// 主控（Controller）节点管理 API。
// 被控节点通过一键安装脚本注册到主控，此后主控可以直接查看/操作被控的容器。

func randomNodeSecret(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func newNodeID() string {
	return "node-" + randomNodeSecret(6)
}

// HandleNodes 列出或创建被控节点。
func HandleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		reconcileNodeOnlineStatuses()
		config.AppConfigMu.RLock()
		nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
		config.AppConfigMu.RUnlock()
		// 脱敏下发：列表绝不携带 agent Token / InstallKey（密钥最小化）。
		safe := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			safe = append(safe, sanitizeNode(n))
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: safe})
	case http.MethodPost:
		if !requireScope(w, r, "node:write") {
			return
		}
		createNode(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleNodeSubRoutes 分发 /api/nodes/{id}[/...] 的各类子操作。
func HandleNodeSubRoutes(w http.ResponseWriter, r *http.Request) {
	nodeID, rest, ok := splitNodeSubPath(r.URL.Path)
	if !ok || nodeID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Node ID required"})
		return
	}
	switch {
	case rest == "" && nodeID == "register" && r.Method == http.MethodPost:
		handleNodeRegister(w, r)
	case rest == "" && nodeID == "adopt" && r.Method == http.MethodPost:
		// 对接已有面板：管理员提供被控面板地址 + 对接密钥，主控主动拉取注册。
		AdminMiddleware(handleNodeAdopt)(w, r)
	case rest == "":
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeItem(w, r, nodeID) })(w, r)
	case rest == "heartbeat":
		handleNodeHeartbeat(w, r, nodeID)
	case rest == "maintenance" && r.Method == http.MethodPost:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeMaintenance(w, r, nodeID) })(w, r)
	case rest == "drain" && r.Method == http.MethodGet:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeDrain(w, r, nodeID) })(w, r)
	case rest == "install-script":
		// 注意：**不要**套 AdminMiddleware —— 外部服务器 curl 拉脚本时只有 X-Install-Key，
		// 没有管理员 token。但 handler 的 scope 回退路径（node:write）依赖认证上下文，
		// 因此套 OptionalAuthMiddleware：有效凭据注入上下文，匿名放行由 handler 的
		// X-Install-Key 分支自认证。
		OptionalAuthMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeInstallScript(w, r, nodeID) })(w, r)
	case rest == "install-command":
		// 一行安装命令（curl | sudo bash）：面板只展示命令，不展示脚本正文。
		// 仅管理员可见（在面板内），所以保持 AdminMiddleware。
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeInstallCommand(w, r, nodeID) })(w, r)
	case rest == "install-script/sha256":
		// 同上，外部离线审计/Hash 校验用，handler 内部自己验 X-Install-Key。
		OptionalAuthMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeInstallScriptSHA256(w, r, nodeID) })(w, r)
	case rest == "containers" && r.Method == http.MethodGet:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeContainers(w, r, nodeID) })(w, r)
	case rest == "containers" && r.Method == http.MethodPost:
		// 主控在被控节点开通（发机）新容器：透传到 agent 的 create 接口。
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
			node, ok := config.FindNode(nodeID)
			if !ok {
				jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
				return
			}
			if node.Status != "" && node.Status != "online" {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "节点不在线，无法发机"})
				return
			}
			if node.MaintenanceMode {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "节点处于维护模式，禁止发机；请先关闭维护模式或选择其他节点"})
				return
			}
			if node.Address == "" {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
				return
			}
			data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/containers/create", r.Body)
			if err != nil {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理发机失败: " + err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(data)
		})(w, r)
	case strings.HasPrefix(rest, "containers/") && r.Method == http.MethodPost:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
			handleNodeContainerAction(w, r, nodeID, strings.TrimPrefix(rest, "containers/"))
		})(w, r)
	case rest == "images" && r.Method == http.MethodGet:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
			handleNodeImageProxy(w, r, nodeID, http.MethodGet, "/api/agent/images", nil)
		})(w, r)
	case rest == "images/sync" && r.Method == http.MethodPost:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeImageSyncProxy(w, r, nodeID) })(w, r)
	case rest == "backup" && r.Method == http.MethodPost:
		// 节点级冷备份：主控触发被控对全部容器做完整备份（重装/重建前保全）。
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
			node, ok := config.FindNode(nodeID)
			if !ok {
				jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
				return
			}
			if node.Status != "" && node.Status != "online" {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "节点不在线，无法冷备份"})
				return
			}
			if node.Address == "" {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
				return
			}
			data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/node-backup", r.Body)
			if err != nil {
				jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "冷备份失败: " + err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(data)
		})(w, r)
	default:
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Action not found"})
	}
}

// handleNodeImageProxy 代理主控到被控的镜像清单查询 / 同步请求。
func handleNodeImageProxy(w http.ResponseWriter, r *http.Request, nodeID, method, path string, body io.Reader) {
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	if node.Address == "" {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
		return
	}
	data, status, err := proxyNodeRequest(r, node, method, path, body)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理请求被控节点失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// handleNodeImageSyncProxy 把主控自身的自定义镜像清单下发给被控，触发被控镜像同步。
func handleNodeImageSyncProxy(w http.ResponseWriter, r *http.Request, nodeID string) {
	catalog := agentImageCatalog{
		LXC: config.ListCustomLXCImages(),
		KVM: config.ListCustomKVMImages(),
	}
	body, err := json.Marshal(catalog)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to build image catalog"})
		return
	}
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	if node.Address == "" {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
		return
	}
	data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/images/sync", bytes.NewReader(body))
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理请求被控节点失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// handleNodeRegister 由被控 agent 首次接入时调用，使用安装密钥换取节点 token。
func handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstallKey string `json:"install_key"`
		Name       string `json:"name"`
		Address    string `json:"address"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if strings.TrimSpace(req.InstallKey) == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "install_key required"})
		return
	}
	// F3：install_key 是一次性凭据，消费事件必须全程留痕。key 只记指纹前
	// 8 位（完整值由 auth.go 的脱敏正则兜底），来源 IP/UA 由 auditRequest 记录。
	keyFingerprint := func(key string) string {
		if len(key) > 8 {
			return key[:8] + "..."
		}
		return key
	}
	node, ok := config.FindNodeByInstallKey(strings.TrimSpace(req.InstallKey))
	if !ok || node.Token == "" {
		auditRequest(r, "node.register", "unknown", "install_key 指纹 "+keyFingerprint(strings.TrimSpace(req.InstallKey)), false, "invalid install key")
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid install key"})
		return
	}
	if node.InstallKeyCreatedAt != "" {
		createdAt, err := time.Parse(time.RFC3339, node.InstallKeyCreatedAt)
		if err == nil && time.Since(createdAt) > 24*time.Hour {
			auditRequest(r, "node.register", node.Name, "install_key 指纹 "+keyFingerprint(req.InstallKey), false, "install key expired")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "install key expired"})
			return
		}
	}
	if node.InstallKeyIP != "" {
		reqIP := clientIP(r)
		if reqIP != node.InstallKeyIP && !sameIPv4Prefix24(reqIP, node.InstallKeyIP) {
			auditRequest(r, "node.register", node.Name, "install_key 指纹 "+keyFingerprint(req.InstallKey), false, "install key IP mismatch")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "install key IP mismatch"})
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = node.Name
	}
	address := normalizeNodeAddress(req.Address)
	if address != "" {
		// agent 自报地址（已持一次性 install_key，属部署信任链）：链路本地
		// 仍硬拒（元数据防泄漏），环回/私网放行——LXC/NAT 环境下 agent
		// 自报内网地址是常态，若照搬管理员录入的严格策略会阻断一键注册。
		if err := validateNodeAddress(address, true); err != nil {
			auditRequest(r, "node.register", name, "install_key 指纹 "+keyFingerprint(req.InstallKey), false, err.Error())
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	_, ok = config.UpdateNode(node.ID, func(n *config.Node) {
		n.Status = "online"
		n.LastSeen = time.Now().Format("2006-01-02 15:04:05")
		n.Name = name
		n.Version = req.Version
		// install_key 是一次性 token：注册成功后立即清空，防止重复注册或被已注册节点重放。
		n.InstallKey = ""
		n.InstallKeyCreatedAt = ""
		if address != "" {
			n.Address = address
		}
	})
	if !ok {
		auditRequest(r, "node.register", name, "install_key 指纹 "+keyFingerprint(req.InstallKey), false, "node update failed")
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	auditRequest(r, "node.register", name, "节点注册成功（install_key 已消费并清空）", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"node_id": node.ID,
		"token":   node.Token,
		"name":    name,
	}})
}

func sameIPv4Prefix24(a, b string) bool {
	ipa := net.ParseIP(a)
	ipb := net.ParseIP(b)
	if ipa == nil || ipb == nil {
		return false
	}
	ipa = ipa.To4()
	ipb = ipb.To4()
	if ipa == nil || ipb == nil {
		return false
	}
	return ipa[0] == ipb[0] && ipa[1] == ipb[1] && ipa[2] == ipb[2]
}

func createNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		// BindIP 可选：把一次性 install_key 绑定到「被控节点出口 IP」，
		// 注册时校验来源 IP 一致（同 /24 前缀亦通过）。留空 = 不绑 IP，
		// 仅保留一次性 + 24h TTL 防护（适合被控出口 IP 未知/多线的场景）。
		BindIP string `json:"bind_ip"`
		// Mode 添加模式：quick=一键添加（默认，生成一行安装命令走 register 流程）；
		// manual=手动添加（被控机手工部署 agent，面板下发 agent.json 预置配置，免 install_key）。
		Mode string `json:"mode"`
		// TLSSkipVerify 允许主控→被控跳过 TLS 证书校验（被控自签证书场景），落审计。
		TLSSkipVerify bool `json:"tls_skip_verify"`
		// AllowPrivate 显式豁免 SSRF 环回/私网拦截（内网部署场景），落审计；
		// 链路本地（169.254.0.0/16 含云元数据、fe80::/10）无豁免永远拒绝。
		AllowPrivate bool `json:"allow_private"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "quick"
	}
	if mode != "quick" && mode != "manual" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "mode 必须为 quick 或 manual"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "node-" + randomNodeSecret(4)
	}
	// 手动添加：地址必填（主控要主动连被控 agent API）；一键添加：可选（通常由 agent 注册时自报）。
	address := normalizeNodeAddress(req.Address)
	if address != "" || mode == "manual" {
		if address == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "手动添加需要填写被控面板地址"})
			return
		}
		// 管理员录入路径执行完整 SSRF 检查：链路本地硬拒 + 环回/私网默认拒（可显式豁免）。
		if err := validateNodeAddress(address, req.AllowPrivate); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	// IP 绑定只接受显式指定的被控出口 IP；不再默认绑定「发起请求的管理员 IP」——
	// 管理员与被控机通常不在同一网段，默认绑定会导致 agent 注册必然 IP mismatch。
	// 手动添加不走 install_key 注册流程，绑定无意义，强制忽略。
	bindIP := ""
	if mode == "quick" {
		if raw := strings.TrimSpace(req.BindIP); raw != "" {
			ip := net.ParseIP(raw)
			if ip == nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的绑定 IP"})
				return
			}
			bindIP = ip.String()
		}
	}
	// 手动模式不生成 install_key（无 register 流程，agent.json 预置配置直接启动）；
	// 后续若想改走一键安装，install-command/install-script 端点会按需换发新 key。
	installKey := ""
	if mode == "quick" {
		installKey = randomNodeSecret(32)
	}
	node := config.Node{
		ID:                  newNodeID(),
		Name:                name,
		Address:             address,
		Token:               randomNodeSecret(32),
		InstallKey:          installKey,
		InstallKeyCreatedAt: time.Now().UTC().Format(time.RFC3339),
		InstallKeyIP:        bindIP,
		Status:              "pending",
		CreatedAt:           time.Now().Format("2006-01-02 15:04:05"),
		TLSSkipVerify:       req.TLSSkipVerify,
		AllowPrivateAddr:    req.AllowPrivate,
	}
	if err := config.AddNode(node); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditDetail := fmt.Sprintf("创建被控节点（模式 %s，install_key 绑定 IP: %s，TLS 校验: %s，内网豁免: %s）",
		mode, bindIPOrNone(bindIP), tlsVerifyLabel(req.TLSSkipVerify), boolLabel(req.AllowPrivate))
	auditRequest(r, "node.create", node.Name, auditDetail, true, "")

	// 手动添加：下发 agent.json 预置配置。管理员把它写到被控机 <data_dir>/agent.json
	// 后直接运行 eyvescloud agent，无需 install_key/register（agent.go needRegister=false 路径）。
	if mode == "manual" {
		// controller 地址优先取「面板绑定域名」，反代/多入口下仍指向正确对外地址。
		controllerURL := externalBaseURL(r)
		agentConfigJSON, _ := json.MarshalIndent(map[string]any{
			"controller": controllerURL,
			"node_id":    node.ID,
			"token":      node.Token,
			"name":       node.Name,
			"address":    address,
		}, "", "  ")
		// 响应不下发 install_key（手动模式无）；agent Token 仅此一次明文展示，
		// 落库即 AES-GCM 密文（store_sqlite.go F7/P2-11）。
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: map[string]any{
			"node": sanitizeNode(node),
			"manual_bootstrap": map[string]string{
				"controller_url": controllerURL,
				"node_id":        node.ID,
				"token":          node.Token,
				"agent_config":   string(agentConfigJSON),
				"deploy_hint":    "将被控程序部署到被控服务器后，把 agent_config 内容写入其数据目录 agent.json，运行 eyvescloud agent 即可接入（无需安装密钥）。",
			},
		}})
		return
	}
	// 一键添加：响应不下发 agent Token（密钥最小化）：仅返回节点信息 + 一次性 install_key
	// 供面板拼接一行安装命令；key 注册成功即焚，TTL 24h。
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: map[string]any{
		"node":              sanitizeNode(node),
		"install_key":       node.InstallKey,
		"install_key_ttl":   "24h",
		"install_key_bound": bindIP,
	}})
}

// handleNodeAdopt 对接已有面板（POST /api/nodes/adopt）：适用于被控机上已用通用
// 安装脚本装好独立面板的场景。管理员提供被控面板地址 + 该面板「节点接入」生成的
// 对接密钥；主控创建节点后凭密钥调用被控 /api/agent/register，被控保存 agent.json
// 并自动重启 agent 服务、回连主控完成注册（与 install key 的方向相反：密钥由被控签发）。
func handleNodeAdopt(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "node:write") {
		return
	}
	var req struct {
		PanelURL     string `json:"panel_url"`
		PairingKey   string `json:"pairing_key"`
		Name         string `json:"name"`
		AllowPrivate bool   `json:"allow_private"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	panelURL := normalizeNodeAddress(req.PanelURL)
	if panelURL == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "请填写被控面板地址（http://IP:端口）"})
		return
	}
	// 主控会主动连接被控面板（对接调用），执行与手动添加一致的 SSRF 检查。
	if err := validateNodeAddress(panelURL, req.AllowPrivate); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	pairingKey := strings.TrimSpace(req.PairingKey)
	if pairingKey == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "请填写被控面板生成的对接密钥（其「节点管理 → 节点接入」中生成）"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "node-" + randomNodeSecret(4)
	}
	node := config.Node{
		ID:                  newNodeID(),
		Name:                name,
		Address:             panelURL,
		Token:               randomNodeSecret(32),
		InstallKey:          randomNodeSecret(32),
		InstallKeyCreatedAt: time.Now().UTC().Format(time.RFC3339),
		Status:              "pending",
		CreatedAt:           time.Now().Format("2006-01-02 15:04:05"),
		AllowPrivateAddr:    req.AllowPrivate,
	}
	if err := config.AddNode(node); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "node.adopt", node.Name, "对接已有面板 "+panelURL+"（内网豁免: "+boolLabel(req.AllowPrivate)+"）", true, "")

	// 主控对外地址（优先面板绑定域名）；本主控走明文 http 时告知被控豁免（与安装命令一致）。
	controllerURL := externalBaseURL(r)
	allowInsecure := strings.HasPrefix(strings.ToLower(controllerURL), "http://")
	payload, _ := json.Marshal(map[string]any{
		"controller":          controllerURL,
		"install_key":         node.InstallKey,
		"pairing_key":         pairingKey,
		"name":                name,
		"address":             panelURL,
		"allow_insecure_http": allowInsecure,
	})
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(strings.TrimSuffix(panelURL, "/")+"/api/agent/register", "application/json", bytes.NewReader(payload))
	if err != nil {
		config.RemoveNode(node.ID)
		auditRequest(r, "node.adopt", node.Name, "对接调用失败", false, err.Error())
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "连接被控面板失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		config.RemoveNode(node.ID)
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "解析被控面板响应失败: " + err.Error()})
		return
	}
	if !out.Success {
		// 被控侧拒绝（密钥无效/过期等）：回滚本次创建的节点。
		config.RemoveNode(node.ID)
		auditRequest(r, "node.adopt", node.Name, "被控面板拒绝对接", false, out.Message)
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "对接失败: " + out.Message})
		return
	}
	if n, ok := config.FindNode(node.ID); ok {
		node = n
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "对接成功，节点已接入（agent 服务重启后即上线）", Data: map[string]any{"node": sanitizeNode(node)}})
}

// tlsVerifyLabel / boolLabel 审计日志友好输出。
func tlsVerifyLabel(skip bool) string {
	if skip {
		return "跳过（已豁免）"
	}
	return "严格校验"
}

func boolLabel(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

// bindIPOrNone 审计日志友好输出。
func bindIPOrNone(ip string) string {
	if ip == "" {
		return "无"
	}
	return ip
}

// nodeInstallCommand 返回一行式安装命令所需的材料。
type nodeInstallCommand struct {
	Command   string `json:"command"`
	Key       string `json:"install_key"`
	ExpiresAt string `json:"expires_at"`
	BoundIP   string `json:"bound_ip"`
	SHA256    string `json:"sha256"`
}

// handleNodeInstallCommand 生成「一行安装命令」（管理员面板用）。
// 面板不再展示/复制完整 bash 脚本正文，只下发：
//
//	curl -fsSL -H "X-Install-Key: <key>" https://<主控>/api/nodes/<id>/install-script | sudo bash
//
// key 特性：一次性（注册即焚）、24h TTL、可选绑定被控出口 IP；
// 经 X-Install-Key 请求头传输，不进 URL/反代日志/浏览器历史（F4）。
// 脚本正文由 install-script 端点动态生成（动态端点传参，无静态硬编码密钥）。
func handleNodeInstallCommand(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	// key 已被消费（注册成功即焚）或从未生成：自动换发新 key 供重装/换机。
	installKey := node.InstallKey
	if installKey == "" {
		installKey = randomNodeSecret(32)
		expiry := time.Now().UTC().Format(time.RFC3339)
		// 换发时保留节点原有的显式 IP 绑定；不再回填「管理员 IP」（那不是被控出口）。
		config.UpdateNode(node.ID, func(n *config.Node) {
			n.InstallKey = installKey
			n.InstallKeyCreatedAt = expiry
		})
		node, _ = config.FindNode(nodeID)
		auditRequest(r, "node.install_command", node.Name, "换发一次性 install_key（指纹 "+installKeyFingerprint(installKey)+"）", true, "")
	}
	script := buildAgentInstallScript(externalBaseURL(r), installKey, node.Name, "")
	hashBytes := sha256.Sum256([]byte(script))
	curlURL := fmt.Sprintf("%s/api/nodes/%s/install-script", externalBaseURL(r), nodeID)
	command := fmt.Sprintf("curl -fsSL -H \"X-Install-Key: %s\" %s | sudo bash", installKey, curlURL)
	expiresAt := ""
	if node.InstallKeyCreatedAt != "" {
		if created, err := time.Parse(time.RFC3339, node.InstallKeyCreatedAt); err == nil {
			expiresAt = created.Add(24 * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: nodeInstallCommand{
		Command:   command,
		Key:       installKey,
		ExpiresAt: expiresAt,
		BoundIP:   node.InstallKeyIP,
		SHA256:    hex.EncodeToString(hashBytes[:]),
	}})
}

// installKeyFingerprint 审计日志脱敏：只保留 key 前 8 位指纹。
func installKeyFingerprint(key string) string {
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return key
}

// sanitizeNode 剥离凭据字段：agent Token / InstallKey 永不出现在
// 节点列表与详情响应中（密钥最小化，防止 node:read 权限账号横向控制被控机）。
func sanitizeNode(n config.Node) map[string]any {
	return map[string]any{
		"id":               n.ID,
		"name":             n.Name,
		"address":          n.Address,
		"status":           n.Status,
		"last_seen":        n.LastSeen,
		"version":          n.Version,
		"os_name":          n.OSName,
		"cpu_count":        n.CPUCount,
		"ram_total_mb":     n.RAMTotalMB,
		"ram_used_mb":      n.RAMUsedMB,
		"disk_total_gb":    n.DiskTotalGB,
		"disk_used_gb":     n.DiskUsedGB,
		"container_count":  n.ContainerCount,
		"region_id":        n.RegionID,
		"node_group_id":    n.NodeGroupID,
		"cluster_id":       n.ClusterID,
		"cell_id":          n.CellID,
		"virt_types":       n.VirtTypes,
		"created_at":       n.CreatedAt,
		"maintenance_mode": n.MaintenanceMode,
	}
}

func handleNodeItem(w http.ResponseWriter, r *http.Request, nodeID string) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "node:read") {
			return
		}
		node, ok := config.FindNode(nodeID)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
			return
		}
		// 脱敏下发：不携带 Token / InstallKey。
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: sanitizeNode(node)})
	case http.MethodDelete:
		if !requireScope(w, r, "node:write") {
			return
		}
		node, ok := config.FindNode(nodeID)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
			return
		}
		removed, conts := config.RemoveNode(nodeID)
		if !removed {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
			return
		}
		// 级联移除该节点的容器记录，避免留下悬空 node_id 的幽灵实例（见 config.RemoveNode）。
		auditRequest(r, "node.delete", node.Name,
			fmt.Sprintf("删除被控节点（一并移除 %d 个实例记录）", conts), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Node deleted"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// nodeHeartbeatPersistEvery throttles persistence of telemetry-only heartbeats.
// The agent beats every 10s; persisting every beat would write one node row per
// beat even when nothing but LastSeen changed. Memory state is still refreshed
// on every beat (the panel reads memory).
//
// 批处理（P2）之后这个节流仍然保留：合并窗口已把事务数降到每秒几次，节流省的是
// 「写进行数」（30k 节点从 3000 行/秒降到 ~500 行/秒），代价是库里的 LastSeen
// 最多滞后 60s。面板显示读内存，不受影响。
const nodeHeartbeatPersistEvery = 60 * time.Second

var (
	nodeHeartbeatSaveMu  sync.Mutex
	nodeHeartbeatSavedAt = map[string]time.Time{}

	// nodeReported 记录「每个节点上一次上报的容器 UUID 集合」。orphan 判定需要
	// 「主控有、agent 这次没报」的容器集合：直接扫 cfg.Containers 找本节点的容器
	// 是 O(全部容器)（30w 容器 × 3000 次心跳/秒 = 不可能），而 agent 的上报集合
	// 正是「这台节点上应该有哪些容器」的权威答案，取差集即 O(本节点容器数)。
	//
	// 只在 AppConfigMu 写锁内访问（心跳的变更回调），因此不需要额外的锁。
	nodeReported       map[string]map[string]bool
	nodeReportedSeeded bool
)

// nodeHeartbeatSaveDue reports whether this node's beat is due for persistence
// (first beat, or >= throttle interval). It stamps the time on hit so that the
// caller can reuse the decision when nothing structural changed.
func nodeHeartbeatSaveDue(nodeID string) bool {
	nodeHeartbeatSaveMu.Lock()
	defer nodeHeartbeatSaveMu.Unlock()
	if t, ok := nodeHeartbeatSavedAt[nodeID]; ok && time.Since(t) < nodeHeartbeatPersistEvery {
		return false
	}
	nodeHeartbeatSavedAt[nodeID] = time.Now()
	return true
}

// handleNodeHeartbeat 由被控 agent 周期性上报资源、容器清单与在线状态。
// 心跳携带的容器摘要会与主控本地容器列表做增量同步：
//   - 已存在（同 UUID）：更新状态/资源字段，确保 NodeID 归属正确
//   - 主控无：新增到主控列表（标记 NodeID）
//   - 主控有但 agent 未上报：标记 orphaned=true（agent 侧已删）
func handleNodeHeartbeat(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	token := tokenFromRequest(r)
	node, ok := config.FindNode(nodeID)
	// 常量时间比较（审计 N-2）：同文件其它节点 token 校验都用
	// subtle.ConstantTimeCompare，此处曾用 != 会泄漏前缀匹配长度。
	if !ok || node.Token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(node.Token)) != 1 {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid node token"})
		return
	}
	var req struct {
		Version        string  `json:"version"`
		OSName         string  `json:"os_name"`
		CPUCount       int     `json:"cpu_count"`
		RAMTotalMB     int64   `json:"ram_total_mb"`
		RAMUsedMB      int64   `json:"ram_used_mb"`
		DiskTotalGB    float64 `json:"disk_total_gb"`
		DiskUsedGB     float64 `json:"disk_used_gb"`
		ContainerCount int     `json:"container_count"`
		// 容器摘要（可选）：agent 心跳时附带的轻量容器列表，供主控聚合。
		// 指针类型用于区分"字段缺失"（老 agent，跳过同步）与"空数组"（节点上零容器，需要同步清理 orphan）。
		ContainerSummaries *[]heartbeatContainerSummary `json:"containers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	due := nodeHeartbeatSaveDue(nodeID)
	var statusChanges []containerStatusChange
	// changedIDs 收集本次心跳实际改动过的容器 ID。延迟落库按「行标识」声明改动
	// （值在提交时重新读取），因此这里只需要 ID，且单节点只上报几十个容器，
	// 落库开销与「主控总容器数」无关。
	var changedIDs []int
	notFound := false
	// 心跳只触碰本节点的 cfg.Node 与本节点的容器，故用精确脏集声明：未声明的行不
	// 重算指纹，子用户/密钥/快照/任务零扫描。落库本身并入后台窗口合并成一个事务
	// （P2，见 config/store_batch.go）：30k 节点 × 10s = 3000 次/秒的请求不再等于
	// 3000 次提交，HTTP 响应也不再等 SQLite 提交。
	config.MutateGlobalSaveDeferred(func(cfg *config.EyvescloudConfig) (bool, config.DirtyIDs) {
		idx, ok := config.FindNodeIndexUnlocked(nodeID)
		if !ok {
			notFound = true
			return false, config.DirtyIDs{}
		}
		n := &cfg.Nodes[idx]
		structural := false
		if n.Status != "online" {
			n.Status = "online"
			structural = true
		}
		if req.Version != "" && n.Version != req.Version {
			n.Version = req.Version
			structural = true
		}
		if req.OSName != "" && n.OSName != req.OSName {
			n.OSName = req.OSName
			structural = true
		}
		if req.CPUCount > 0 && n.CPUCount != req.CPUCount {
			n.CPUCount = req.CPUCount
			structural = true
		}
		if req.RAMTotalMB > 0 && n.RAMTotalMB != req.RAMTotalMB {
			n.RAMTotalMB = req.RAMTotalMB
			structural = true
		}
		if req.DiskTotalGB > 0 && n.DiskTotalGB != req.DiskTotalGB {
			n.DiskTotalGB = req.DiskTotalGB
			structural = true
		}
		// Telemetry fields only refresh memory state; they are not structural.
		n.RAMUsedMB = req.RAMUsedMB
		n.DiskUsedGB = req.DiskUsedGB
		n.ContainerCount = req.ContainerCount
		n.LastSeen = now
		if req.ContainerSummaries != nil {
			if syncAgentContainersUnlocked(cfg, nodeID, *req.ContainerSummaries, &statusChanges, &changedIDs) {
				structural = true
			}
		}
		if !structural && !due {
			return false, config.DirtyIDs{}
		}
		// LastSeen 到期必写，故声明本节点。
		return true, config.DirtyIDs{Nodes: []string{nodeID}, Containers: changedIDs}
	})
	if notFound {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}

	for _, ch := range statusChanges {
		config.FireContainerStatusHook(ch.id, ch.name, ch.old, ch.new)
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ok"})
}

// heartbeatContainerSummary 是 agent 心跳上报的轻量容器摘要。
type heartbeatContainerSummary struct {
	ID             int    `json:"id"`
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Virtualization string `json:"virtualization"`
	// Template 是实例的系统镜像/模板 ID（列表与详情页的"系统"列展示用）。
	Template string `json:"template,omitempty"`
	// IP / SSHPort 由被控上报，供主控列表直接展示节点容器地址（此前主控侧为空）。
	IP            string  `json:"ip,omitempty"`
	SSHPort       int     `json:"ssh_port,omitempty"`
	Suspended     bool    `json:"suspended,omitempty"`
	VCPU          float64 `json:"vcpu"`
	RAMMB         int     `json:"ram_mb"`
	DiskGB        float64 `json:"disk_gb"`
	ExpiresAt     string  `json:"expires_at,omitempty"`
	TrafficUsedRX int64   `json:"traffic_used_rx,omitempty"`
	TrafficUsedTX int64   `json:"traffic_used_tx,omitempty"`
	TrafficLimit  int64   `json:"traffic_limit,omitempty"`
	// 最新实时指标（agent 本机 metric 尾采样点）
	CPU       float64 `json:"cpu,omitempty"`
	Memory    float64 `json:"memory,omitempty"`
	NetworkRx float64 `json:"network_rx,omitempty"`
	NetworkTx float64 `json:"network_tx,omitempty"`
	DiskRead  float64 `json:"disk_read,omitempty"`
	DiskWrite float64 `json:"disk_write,omitempty"`
	MetricTS  int64   `json:"metric_ts,omitempty"`
}

// newContainerIDAllocator 为节点容器分配主控侧全局唯一 ID：首次调用扫一遍现有容器求
// 最大 ID（防 NextContainerID 计数漂移；避开本机容器与其它节点容器已占用的号段），
// 之后本地自增 —— 故一次 sync 里新建 m 个容器总共只扫一遍 O(全部容器)，而非 m 遍。
//
// 与旧实现等价：旧实现的 `used` 集合是死代码（`id` 必然 > maxID，而 `used` 只含
// ≤ maxID 的 ID，故查表永不命中），已删除。
func newContainerIDAllocator(cfg *config.EyvescloudConfig) func() int {
	next := 0
	initialized := false
	return func() int {
		if !initialized {
			maxID := 0
			for i := range cfg.Containers {
				if cfg.Containers[i].ID > maxID {
					maxID = cfg.Containers[i].ID
				}
			}
			next = cfg.NextContainerID
			if next <= maxID {
				next = maxID + 1
			}
			initialized = true
		}
		id := next
		next++
		if cfg.NextContainerID <= id {
			cfg.NextContainerID = id + 1
		}
		return id
	}
}

// syncAgentContainersUnlocked merges agent-reported container summaries into the
// controller container list. Match by UUID (IDs may collide across nodes; UUIDs are
// globally unique). Caller must hold AppConfigMu write lock (guaranteed by
// MutateGlobalSaveDeferred). Returns whether a structural change occurred:
// container add, status/suspend/quota/expiry/virtualization/template changes.
// Pure telemetry refreshes (traffic counters, IP, SSH port) only update memory.
//
// 复杂度：O(本节点上报容器数)，与主控总容器数无关。两条 O(全部容器) 扫描是这里
// 曾经的墙（30w 容器 × 3000 次心跳/秒）：
//   - 按 UUID 找容器 → 改为下标缓存（config.FindContainerIndexByUUIDUnlocked）；
//   - 找「本节点已有但 agent 这次没报」的容器 → 改为取 agent 上次上报集合的差集
//     （nodeReported）。上报集合就是「这台节点上应该有哪些容器」的权威答案。
//
// changedIDs（可空）收集本次实际碰过的容器主控 ID，供调用方声明落库脏集。落库的值
// 在提交时按 ID 从内存重新读取，故这里收集的是 ID 而不是快照，也不会写入过期值。
func syncAgentContainersUnlocked(cfg *config.EyvescloudConfig, nodeID string, summaries []heartbeatContainerSummary, statusChanges *[]containerStatusChange, changedIDs *[]int) bool {
	structural := false
	var marked map[int]bool // 主控侧容器 ID 已收集
	mark := func(id int) {
		if changedIDs == nil || id == 0 {
			return
		}
		if marked == nil {
			marked = make(map[int]bool, len(summaries))
		}
		if marked[id] {
			return
		}
		marked[id] = true
		*changedIDs = append(*changedIDs, id)
	}

	// Metric history write (data source for cross-node container detail/monitor pages).
	for _, s := range summaries {
		if s.UUID == "" || s.MetricTS == 0 {
			continue
		}
		appendAgentMetricPoint(s)
	}

	ensureNodeReportedUnlocked(cfg)

	reported := make(map[string]bool, len(summaries))
	for _, s := range summaries {
		if s.UUID != "" {
			reported[s.UUID] = true
		}
	}

	// 主控侧 ID 分配器：整段 sync 复用，避免「每新建一个容器就 O(全部容器) 扫一遍」。
	nextContainerID := newContainerIDAllocator(cfg)

	// 2) handle each container reported by the agent
	for _, s := range summaries {
		if s.UUID == "" {
			continue
		}
		if i, ok := config.FindContainerIndexByUUIDUnlocked(s.UUID); ok {
			c := &cfg.Containers[i]
			// Update heartbeat-synced fields (controller-exclusive fields like
			// OwnerSubUserID/SSHPassword are preserved).
			if c.NodeID != nodeID {
				c.NodeID = nodeID
				structural = true
			}
			if s.ID > 0 && c.NodeLocalID != s.ID {
				c.NodeLocalID = s.ID
				structural = true
			}
			if c.Status != s.Status {
				*statusChanges = append(*statusChanges, containerStatusChange{
					id:   c.ID,
					name: c.Name,
					old:  c.Status,
					new:  s.Status,
				})
				c.Status = s.Status
				structural = true
			}
			if c.Virtualization != s.Virtualization {
				c.Virtualization = s.Virtualization
				structural = true
			}
			if s.Template != "" && c.Template != s.Template {
				c.Template = s.Template
				structural = true
			}
			if c.Suspended != s.Suspended {
				c.Suspended = s.Suspended
				structural = true
			}
			if c.VCPU != s.VCPU {
				c.VCPU = s.VCPU
				structural = true
			}
			if c.RAMMB != s.RAMMB {
				c.RAMMB = s.RAMMB
				structural = true
			}
			if c.DiskGB != s.DiskGB {
				c.DiskGB = s.DiskGB
				structural = true
			}
			if c.ExpiresAt != s.ExpiresAt {
				c.ExpiresAt = s.ExpiresAt
				structural = true
			}
			// Telemetry: refresh memory only.
			if s.IP != "" {
				c.IP = s.IP
			}
			if s.SSHPort > 0 {
				c.SSHPort = s.SSHPort
			}
			c.TrafficUsedRX = s.TrafficUsedRX
			c.TrafficUsedTX = s.TrafficUsedTX
			mark(c.ID)
			continue
		}
		// Not present on the controller: add from the agent summary. The
		// controller-side ID must be globally unique (SQLite primary key):
		// node-local IDs may collide with local containers (observed: node
		// id=3 vs local id=3 -> whole save tx PK conflict -> all writes fail
		// silently). Allocate a controller-unique ID; keep the node-local ID
		// in NodeLocalID for proxy calls.
		newC := config.Container{
			ID: nextContainerID(), NodeLocalID: s.ID,
			UUID: s.UUID, Name: s.Name,
			Status: s.Status, Virtualization: s.Virtualization, Template: s.Template,
			Suspended: s.Suspended, VCPU: s.VCPU, RAMMB: s.RAMMB,
			DiskGB: s.DiskGB, NodeID: nodeID,
			IP: s.IP, SSHPort: s.SSHPort,
			ExpiresAt: s.ExpiresAt, TrafficUsedRX: s.TrafficUsedRX,
			TrafficUsedTX: s.TrafficUsedTX,
		}
		config.AppendContainerUnlocked(newC)
		mark(newC.ID)
		structural = true
	}

	// 3) containers this node reported last time but no longer reports: mark orphaned.
	lastReported := nodeReported[nodeID]
	for uuid := range lastReported {
		if reported[uuid] {
			continue
		}
		i, ok := config.FindContainerIndexByUUIDUnlocked(uuid)
		if !ok {
			continue // 主控侧对应的行已不存在，无需清理
		}
		c := &cfg.Containers[i]
		// 已迁移到别的节点（或被改回本机容器）的不算本节点的 orphan：
		// 差集来自「本节点上次上报」，归属要按当前值再核对一次。
		if c.NodeID != nodeID {
			continue
		}
		if c.Status != "orphaned" {
			*statusChanges = append(*statusChanges, containerStatusChange{
				id:   c.ID,
				name: c.Name,
				old:  c.Status,
				new:  "orphaned",
			})
			c.Status = "orphaned"
			structural = true
		}
		mark(c.ID)
	}

	// 用本次上报集合替换：下一次心跳的 orphan 候选就是这个差集。
	next := make(map[string]bool, len(reported))
	for uuid := range reported {
		next[uuid] = true
	}
	nodeReported[nodeID] = next

	return structural
}

// ensureNodeReportedUnlocked 首次心跳时用主控侧已有的节点容器播种「上次上报集合」，
// 使重启后第一次心跳仍能检出「agent 已删除但主控还留着」的容器。
// 调用方必须持有 AppConfigMu 写锁。
func ensureNodeReportedUnlocked(cfg *config.EyvescloudConfig) {
	if nodeReportedSeeded {
		return
	}
	nodeReported = make(map[string]map[string]bool)
	for i := range cfg.Containers {
		c := cfg.Containers[i]
		if c.NodeID == "" || c.UUID == "" {
			continue
		}
		set := nodeReported[c.NodeID]
		if set == nil {
			set = make(map[string]bool)
			nodeReported[c.NodeID] = set
		}
		set[c.UUID] = true
	}
	nodeReportedSeeded = true
}

// appendAgentMetricPoint 把 agent 心跳上报的指标点写入主控 metric history，
// 让跨节点容器在监控页/详情页与本机容器数据形态一致。
func appendAgentMetricPoint(s heartbeatContainerSummary) {
	key := "uuid:" + s.UUID
	point := ContainerMetricPoint{
		TS: s.MetricTS, CPU: s.CPU, Memory: s.Memory,
		NetworkRx: s.NetworkRx, NetworkTx: s.NetworkTx,
		DiskRead: s.DiskRead, DiskWrite: s.DiskWrite,
	}
	containerMetricMu.Lock()
	history := containerMetricHistory[key]
	// 去重：agent 心跳 10s 一次，指标采样 30s 一次，相同 TS 不重复追加
	if len(history) > 0 && history[len(history)-1].TS == point.TS {
		containerMetricMu.Unlock()
		return
	}
	// 与本机采样一致的保留窗口裁剪
	cutoff := time.Now().Add(-hostMetricRetention).UnixMilli()
	keepFrom := 0
	for keepFrom < len(history) && history[keepFrom].TS < cutoff {
		keepFrom++
	}
	if keepFrom > 0 {
		copy(history, history[keepFrom:])
		history = history[:len(history)-keepFrom]
	}
	containerMetricHistory[key] = append(history, point)
	containerMetricMu.Unlock()
}

// handleNodeInstallScript 生成被控一键安装脚本。
// 主控地址烘焙进脚本：优先「面板绑定域名」（PanelDomain），否则按本次请求
// Host 推导（curl 从哪台主控拉到脚本，脚本就回连哪台主控）。运行时探测
// （--controller 覆盖 / SSH 来源 / 本机源 IP / 交互提示）仅作兜底：
//  1. 用户通过 --controller 参数显式指定
//  2. 硬编码主控地址（本 handler 烘焙，正常路径必命中）
//  3. SSH 会话客户端 IP（SSH_CONNECTION 环境变量）
//  4. 本机源 IP（ip route get）—— 仅地址推导失败时
//  5. 交互式提示
//
// 也支持网络脚本一行命令（类似 主流面板 / 同类面板 风格）：
//
//	curl -fsSL -H "X-Install-Key: <key>" https://<主控>/api/nodes/<id>/install-script | sudo bash
//
// 认证支持两条路径：
//  1. X-Install-Key 请求头携带有效 install_key 且属于该节点
//     （F4：key 走 header 而非 URL query，避免落入反代访问日志/浏览器历史/Referer）
//  2. 管理员 node:write scope（面板下载）
func handleNodeInstallScript(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	// 认证路径 1：X-Install-Key 头（且必须属于本节点，防止拿 A 节点的 key 拉 B 节点的脚本）
	authedByKey := false
	if hk := strings.TrimSpace(r.Header.Get("X-Install-Key")); hk != "" {
		if kn, found := config.FindNodeByInstallKey(hk); found && kn.ID == nodeID {
			authedByKey = true
		}
	}
	// 认证路径 2：管理员 scope
	if !authedByKey && !requireScope(w, r, "node:write") {
		return
	}
	// 注册成功后 InstallKey 会被清空（一次性 token）。
	// 重新获取脚本时自动换发新 key，保证“面板再次下载脚本”始终可用于重装/换机。
	installKey := node.InstallKey
	if installKey == "" {
		installKey = randomNodeSecret(32)
		config.UpdateNode(node.ID, func(n *config.Node) {
			n.InstallKey = installKey
			n.InstallKeyCreatedAt = time.Now().UTC().Format(time.RFC3339)
			// 不回填 IP：clientIP(r) 是管理员 IP 而非被控出口 IP，
			// 错误绑定会导致 agent 注册必然 IP mismatch；绑定仅由 bind_ip 显式指定。
		})
	}
	// 烘焙主控对外地址（优先「面板绑定域名」，否则按本次请求 Host 推导）：
	// 被控无法可靠自知主控地址，运行时探测仅在地址推导失败时兜底。
	script := buildAgentInstallScript(externalBaseURL(r), installKey, node.Name, "")
	hashBytes := sha256.Sum256([]byte(script))
	hash := hex.EncodeToString(hashBytes[:])

	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=eyvescloud-agent-%s.sh", node.Name))
	w.Header().Set("X-Content-SHA256", hash)

	// F4：install_key 走请求头，不进 URL（避免落入访问日志/历史/Referer）。
	// 安装命令地址优先取「面板绑定域名」，保证反代环境下 curl 直达。
	curlURL := fmt.Sprintf("%s/api/nodes/%s/install-script", externalBaseURL(r), nodeID)

	comments := fmt.Sprintf(`# 一行安装（推荐；X-Install-Key 头携带密钥，不写入 URL）：
# curl -fsSL -H "X-Install-Key: %s" %s | sudo bash
#
# 先校验再安装（离线审计场景）：
# curl -fsSL -H "X-Install-Key: %s" %s -o install.sh
# echo "%s  install.sh" | sha256sum -c
# sudo bash install.sh
#
`, installKey, curlURL, installKey, curlURL, hash)

	_, _ = w.Write([]byte(comments + script))
}

func handleNodeInstallScriptSHA256(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	authedByKey := false
	if hk := strings.TrimSpace(r.Header.Get("X-Install-Key")); hk != "" {
		if kn, found := config.FindNodeByInstallKey(hk); found && kn.ID == nodeID {
			authedByKey = true
		}
	}
	if !authedByKey && !requireScope(w, r, "node:write") {
		return
	}
	installKey := node.InstallKey
	if installKey == "" {
		installKey = randomNodeSecret(32)
		config.UpdateNode(node.ID, func(n *config.Node) {
			n.InstallKey = installKey
			n.InstallKeyCreatedAt = time.Now().UTC().Format(time.RFC3339)
			// 不回填管理员 IP；绑定仅由 bind_ip 显式指定（见 handleNodeInstallCommand 注释）。
		})
	}
	script := buildAgentInstallScript(externalBaseURL(r), installKey, node.Name, "")
	hashBytes := sha256.Sum256([]byte(script))
	hash := hex.EncodeToString(hashBytes[:])

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"sha256": hash}})
}

// buildAgentInstallScript 生成被控一键安装脚本。
//
// controller 为主控对外地址：由调用方传入 externalBaseURL(r)（优先「面板绑定
// 域名」，否则按请求 Host 推导）——被控无法可靠自知主控地址（SSH 来源是管理
// 员客户端 IP、ip route get 源地址是被控自身 IP），主控必须在生成脚本时把
// 自己的地址烘焙进去。脚本内的运行时探测仅作为 controller 为空时的兜底。
//
// 注意：模板中所有 %s 都处于**双引号**上下文（INSTALL_KEY="%s" 等），必须用
// shellDQ（双引号转义）而非 shellEscape（单引号包裹）。此前误用 shellEscape
// 会生成 INSTALL_KEY="'<key>'"（值带字面单引号，后续 X-Install-Key 认证失败）、
// 以及 [ -n "”" ]（非空恒真）导致 CONTROLLER 被赋成字面量 ”，curl 报
// "Could not resolve host: ”"。
func buildAgentInstallScript(controller, installKey, nodeName, defaultAddr string) string {
	nameSQ := shellDQ(nodeName)
	addrSQ := shellDQ(defaultAddr)
	installKeySQ := shellDQ(installKey)
	hardController := shellDQ(controller)
	return fmt.Sprintf(`#!/bin/bash
# EyvesCloud 被控节点一键安装脚本
# 用法:
#   bash eyvescloud-agent.sh [--controller URL] [--name 名称] [--addr 被控面板地址]
#   curl -fsSL -H "X-Install-Key: <key>" https://<主控>/api/nodes/<id>/install-script | sudo bash
#
# 主控地址优先级：
#   1) --controller 参数显式指定
#   2) 主控烘焙地址（生成脚本时的面板域名/请求地址，正常路径必命中）
#   3) SSH 会话客户端 IP（通过 SSH_CONNECTION）—— 兜底
#   4) 本机 IPv4/IPv6 源 IP（ip route get）—— 兜底
#   5) 交互式提示（非 tty 时跳过，会报错）
set -e

INSTALL_KEY="%s"
NODE_NAME=""
NODE_ADDR=""
CONTROLLER_OVERRIDE=""

# 参数解析
while [ $# -gt 0 ]; do
  case "$1" in
    --controller) CONTROLLER_OVERRIDE="$2"; shift 2 ;;
    --name) NODE_NAME="$2"; shift 2 ;;
    --addr) NODE_ADDR="$2"; shift 2 ;;
    --allow-insecure-http) insecure_arg="--allow-insecure-http"; shift ;;
    -h|--help)
      echo "用法: $0 [--controller URL] [--name 名称] [--addr 面板地址]"
      echo "      curl -fsSL -H 'X-Install-Key: <key>' https://<主控>/api/nodes/<id>/install-script | sudo bash"
      exit 0
      ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

NODE_NAME="${NODE_NAME:-%s}"
NODE_ADDR="${NODE_ADDR:-%s}"

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 权限运行: sudo bash $0"
  exit 1
fi

command -v curl >/dev/null 2>&1 || { echo "缺少 curl，请先安装"; exit 1; }

# ============ 主控地址自动探测 ============
detect_controller() {
  if [ -n "$CONTROLLER_OVERRIDE" ]; then
    echo "$CONTROLLER_OVERRIDE"
    return 0
  fi
  # 硬编码兜底（旧路径/回退）
  if [ -n "%s" ]; then
    echo "%s"
    return 0
  fi

  local detected_ip=""
  local scheme="https"
  local port="8999"

  # 1) SSH 会话客户端 IP —— 最可靠（你就是从主控 SSH 过来的）
  if [ -n "$SSH_CONNECTION" ]; then
    local ssh_ip
    ssh_ip="$(printf '%%s' "$SSH_CONNECTION" | awk '{print $1}')"
    if [ -n "$ssh_ip" ]; then
      case "$ssh_ip" in
        *:*) detected_ip="[$ssh_ip]" ;;  # IPv6 客户端：URL 里必须加方括号
        *)   detected_ip="$ssh_ip" ;;
      esac
    fi
  fi

  # 2) IPv4 源 IP：从 ip route get 输出的 src 字段提取。
  #    不用固定字段位置：不同内核输出可能含 from/via 等可选段，位置会漂移。
  if [ -z "$detected_ip" ] && command -v ip >/dev/null 2>&1; then
    local ipv4
    ipv4="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src"){print $(i+1); exit}}')"
    if [ -n "$ipv4" ]; then
      detected_ip="$ipv4"
    fi
  fi

  # 3) IPv6 源 IP —— 纯 IPv6 环境走这个（同样按 src 字段提取，加方括号）
  if [ -z "$detected_ip" ] && command -v ip >/dev/null 2>&1; then
    local ipv6
    ipv6="$(ip -6 route get 2001:4860:4860::8888 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src"){print $(i+1); exit}}')"
    if [ -n "$ipv6" ]; then
      detected_ip="[$ipv6]"
    fi
  fi

  if [ -n "$detected_ip" ]; then
    echo "${scheme}://${detected_ip}:${port}"
    return 0
  fi

  # 4) 交互式提示（仅 tty）
  if [ -t 0 ]; then
    printf "无法自动探测主控地址，请输入（如 4.4.4.4 / 4.4.4.4:8999 / 2001:db8::1 / https://ctrl.example.com）: "
    IFS= read -r user_input
    user_input="$(printf '%%s' "$user_input" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    if [ -z "$user_input" ]; then
      echo "未输入主控地址，退出" >&2
      exit 1
    fi
    # 补全 scheme/port，IPv6 需加方括号
    case "$user_input" in
      https://*|http://*) echo "$user_input" ;;
      *\]* )              echo "https://$user_input" ;;            # 已带 [] 的 IPv6（可含端口）
      *:*:* )             echo "https://[${user_input}]:${port}" ;; # 裸 IPv6（多个冒号）
      *:* )               echo "https://$user_input" ;;            # IPv4:port
      * )                 echo "${scheme}://${user_input}:${port}" ;;
    esac
    return 0
  fi

  echo "无法自动探测主控地址，且非交互模式。请使用 --controller 参数指定。" >&2
  exit 1
}

CONTROLLER="$(detect_controller)"
echo "==> 主控地址: $CONTROLLER"

# 探测是否是 http://（自动加 --allow-insecure-http）
case "$CONTROLLER" in
  http://* ) insecure_arg="${insecure_arg:---allow-insecure-http}" ;;
esac

ARCH="$(uname -m 2>/dev/null || echo amd64)"
case "$ARCH" in
  x86_64|amd64) ARCH_NORM="amd64" ;;
  aarch64|arm64) ARCH_NORM="arm64" ;;
  *) echo "不支持的架构: $ARCH（仅支持 amd64/arm64）"; exit 1 ;;
esac

echo "==> [1/3] 下载 EyvesCloud 二进制"
HDR_FILE="$(mktemp)"
TARGET_BIN="/usr/local/bin/eyvescloud"
# 下载到临时文件再原子替换：直接覆盖正在运行的二进制会返回 ETXTBSY
# （升级已安装节点时必然命中）；rename 不受此限制，运行中的进程继续使用旧 inode，
# 待本脚本最后一步重启服务后切换到新二进制。
TMP_BIN="$(mktemp "$TARGET_BIN.new.XXXXXX" 2>/dev/null || mktemp /tmp/eyvescloud.new.XXXXXX)"
cleanup_tmp() { rm -f "$HDR_FILE" "$TMP_BIN"; }
trap cleanup_tmp EXIT
# F4：install_key 走 X-Install-Key 头，不进 URL（避免落入反代访问日志/Referer）。
if ! curl -fsSL -o "$TMP_BIN" -D "$HDR_FILE" \
  -H "X-Install-Key: $INSTALL_KEY" \
  "$CONTROLLER/api/nodes/binary?arch=$ARCH_NORM"; then
  echo "下载失败，请检查：主控地址是否正确、install_key 是否有效、网络是否可达"
  exit 1
fi
if [ ! -s "$TMP_BIN" ]; then
  echo "下载产物为空（主控返回空文件），已中止"
  exit 1
fi

# G4（P1-7）：SHA256 完整性校验——主控在 X-Binary-SHA256 响应头中附带摘要，
# 此处强制比对，防止传输损坏/被篡改的产物被安装。校验失败即删除并中止。
# 注：Go 侧 Header.Set 输出为规范化的 X-Binary-Sha256，此处用 tolower 匹配保持可移植。
EXPECTED_SHA="$(awk 'tolower($1)=="x-binary-sha256:" {gsub(/\r/,""); print $2}' "$HDR_FILE")"
if [ -n "$EXPECTED_SHA" ]; then
  ACTUAL_SHA="$(sha256sum "$TMP_BIN" 2>/dev/null | awk '{print $1}')"
  if [ -z "$ACTUAL_SHA" ] || [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "二进制 SHA256 校验失败（期望 $EXPECTED_SHA，实际 ${ACTUAL_SHA:-无法计算}），已删除"
    exit 1
  fi
  echo "SHA256 校验通过: $ACTUAL_SHA"
else
  echo "警告：主控未返回 X-Binary-SHA256，跳过完整性校验（建议升级主控）" >&2
fi
chmod +x "$TMP_BIN"
# 原子替换（旧二进制被覆盖前先备份失败不影响：rename 成功即新版本就位）。
mv -f "$TMP_BIN" "$TARGET_BIN"
chmod +x "$TARGET_BIN"
TMP_BIN="" # 已消费，避免 trap 误删

echo "==> [1/3] 校验二进制"
"$TARGET_BIN" --version >/dev/null 2>&1 \
  || { echo "下载的二进制无法运行，架构或产物不匹配（本机 $ARCH）"; exit 1; }

echo "==> [2/3] 注册被控节点"
# --register-only：仅注册并落盘后退出。完整 agent 最后会进入 server.Run()
# 永久阻塞（心跳 + 本地面板），在前台执行会把安装脚本卡死在这一步。
/usr/local/bin/eyvescloud agent --register-only --controller="$CONTROLLER" --install-key="$INSTALL_KEY" --name="$NODE_NAME" --addr="$NODE_ADDR" $insecure_arg || {
  echo "注册失败（已注册过的节点可忽略）";
}

echo "==> [3/3] 配置自启动服务"
# heredoc 不加引号：${CONTROLLER} 等由 shell 展开后写入，参数加双引号防空格拆分
# （systemd 自行解析 ExecStart 中的双引号）。
cat > /etc/systemd/system/eyvescloud-agent.service <<UNITEOF
[Unit]
Description=EyvesCloud Agent
After=network.target
# 注册失败（如 install_key 失效）时防止死循环重启
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=/usr/local/bin/eyvescloud agent --controller="${CONTROLLER}" --install-key="${INSTALL_KEY}" --name="${NODE_NAME}" --addr="${NODE_ADDR}" ${insecure_arg}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload

# 同机部署保护：若本机已安装面板（eyvescloud.service），则**不启用** agent 服务——
# agent 会再起一个面板并抢占同一端口（8999），谁先绑定谁赢，另一个无限重启。
# 同机场景下被控职责由面板进程承担（节点 token + 心跳 + 自更新），无需 agent 进程。
# 纯被控节点（没有面板单元）才启用 agent 服务。
if [ -f /etc/systemd/system/eyvescloud.service ] || [ -f /usr/lib/systemd/system/eyvescloud.service ]; then
  systemctl disable eyvescloud-agent >/dev/null 2>&1 || true
  # 关键：面板进程仍在使用"替换前"的旧 inode（rename 不影响已运行进程），
  # 必须重启面板服务，新二进制才真正生效（否则心跳/新端点仍是旧版本行为）。
  systemctl restart eyvescloud >/dev/null 2>&1 || true
  echo ""
  echo "提示：检测到本机已安装面板（eyvescloud.service）。"
  echo "      节点职责已由面板进程承担（心跳 + 自更新），agent 服务保持停用；"
  echo "      已重启面板服务以生效新二进制。"
  echo "      如需改为独立 agent 部署，请手动 systemctl enable --now eyvescloud-agent。"
else
  systemctl enable --now eyvescloud-agent
fi

echo ""
echo "=============================================="
echo "  EyvesCloud Agent 安装完成"
echo "  节点: $NODE_NAME"
echo "  主控: $CONTROLLER"
echo "  请回到主控面板查看节点状态"
echo "=============================================="
`,
		installKeySQ,
		nameSQ, addrSQ,
		hardController, hardController,
	)
}

// shellDQ 转义用于双引号包裹的 shell 变量值。
func shellDQ(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return replacer.Replace(value)
}

func defaultNodeAddress(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":8999"
}

// normalizeArch maps common `uname -m` / GOARCH values to the canonical Go
// architecture name used for release binary builds. Unknown inputs map to "".
func normalizeArch(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return ""
	}
}

// HandleNodeBinary serves the EyvesCloud binary to a worker node.
//
// Security & correctness:
//   - Requires a valid `X-Install-Key` request header so that arbitrary hosts
//     cannot download the controller binary. The key is the same install key
//     the worker was created with and uses for registration.（F4：header 传参，
//     不再接受 URL query，避免 key 落入访问日志/Referer）
//   - Honors an optional `arch` query parameter. When supplied and it does not
//     match the controller's own architecture, the request is rejected with a
//     clear error instead of handing out a binary that cannot run on the
//     worker (avoids silently installing a mismatched-build).
//   - Attaches an `X-Binary-SHA256` response header (G4/P1-7) so the worker
//     install script can verify the downloaded artifact's integrity before
//     installing it; the hash is cached by path/size/mtime.
func HandleNodeBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	// Authenticate using the worker's install key.
	installKey := strings.TrimSpace(r.Header.Get("X-Install-Key"))
	if installKey == "" {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "X-Install-Key header required"})
		return
	}
	if _, ok := config.FindNodeByInstallKey(installKey); !ok {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid install key"})
		return
	}

	// Architecture guard: reject clearly-incompatible target builds early.
	if requestedArch := strings.TrimSpace(r.URL.Query().Get("arch")); requestedArch != "" {
		canon := normalizeArch(requestedArch)
		if canon == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported arch: " + requestedArch})
			return
		}
		switch runtime.GOARCH {
		case "amd64":
			if canon != "amd64" {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "主控仅提供 amd64 二进制，无法为 " + canonicalArchLabel(canon) + " 被控提供适配构建"})
				return
			}
		case "arm64":
			if canon != "arm64" {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "主控仅提供 arm64 二进制，无法为 " + canonicalArchLabel(canon) + " 被控提供适配构建"})
				return
			}
		default:
			// Unknown controller architecture: allow the download rather than
			// hard-failing; the worker install script will verify the binary.
		}
	}

	exe, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(exe)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename=eyvescloud`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	// G4（P1-7）：附带二进制 SHA256，安装脚本下载后强制比对，
	// 防止传输损坏/被篡改的产物仅靠 --version 自检蒙混过关。
	if hash, herr := executableSHA256(exe); herr == nil {
		w.Header().Set("X-Binary-SHA256", hash)
	}
	_, _ = io.Copy(w, f)
}

// binaryHashCache 缓存主控二进制的 SHA256（按 路径+大小+mtime 失效），
// 避免每次 /api/nodes/binary 请求都全量读一遍自身二进制。
var binaryHashCache struct {
	mu    sync.Mutex
	path  string
	size  int64
	mtime time.Time
	hash  string
}

// executableSHA256 返回指定可执行文件的 SHA256 十六进制摘要（带缓存）。
func executableSHA256(exe string) (string, error) {
	info, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	binaryHashCache.mu.Lock()
	defer binaryHashCache.mu.Unlock()
	if binaryHashCache.path == exe &&
		binaryHashCache.size == info.Size() &&
		binaryHashCache.mtime.Equal(info.ModTime()) &&
		binaryHashCache.hash != "" {
		return binaryHashCache.hash, nil
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	binaryHashCache.path = exe
	binaryHashCache.size = info.Size()
	binaryHashCache.mtime = info.ModTime()
	binaryHashCache.hash = hash
	return hash, nil
}

func canonicalArchLabel(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64/amd64"
	case "arm64":
		return "aarch64/arm64"
	default:
		return arch
	}
}

func handleNodeContainers(w http.ResponseWriter, r *http.Request, nodeID string) {
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	if node.Address == "" {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
		return
	}
	data, status, err := proxyNodeRequest(r, node, http.MethodGet, "/api/agent/containers", nil)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理请求被控节点失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// handleNodeMaintenance 切换节点维护模式（POST /api/nodes/{id}/maintenance）。
// 开启后调度器不再把新容器放到该节点，用于系统升级 / 硬件维修前的排空准备。
func handleNodeMaintenance(w http.ResponseWriter, r *http.Request, nodeID string) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	found := false
	err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Nodes {
			if cfg.Nodes[i].ID == nodeID {
				cfg.Nodes[i].MaintenanceMode = req.Enabled
				if req.Enabled {
					cfg.Nodes[i].MaintenanceSince = time.Now().Format(time.RFC3339)
				} else {
					cfg.Nodes[i].MaintenanceSince = ""
				}
				found = true
				return
			}
		}
	})
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if !found {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	state := "off"
	if req.Enabled {
		state = "on"
	}
	auditRequest(r, "node.maintenance", nodeID, "maintenance="+state, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Maintenance mode " + state})
}

// handleNodeDrain 节点排空清单（GET /api/nodes/{id}/drain）：
// 返回该节点上的容器列表 + 可接收迁移的候选节点（在线、非维护、有余量）。
// 热迁移驱动（TransferDriver）落地前，管理员按此清单用迁移导出/导入完成搬移。
func handleNodeDrain(w http.ResponseWriter, r *http.Request, nodeID string) {
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	if node.Address == "" {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
		return
	}
	// 拉取被控节点容器清单
	data, status, err := proxyNodeRequest(r, node, http.MethodGet, "/api/agent/containers", nil)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理请求被控节点失败: " + err.Error()})
		return
	}
	var containers interface{}
	if status == http.StatusOK {
		_ = json.Unmarshal(data, &containers)
	}
	// 候选目标节点：在线、非维护
	config.AppConfigMu.RLock()
	candidates := []map[string]interface{}{}
	for _, n := range config.AppConfig.Nodes {
		if n.ID == nodeID || n.MaintenanceMode || n.Status != "online" {
			continue
		}
		candidates = append(candidates, map[string]interface{}{
			"id":              n.ID,
			"name":            n.Name,
			"region_id":       n.RegionID,
			"ram_free_mb":     n.RAMTotalMB - n.RAMUsedMB,
			"disk_free_gb":    n.DiskTotalGB - n.DiskUsedGB,
			"container_count": n.ContainerCount,
		})
	}
	maintenance := node.MaintenanceMode
	config.AppConfigMu.RUnlock()

	auditRequest(r, "node.drain", nodeID, "list", true, "")
	w.Header().Set("Content-Type", "application/json")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"node_id":          nodeID,
			"maintenance_mode": maintenance,
			"containers":       containers,
			"candidate_nodes":  candidates,
			"hint":             "开启 maintenance 后调度器不会再放置新容器；按 containers 清单配合迁移导出/导入把存量容器搬往 candidate_nodes。",
		},
	})
}

func handleNodeContainerAction(w http.ResponseWriter, r *http.Request, nodeID, rest string) {
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	if node.Address == "" {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "节点未配置地址，无法代理访问"})
		return
	}
	// 转发容器操作请求体（如重置密码的 {password}），供子端 agent 使用。
	data, status, err := proxyNodeRequest(r, node, http.MethodPost, "/api/agent/containers/"+strings.TrimPrefix(rest, "/"), r.Body)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "代理请求被控节点失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// nodeOnlineTimeout 心跳间隔为 10s；超过该窗口仍未心跳则视为离线。
const nodeOnlineTimeout = 60 * time.Second

// reconcileNodeOnlineStatuses 将超过心跳超时仍为 online 的节点置为 offline（离线检测）。
// 在节点列表读取前调用；心跳续期由 handleNodeHeartbeat 负责。
func reconcileNodeOnlineStatuses() {
	now := time.Now()
	var stale []config.Node
	config.AppConfigMu.RLock()
	for _, n := range config.AppConfig.Nodes {
		if n.Status != "online" || n.LastSeen == "" {
			continue
		}
		last, err := time.ParseInLocation("2006-01-02 15:04:05", n.LastSeen, time.Local)
		if err == nil && now.Sub(last) > nodeOnlineTimeout {
			stale = append(stale, n)
		}
	}
	config.AppConfigMu.RUnlock()
	for _, n := range stale {
		config.UpdateNode(n.ID, func(x *config.Node) { x.Status = "offline" })
	}
}

func splitNodeSubPath(path string) (nodeID, rest string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/api/nodes/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	rest = ""
	if len(parts) > 1 {
		rest = parts[1]
	}
	return parts[0], rest, true
}

func proxyNodeRequest(r *http.Request, node config.Node, method, path string, body io.Reader) ([]byte, int, error) {
	return proxyNodeRequestWithTimeout(r, node, method, path, body, 15*time.Second)
}

// NodeCreateTimeout 是"在节点上创建实例"这类长耗时操作的转发超时。
//
// 为什么需要单独一个：proxyNodeRequest 默认 15 秒，而节点上创建一个容器要走
// 完整的 lxc-create（下载/解包 rootfs、配网、装 SSH），实测 30 秒以上。
// 用默认超时会导致"主控报 502 超时，但节点上其实创建成功了"——用户看到失败，
// 实际资源已占用，是最难排查的一类不一致。
const NodeCreateTimeout = 300 * time.Second

// proxyNodeRequestWithTimeout 与 proxyNodeRequest 相同，但允许指定超时。
// 长耗时操作（创建、重装、快照恢复）必须用显式超时，避免主控先超时、
// 节点后完成造成状态分歧。
func proxyNodeRequestWithTimeout(r *http.Request, node config.Node, method, path string, body io.Reader, timeout time.Duration) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(r.Context(), method, strings.TrimSuffix(node.Address, "/")+path, body)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	req.Header.Set("Authorization", "Bearer "+node.Token)
	req.Header.Set("Content-Type", "application/json")
	// 多节点转发：把**服务端判定**的 actor（API Key / 子用户 / admin）写到
	// X-Original-Actor header，agent 端审计时优先使用，避免把操作记到 agent token 名下。
	//
	// 安全约束（审计 H-3）：该 header 属于服务端可信值，绝不能采信客户端传入的同名
	// header——否则任意已认证调用方都能在被控节点上把操作伪造到他人（含 admin）名下，
	// 破坏审计可信度。这里始终用 requestActor(r) 覆盖。
	if r != nil {
		req.Header.Set("X-Original-Actor", requestActor(r))
	}

	resp, err := nodeHTTPClient(node, timeout).Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// nodeHTTPClient 返回主控→被控方向的 HTTP 客户端，按节点 TLS 配置生效：
// 默认严格校验证书；节点创建时显式勾选 TLSSkipVerify（被控自签证书场景，
// 已落审计）才跳过校验。所有直连 node.Address 的路径统一走这里，避免
// 出现「探活严格、代理宽松」之类的配置漂移。
func nodeHTTPClient(node config.Node, timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if node.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 管理员显式豁免，创建节点时已落审计
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// normalizeNodeAddress 规范化节点面板地址。
// F9：无 scheme 时默认补 https://（与 agent→主控方向的强制 https 对齐），
// 避免 Bearer token 在主控→agent 链路明文传输。存量节点已存的 http:// 地址不受影响；
// agent 自注册地址始终显式携带 scheme（agent.go 按自身 SSL 状态补全）。
func normalizeNodeAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "https://" + value
	}
	return strings.TrimSuffix(value, "/")
}

// validateNodeAddress 校验节点面板地址（含 SSRF 防护）。
// SSRF 策略：
//   - 链路本地（169.254.0.0/16 含云元数据 169.254.169.254、fe80::/10）：永远拒绝，
//     无豁免——这是 SSRF 攻击的核心目标（凭据窃取）。
//   - 环回 + RFC1918/ULA 私网：默认拒绝，allowPrivate=true 时显式豁免（内网部署场景，
//     需管理员在添加表单知情勾选并落审计）。
//   - 域名主机：解析全部 A/AAAA 记录后逐一校验（尽力防 DNS 名称指向内网；
//     解析失败按拒绝处理，避免 DNS 故障时静默放行）。
func validateNodeAddress(value string, allowPrivate bool) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return fmt.Errorf("无效的节点地址: %s", value)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("节点地址 scheme 必须为 http 或 https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("无效的节点地址: %s", value)
	}
	ips := []net.IP{nil}
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if resolved, rerr := net.LookupIP(host); rerr == nil && len(resolved) > 0 {
		ips = resolved
	} else {
		return fmt.Errorf("节点地址主机名无法解析: %s", host)
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("节点地址不允许使用链路本地地址（含云元数据端点）")
		}
		if !allowPrivate && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
			return fmt.Errorf("节点地址为环回/内网地址，如确需内网部署请勾选「允许内网地址」豁免")
		}
	}
	return nil
}

// ---- 主动节点探活（HA 前置）----

// healthProbeTimeout 单次探活请求超时。
const healthProbeTimeout = 5 * time.Second

// nodeHealthFailures 记录每个节点连续探活失败次数（内存态，重启清零）。
var nodeHealthFailures = struct {
	sync.Mutex
	counts map[string]int
}{counts: map[string]int{}}

// nodeHealthFailureThreshold 连续失败达到该次数才判定离线，避免单次网络抖动误报。
const nodeHealthFailureThreshold = 3

// StartNodeHealthMonitor 启动后台主动探活循环：每 30s 直接 HTTP 探测每个
// 被控节点的 agent API。心跳超时检测（reconcileNodeOnlineStatuses）只在
// 有人读节点列表时被动触发；这个循环保证故障在无人访问面板时也能被
// 及时发现、落审计并（配置了 SMTP 时）邮件告警管理员。
func StartNodeHealthMonitor() {
	go func() {
		for {
			time.Sleep(30 * time.Second)
			probeAllNodes()
		}
	}()
}

// probeNode 对单个节点做一次 HTTP 探活（GET agent containers，同时验证认证可用）。
func probeNode(n config.Node) bool {
	if n.Address == "" {
		return false
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(n.Address, "/")+"/api/agent/containers", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+n.Token)
	resp, err := nodeHTTPClient(n, healthProbeTimeout).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 401/403 表示 agent 活着但认证配置有误——节点本身在线，但值得在状态里暴露
	return resp.StatusCode < 500
}

func probeAllNodes() {
	if !maintenanceLeaseActive() {
		return
	}
	config.AppConfigMu.RLock()
	nodes := append([]config.Node(nil), config.AppConfig.Nodes...)
	config.AppConfigMu.RUnlock()

	for _, n := range nodes {
		healthy := probeNode(n)
		nodeHealthFailures.Lock()
		if healthy {
			delete(nodeHealthFailures.counts, n.ID)
		} else {
			nodeHealthFailures.counts[n.ID]++
		}
		failures := nodeHealthFailures.counts[n.ID]
		nodeHealthFailures.Unlock()

		if healthy {
			// 恢复路径：之前被探活判为 offline（心跳停了但 HTTP 活着）→ 修正
			if n.Status == "offline" {
				config.UpdateNode(n.ID, func(x *config.Node) {
					x.Status = "online"
					x.LastSeen = time.Now().Format("2006-01-02 15:04:05")
				})
				config.AddAuditLog("node.health", n.Name, "节点恢复在线（主动探活成功）", "admin")
				notifyAdminByEmail("节点恢复在线："+n.Name,
					"被控节点 "+n.Name+"（"+n.Address+"）已恢复在线。", notify.SeverityInfo)
			}
			continue
		}

		if failures >= nodeHealthFailureThreshold && n.Status == "online" {
			// 连续多次失败且仍标记 online：标记离线 + 审计 + 邮件告警
			config.UpdateNode(n.ID, func(x *config.Node) { x.Status = "offline" })
			config.AddAuditLog("node.health", n.Name,
				fmt.Sprintf("节点连续 %d 次探活失败，标记离线", failures), "admin")
			notifyAdminByEmail("节点离线告警："+n.Name,
				fmt.Sprintf("被控节点 %s（%s）连续 %d 次探活失败，已标记离线。该节点上的容器可能不可用，请尽快检查。", n.Name, n.Address, failures),
				notify.SeverityCritical)
		}
	}
}

// notifyAdminByEmail 给主管理员发告警邮件（配置了 SMTP 时）。HA 事件属于
// 面板级告警，走管理员通道而非容器属主通道。
func notifyAdminByEmail(subject, body string, severity notify.Severity) {
	if !smtpConfigured() {
		return
	}
	adminEmail := func() string {
		config.AppConfigMu.RLock()
		defer config.AppConfigMu.RUnlock()
		// 管理员邮箱暂无专字段；退而求其次用 SMTP From 作为兜底收件人，
		// 避免告警静默丢失。
		return strings.TrimSpace(config.AppConfig.SMTPSettings.From)
	}()
	if adminEmail == "" {
		return
	}
	eventType := "node.health"
	go func() {
		failed := smtpDispatcher.Dispatch(notify.Message{
			Subject:   subject,
			Body:      body,
			Severity:  severity,
			EventType: eventType,
			Recipient: adminEmail,
		}, eventType)
		for _, err := range failed {
			fmt.Printf("notify: admin email failed: %v\n", err)
		}
	}()
}
