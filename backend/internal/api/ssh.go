package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type terminalResizeMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type webSSHTicket struct {
	ContainerName string
	SubUser       bool
	ExpiresAt     time.Time
}

var webSSHTickets = struct {
	sync.Mutex
	items map[string]webSSHTicket
}{items: map[string]webSSHTicket{}}

func HandleWebSSHTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	if !requireScope(w, r, "terminal:ssh") {
		return
	}
	var req struct {
		ContainerName string `json:"container_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Container name required"})
		return
	}
	if !isContainerAllowedForRequest(r, req.ContainerName) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	if !requireSubUserWrite(w, r) {
		return
	}
	c := config.FindContainerByName(req.ContainerName)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if isSubUserRequest(r) && c.PolicyBlocked {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: policyBlockedMessage(c)})
		return
	}

	ticket := randomHex(32)
	webSSHTickets.Lock()
	cleanupExpiredWebSSHTicketsLocked(time.Now())
	webSSHTickets.items[ticket] = webSSHTicket{
		ContainerName: req.ContainerName,
		SubUser:       isSubUserRequest(r),
		ExpiresAt:     time.Now().Add(60 * time.Second),
	}
	webSSHTickets.Unlock()

	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]string{"ticket": ticket},
	})
}

// HandleWebSSH proxies an SSH session to the browser over WebSocket.
func HandleWebSSH(w http.ResponseWriter, r *http.Request) {
	ticket := webSSHTicketFromRequest(r)
	if ticket == "" {
		http.Error(w, "ticket required", http.StatusUnauthorized)
		return
	}

	containerName := r.URL.Query().Get("container")
	if containerName == "" {
		http.Error(w, "container name required", http.StatusBadRequest)
		return
	}

	item, ok := consumeWebSSHTicket(ticket, containerName)
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}

	c := config.FindContainerByName(containerName)
	if c == nil {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}
	if item.SubUser && c.PolicyBlocked {
		http.Error(w, "虚拟机被策略临时封禁", http.StatusForbidden)
		return
	}
	if c.Status != "running" {
		http.Error(w, "container is not running", http.StatusBadRequest)
		return
	}
	// 容器内网 IP 不可用(空/link-local)时无法出网，ssh 也无法直连。
	// 这里先尝试从运行环境实时刷新 IP；若仍不可用，LXC 触发一次 DHCP 修复，
	// 让 eth0 从 lxcbr0 拿到正常网段地址，为后续 sshd 的安装提供外网连接。
	if !isUsableContainerIP(c.IP) {
		var ip string
		var err error
		if c.IsKVM() {
			ip, err = kvmManager.GetContainerIP(c.VirshName())
		} else {
			ip, err = lxcManager.GetContainerIP(c.LxcName())
		}
		if err == nil && isUsableContainerIP(ip) {
			config.MutateContainerByID(c.ID, func(l *config.Container) { l.IP = ip })
			if refreshed := config.FindContainer(c.ID); refreshed != nil {
				c = refreshed
			}
		}
	}
	if !isUsableContainerIP(c.IP) && !c.IsKVM() {
		if ip, err := lxcManager.EnsureContainerIPv4(c.ID); err == nil && isUsableContainerIP(ip) {
			config.MutateContainerByID(c.ID, func(l *config.Container) { l.IP = ip })
			if refreshed := config.FindContainer(c.ID); refreshed != nil {
				c = refreshed
			}
		}
	}
	if !isUsableContainerIP(c.IP) && c.IsKVM() {
		if err := kvmManager.EnsureSSH(c.ID); err == nil {
			if refreshed := config.FindContainer(c.ID); refreshed != nil {
				c = refreshed
			}
		}
	}
	if c.IP == "" {
		http.Error(w, "container ip is not available", http.StatusBadRequest)
		return
	}
	responseHeader := http.Header{}
	if protocol := webSSHTicketProtocol(r); protocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", protocol)
	}
	ws, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		log.Printf("WebSSH upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	if c.SSHPassword == "" {
		if c.IsKVM() {
			writeWebSocketText(ws, nil, "\r\nKVM SSH password is not available. Reinstall or reset after SSH is ready.\r\n")
			return
		}
		writeWebSocketText(ws, nil, "\r\nPreparing SSH service. This can take up to 90 seconds on first boot...\r\n")
		if err := lxcManager.EnsureSSH(c.ID); err != nil {
			writeWebSocketText(ws, nil, fmt.Sprintf("\r\nSSH auto setup failed: %v\r\n", err))
			return
		}
		if refreshed := config.FindContainer(c.ID); refreshed != nil {
			c = refreshed
		}
	}
	if c.SSHPassword == "" {
		writeWebSocketText(ws, nil, "\r\nSSH password is empty after auto setup\r\n")
		return
	}

	sshConfig := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.Password(c.SSHPassword),
		},
		HostKeyCallback: containerHostKeyCallback(c),
		Timeout:         4 * time.Second,
	}

	client, err := webSSHOpenSSH(c, sshConfig)
	if err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nWebSSH connection failed: %v\r\n", err))
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to create SSH session: %v\r\n", err))
		return
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to open SSH stdin: %v\r\n", err))
		return
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to open SSH stdout: %v\r\n", err))
		return
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to open SSH stderr: %v\r\n", err))
		return
	}

	if err := session.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to request pty: %v\r\n", err))
		return
	}

	if err := session.Shell(); err != nil {
		writeWebSocketText(ws, nil, fmt.Sprintf("\r\nFailed to start shell: %v\r\n", err))
		return
	}
	writeWebSocketText(ws, nil, "\r\nSSH shell ready. Press Enter if the prompt is not visible.\r\n")
	_, _ = stdin.Write([]byte("\n"))

	log.Printf("WebSSH connected for container %s", containerName)

	done := make(chan struct{}, 3)
	var writeMu sync.Mutex

	go streamSSHOutput(ws, &writeMu, stdout, done)
	go streamSSHOutput(ws, &writeMu, stderr, done)

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}

			if messageType == websocket.TextMessage {
				var resize terminalResizeMessage
				if err := json.Unmarshal(msg, &resize); err == nil && resize.Type == "resize" {
					if resize.Rows > 0 && resize.Cols > 0 {
						_ = session.WindowChange(resize.Rows, resize.Cols)
					}
					continue
				}
			}

			if _, err := stdin.Write(msg); err != nil {
				return
			}
		}
	}()

	<-done
	_ = session.Signal(ssh.SIGTERM)
	log.Printf("WebSSH disconnected for container %s", containerName)
}

func containerHostKeyCallback(c *config.Container) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		sum := sha256.Sum256(key.Marshal())
		fingerprint := hex.EncodeToString(sum[:])
		if c.SSHHostKey != "" && c.SSHHostKey != fingerprint {
			return fmt.Errorf("container SSH host key mismatch")
		}
		if c.SSHHostKey == "" {
			config.MutateContainerByID(c.ID, func(l *config.Container) {
				if l.SSHHostKey == "" {
					l.SSHHostKey = fingerprint
				}
			})
		}
		return nil
	}
}

func webSSHTicketFromRequest(r *http.Request) string {
	for _, protocol := range websocket.Subprotocols(r) {
		const prefix = "eyvescloud-ticket."
		if len(protocol) > len(prefix) && protocol[:len(prefix)] == prefix {
			return protocol[len(prefix):]
		}
	}
	return ""
}

func webSSHTicketProtocol(r *http.Request) string {
	for _, protocol := range websocket.Subprotocols(r) {
		const prefix = "eyvescloud-ticket."
		if len(protocol) > len(prefix) && protocol[:len(prefix)] == prefix {
			return protocol
		}
	}
	return ""
}

func streamSSHOutput(ws *websocket.Conn, writeMu *sync.Mutex, src io.Reader, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	buf := make([]byte, 8192)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			writeMu.Lock()
			writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			writeMu.Unlock()
			if writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func writeWebSocketText(ws *websocket.Conn, writeMu *sync.Mutex, msg string) {
	if writeMu != nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}
	_ = ws.WriteMessage(websocket.TextMessage, []byte(msg))
}

func consumeWebSSHTicket(ticket, containerName string) (webSSHTicket, bool) {
	now := time.Now()
	webSSHTickets.Lock()
	defer webSSHTickets.Unlock()
	cleanupExpiredWebSSHTicketsLocked(now)
	item, ok := webSSHTickets.items[ticket]
	if !ok {
		return webSSHTicket{}, false
	}
	delete(webSSHTickets.items, ticket)
	return item, item.ContainerName == containerName && now.Before(item.ExpiresAt)
}

func cleanupExpiredWebSSHTicketsLocked(now time.Time) {
	for ticket, item := range webSSHTickets.items {
		if !now.Before(item.ExpiresAt) {
			delete(webSSHTickets.items, ticket)
		}
	}
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// containerIPLooksUnusable 判断容器 IP 是否不可用于直连（link-local/loopback/空）。
// 在 DHCP 尚未从 lxcbr0 拿到正常网段地址时，容器 eth0 常落到 169.254.x（APIPA），
// 宿主对它不可路由，导致 WebSSH 永远卡在 preparing。
func containerIPLooksUnusable(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsLinkLocalUnicast() || parsed.IsLoopback() || parsed.IsUnspecified()
}

// isUsableContainerIP 判断 IP 是否既非空也不属不可用地址族。
func isUsableContainerIP(ip string) bool {
	return !containerIPLooksUnusable(ip)
}

// webSSHOpenSSH 以「多候选地址」方式建立到容器的 SSH 连接。
// 候选顺序：
//  1. 容器内网 IPv4 c.IP:22（正常场景）
//  2. 若 c.IP 不可用，尝试动态刷新容器 IP 后再连
//  3. 最终回退到宿主回环 127.0.0.1:<SSHPort>——DNAT 已将宿主管理端口转发到容器 22，
//     该路径不经容器 link-local 地址，宿主机上必然可达。
func webSSHOpenSSH(c *config.Container, sshConfig *ssh.ClientConfig) (*ssh.Client, error) {
	candidates := make([]string, 0, 4)
	if isUsableContainerIP(c.IP) {
		candidates = append(candidates, net.JoinHostPort(c.IP, "22"))
	}
	if c.IsKVM() {
		if ip, err := kvmManager.GetContainerIP(c.VirshName()); err == nil && isUsableContainerIP(ip) {
			config.MutateContainerByID(c.ID, func(l *config.Container) { l.IP = ip })
			candidates = append(candidates, net.JoinHostPort(ip, "22"))
		}
	} else {
		if usable := isUsableContainerIP(c.IP); !usable {
			// c.IP 不可用(link-local/空)时，先在容器内触发一次 DHCP 修复，
			// 让 eth0 尽量拿到 lxcbr0 网段的正常地址。
			if ip, err := lxcManager.EnsureContainerIPv4(c.ID); err == nil && isUsableContainerIP(ip) {
				config.MutateContainerByID(c.ID, func(l *config.Container) { l.IP = ip })
				candidates = append(candidates, net.JoinHostPort(ip, "22"))
			}
		}
		if ip, err := lxcManager.GetContainerIP(c.LxcName()); err == nil && isUsableContainerIP(ip) {
			config.MutateContainerByID(c.ID, func(l *config.Container) { l.IP = ip })
			candidates = append(candidates, net.JoinHostPort(ip, "22"))
		}
	}
	// 最后回退到宿主回环 + DNAT 管理端口。
	if c.SSHPort > 0 && c.SSHPort <= 65535 {
		candidates = append(candidates, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", c.SSHPort)))
	}

	var lastErr error
	for _, addr := range candidates {
		client, err := dialSSHWithTimeout(addr, sshConfig, 8*time.Second)
		if err == nil {
			return client, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("container has no reachable SSH address")
	}
	return nil, lastErr
}

// dialSSHWithTimeout 在「TCP 拨号 + SSH 握手」整体上施加 deadline。
// x/crypto/ssh 的 ClientConfig.Timeout 只覆盖 TCP 拨号阶段，不覆盖 SSH
// 版本交换与密钥交换（KEX）。当目标 TCP 可连接但 sshd 不响应握手时，
// ssh.Dial 会无限阻塞，导致 WebSSH 永远卡在 "preparing" 界面。
// 因此这里在已建立的连接上设置绝对 deadline，超时则关闭并返回错误。
func dialSSHWithTimeout(addr string, sshConfig *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	deadline := time.Now().Add(timeout)

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	// 覆盖握手阶段：任何超过 deadline 的读写都会被 io 层以 timeout 终止。
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetDeadline(deadline)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	// 握手已完成，取消 deadline，避免影响后续交互会话。
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetDeadline(time.Time{})
	}

	return ssh.NewClient(c, chans, reqs), nil
}
