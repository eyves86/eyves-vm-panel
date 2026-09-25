package api

import (
	"bytes"
	"crypto/rand"
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
		if nodes == nil {
			nodes = []config.Node{}
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: nodes})
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
	case rest == "":
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeItem(w, r, nodeID) })(w, r)
	case rest == "heartbeat":
		handleNodeHeartbeat(w, r, nodeID)
	case rest == "maintenance" && r.Method == http.MethodPost:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeMaintenance(w, r, nodeID) })(w, r)
	case rest == "drain" && r.Method == http.MethodGet:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeDrain(w, r, nodeID) })(w, r)
	case rest == "install-script":
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeInstallScript(w, r, nodeID) })(w, r)
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
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeContainerAction(w, r, nodeID, strings.TrimPrefix(rest, "containers/")) })(w, r)
	case rest == "images" && r.Method == http.MethodGet:
		AdminMiddleware(func(w http.ResponseWriter, r *http.Request) { handleNodeImageProxy(w, r, nodeID, http.MethodGet, "/api/agent/images", nil) })(w, r)
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
	node, ok := config.FindNodeByInstallKey(strings.TrimSpace(req.InstallKey))
	if !ok || node.Token == "" {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid install key"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = node.Name
	}
	address := normalizeNodeAddress(req.Address)
	if address != "" {
		if err := validateNodeAddress(address); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	_, ok = config.UpdateNode(node.ID, func(n *config.Node) {
		n.Status = "online"
		n.LastSeen = time.Now().Format("2006-01-02 15:04:05")
		n.Name = name
		n.Version = req.Version
		if address != "" {
			n.Address = address
		}
	})
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"node_id": node.ID,
		"token":   node.Token,
		"name":    name,
	}})
}

func createNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "node-" + randomNodeSecret(4)
	}
	address := normalizeNodeAddress(req.Address)
	if address != "" {
		if err := validateNodeAddress(address); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	node := config.Node{
		ID:         newNodeID(),
		Name:       name,
		Address:    address,
		Token:      randomNodeSecret(32),
		InstallKey: randomNodeSecret(32),
		Status:     "pending",
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := config.AddNode(node); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "node.create", node.Name, "创建被控节点", true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: node})
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
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: node})
	case http.MethodDelete:
		if !requireScope(w, r, "node:write") {
			return
		}
		node, ok := config.FindNode(nodeID)
		if !ok {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
			return
		}
		config.RemoveNode(nodeID)
		auditRequest(r, "node.delete", node.Name, "删除被控节点", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Node deleted"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// handleNodeHeartbeat 由被控 agent 周期性上报资源与在线状态。
func handleNodeHeartbeat(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	token := tokenFromRequest(r)
	node, ok := config.FindNode(nodeID)
	if !ok || node.Token == "" || token != node.Token {
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	_, ok = config.UpdateNode(nodeID, func(n *config.Node) {
		n.LastSeen = time.Now().Format("2006-01-02 15:04:05")
		n.Status = "online"
		if req.Version != "" {
			n.Version = req.Version
		}
		if req.OSName != "" {
			n.OSName = req.OSName
		}
		if req.CPUCount > 0 {
			n.CPUCount = req.CPUCount
		}
		if req.RAMTotalMB > 0 {
			n.RAMTotalMB = req.RAMTotalMB
		}
		n.RAMUsedMB = req.RAMUsedMB
		if req.DiskTotalGB > 0 {
			n.DiskTotalGB = req.DiskTotalGB
		}
		n.DiskUsedGB = req.DiskUsedGB
		n.ContainerCount = req.ContainerCount
	})
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ok"})
}

// handleNodeInstallScript 生成被控一键安装脚本。
func handleNodeInstallScript(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "node:write") {
		return
	}
	node, ok := config.FindNode(nodeID)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Node not found"})
		return
	}
	controller := requestOrigin(r)
	if controller == "" {
		controller = "http://127.0.0.1:8999"
	}
	// 无需也无法在母侧确定子的真实地址：地址留空，由子端 agent 通过与母建连推算，或用脚本第 2 个参数显式指定。
	defaultAddr := ""
	script := buildAgentInstallScript(controller, node.InstallKey, node.Name, defaultAddr)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=eyvescloud-agent-%s.sh", node.Name))
	_, _ = w.Write([]byte(script))
}

func buildAgentInstallScript(controller, installKey, nodeName, defaultAddr string) string {
	name := shellDQ(nodeName)
	addr := shellDQ(defaultAddr)
	controllerEsc := shellDQ(controller)
	keyEsc := shellDQ(installKey)
	nameSQ := shellEscape(nodeName)
	addrSQ := shellEscape(defaultAddr)
	controllerSQ := shellEscape(controller)
	keySQ := shellEscape(installKey)
	insecureArg := ""
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(controller)), "http://") {
		// 主控为明文 http：agent 需显式开启不安全传输。
		insecureArg = "--allow-insecure-http"
	}
	return fmt.Sprintf(`#!/bin/bash
# EyvesCloud 被控节点一键安装脚本
# 用法: bash %s.sh [节点名称] [被控面板地址]
set -e

CONTROLLER="%s"
INSTALL_KEY="%s"
NODE_NAME="${1:-%s}"
NODE_ADDR="${2:-%s}"

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 权限运行: sudo bash $0"
  exit 1
fi

command -v curl >/dev/null 2>&1 || { echo "缺少 curl，请先安装"; exit 1; }

# 自动探测被控宿主架构，用于向主控请求架构匹配的二进制。
ARCH="$(uname -m 2>/dev/null || echo amd64)"
case "$ARCH" in
  x86_64|amd64) ARCH_NORM="amd64" ;;
  aarch64|arm64) ARCH_NORM="arm64" ;;
  *) echo "未知架构: $ARCH"; ARCH_NORM="" ;;
esac

echo "==> [1/3] 下载 EyvesCloud 二进制"
curl -fsSL -o /usr/local/bin/eyvescloud \
  "$CONTROLLER/api/nodes/binary?install_key=$INSTALL_KEY&arch=$ARCH_NORM"
chmod +x /usr/local/bin/eyvescloud

echo "==> [1/3] 校验二进制"
/usr/local/bin/eyvescloud --version >/dev/null 2>&1 \
  || { echo "下载的二进制无法运行，架构或产物不匹配（本机 $ARCH）"; exit 1; }

echo "==> [2/3] 注册被控节点"
/usr/local/bin/eyvescloud agent --controller="$CONTROLLER" --install-key="$INSTALL_KEY" --name="$NODE_NAME" --addr="$NODE_ADDR" %s || {
  echo "注册失败（已注册过的节点可忽略）";
}

echo "==> [3/3] 配置自启动服务"
cat > /etc/systemd/system/eyvescloud-agent.service <<'UNIT'
[Unit]
Description=EyvesCloud Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/eyvescloud agent --controller=%s --install-key=%s --name=%s --addr=%s %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now eyvescloud-agent

echo ""
echo "=============================================="
echo "  EyvesCloud Agent 安装完成"
echo "  节点: $NODE_NAME"
echo "  主控: $CONTROLLER"
echo "  请回到主控面板查看节点状态"
echo "=============================================="
`,
		nameSQ, controllerEsc, keyEsc, name, addr,
		insecureArg, controllerSQ, keySQ, nameSQ, addrSQ, insecureArg,
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
//   - Requires a valid `install_key` query parameter so that arbitrary hosts
//     cannot download the controller binary. The key is the same install key
//     the worker was created with and uses for registration.
//   - Honors an optional `arch` query parameter. When supplied and it does not
//     match the controller's own architecture, the request is rejected with a
//     clear error instead of handing out a binary that cannot run on the
//     worker (avoids silently installing a mismatched-build).
func HandleNodeBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	// Authenticate using the worker's install key.
	installKey := strings.TrimSpace(r.URL.Query().Get("install_key"))
	if installKey == "" {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "install_key query parameter required"})
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
	_, _ = io.Copy(w, f)
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
			"id":             n.ID,
			"name":           n.Name,
			"region_id":      n.RegionID,
			"ram_free_mb":    n.RAMTotalMB - n.RAMUsedMB,
			"disk_free_gb":   n.DiskTotalGB - n.DiskUsedGB,
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
	req, err := http.NewRequestWithContext(r.Context(), method, strings.TrimSuffix(node.Address, "/")+path, body)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	req.Header.Set("Authorization", "Bearer "+node.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

func normalizeNodeAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "http://" + value
	}
	return strings.TrimSuffix(value, "/")
}

func validateNodeAddress(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return fmt.Errorf("无效的节点地址: %s", value)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("节点地址 scheme 必须为 http 或 https")
	}
	return nil
}

func shellEscape(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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
	client := &http.Client{Timeout: healthProbeTimeout}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(n.Address, "/")+"/api/agent/containers", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+n.Token)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 401/403 表示 agent 活着但认证配置有误——节点本身在线，但值得在状态里暴露
	return resp.StatusCode < 500
}

func probeAllNodes() {
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
