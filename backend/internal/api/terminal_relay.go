package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"

	"github.com/gorilla/websocket"
)

// terminal_relay.go 实现主控侧的跨节点控制台级联：
//
//	浏览器 ──WS── 主控 /api/ssh|/api/vnc ──WS── agent /api/ssh|/api/vnc
//
// 容器 NodeID 非空时，主控先向 agent 申请一次性票据（node token 认证），
// 再以 WebSocket 客户端身份拨 agent，与浏览器连接做双向透明转发。
// 浏览器侧票据/subprotocol 流程与本机容器完全一致，前端零改动。

// relayTerminalToNode 处理跨节点 WebSSH/VNC 的完整级联。
// kind: "ssh" 或 "vnc"。返回 error 时已向浏览器写出 http.Error。
func relayTerminalToNode(w http.ResponseWriter, r *http.Request, c config.Container, node config.Node, kind, containerName, browserProtocol string) {
	// 1) 向 agent 申请一次性票据
	ticketPath := "/api/agent/ssh-ticket"
	if kind == "vnc" {
		ticketPath = "/api/agent/vnc-ticket"
	}
	body, _ := json.Marshal(map[string]string{"container_name": containerName})
	data, status, err := proxyNodeRequest(r, node, http.MethodPost, ticketPath, strings.NewReader(string(body)))
	if err != nil || status != http.StatusOK {
		msg := fmt.Sprintf("获取被控节点控制台票据失败 (HTTP %d): %v", status, err)
		http.Error(w, msg, http.StatusBadGateway)
		return
	}
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || !out.Success || out.Data.Ticket == "" {
		msg := out.Message
		if msg == "" {
			msg = "被控节点票据响应无效"
		}
		http.Error(w, msg, http.StatusBadGateway)
		return
	}

	// 2) 构造 agent WS URL（http→ws, https→wss）
	base := strings.TrimSuffix(node.Address, "/")
	wsBase := strings.Replace(base, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)
	agentURL := wsBase + "/api/" + kind + "?container=" + url.QueryEscape(containerName)
	if kind == "vnc" {
		// VNC 票据支持 query 参数
		agentURL += "&ticket=" + url.QueryEscape(out.Data.Ticket)
	}

	// 3) 拨号 agent
	dialHeader := http.Header{}
	if kind == "ssh" {
		// SSH 票据走 subprotocol（webSSHTicketFromRequest 只认子协议）
		dialHeader.Set("Sec-WebSocket-Protocol", "eyvescloud-ticket."+out.Data.Ticket)
	}
	agentWS, _, err := relayDialer.Dial(agentURL, dialHeader)
	if err != nil {
		log.Printf("terminal relay: dial agent %s failed: %v", agentURL, err)
		http.Error(w, "连接被控节点控制台失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer agentWS.Close()

	// 4) 升级浏览器连接（沿用原 subprotocol 响应，前端握手语义不变）
	responseHeader := http.Header{}
	if browserProtocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", browserProtocol)
	}
	browserWS, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		log.Printf("terminal relay: browser upgrade failed: %v", err)
		return
	}
	defer browserWS.Close()

	log.Printf("terminal relay: %s console for %s via node %s", kind, containerName, node.Name)

	// 5) 双向透明转发
	relayWebSockets(browserWS, agentWS)
}

// relayWebSockets 在两个 WebSocket 连接间做双向透明转发（保留消息类型）。
func relayWebSockets(a, b *websocket.Conn) {
	done := make(chan struct{}, 2)
	var aMu, bMu sync.Mutex

	pump := func(dst *websocket.Conn, dstMu *sync.Mutex, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			msgType, msg, err := src.ReadMessage()
			if err != nil {
				return
			}
			dstMu.Lock()
			writeErr := dst.WriteMessage(msgType, msg)
			dstMu.Unlock()
			if writeErr != nil {
				return
			}
		}
	}

	go pump(b, &bMu, a) // 浏览器 → agent
	go pump(a, &aMu, b) // agent → 浏览器

	<-done
	_ = a.Close()
	_ = b.Close()
	// 等第二个 pump 退出（WriteMessage 因对端关闭会立即失败）
	<-done
}

// nodeForContainer 返回容器所属节点（跨节点终端路由辅助）。
func nodeForContainer(c *config.Container) (config.Node, bool) {
	if c == nil || c.NodeID == "" {
		return config.Node{}, false
	}
	node, ok := config.FindNode(c.NodeID)
	if !ok || node.Address == "" {
		return config.Node{}, false
	}
	return node, true
}

// relayDialTimeout 是主控拨 agent WS 的超时（与 HTTP 代理一致量级）。
const relayDialTimeout = 15 * time.Second

// relayDialer 是主控拨 agent 控制台的 WS 客户端。
var relayDialer = &websocket.Dialer{HandshakeTimeout: relayDialTimeout}
