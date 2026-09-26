package api

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// console_guard.go 集中放置控制台（WebVNC / 跨节点级联）的 WebSocket 加固逻辑。
//
// 加固目标：
//  1. 防止半死连接泄漏 goroutine/内存：靠 ping/pong 保活 + 读超时回收；
//  2. 防止慢客户端把写入 pump 永久阻塞：每次写入带写超时；
//  3. 防止超大帧滥用内存：设置读上限；
//  4. 防止单容器被大量并发控制台拖垮：限制每容器并发会话数。
const (
	// consolePingInterval 服务端主动 ping 的间隔。
	consolePingInterval = 30 * time.Second
	// consolePongWait 是判定「对端仍存活」的窗口；pong 到达即顺延。
	// 必须大于 ping 间隔的 2 倍，避免把只是暂时静默的正常会话误杀。
	consolePongWait = 90 * time.Second
	// consoleWriteWait 单次写入的超时，避免慢客户端拖死写入方。
	consoleWriteWait = 10 * time.Second
	// consoleMaxMessage 单帧上限，远大于 VNC/SSH 的交互帧，仅作兜底防滥用。
	consoleMaxMessage = 4 << 20
)

// hardenWebSocket 为控制台 WebSocket 装上读上限、读超时与 ping/pong 保活。
// 返回的 stop 必须在会话结束时调用，否则保活 goroutine 会一直存活。
func hardenWebSocket(ws *websocket.Conn) (stop func()) {
	ws.SetReadLimit(consoleMaxMessage)
	_ = ws.SetReadDeadline(time.Now().Add(consolePongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(consolePongWait))
	})

	ticker := time.NewTicker(consolePingInterval)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// WriteControl 可与其它写入并发调用，因此不受数据写入锁约束。
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(consoleWriteWait)); err != nil {
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// writeConsoleMessage 在写锁内带写超时地发送一条消息。
func writeConsoleMessage(ws *websocket.Conn, mu *sync.Mutex, messageType int, data []byte) error {
	mu.Lock()
	defer mu.Unlock()
	_ = ws.SetWriteDeadline(time.Now().Add(consoleWriteWait))
	return ws.WriteMessage(messageType, data)
}

// maxConsoleSessionsPerContainer 单容器允许的并发控制台会话数。
const maxConsoleSessionsPerContainer = 4

// webVNCTicketLimit 未消费票据的数量上限，防止票据接口被刷爆内存。
const webVNCTicketLimit = 512

var consoleSessions = struct {
	sync.Mutex
	byContainer map[string]int
}{byContainer: map[string]int{}}

// acquireConsoleSession 尝试占用一个会话名额；超过上限返回 false。
func acquireConsoleSession(containerName string) bool {
	consoleSessions.Lock()
	defer consoleSessions.Unlock()
	if consoleSessions.byContainer[containerName] >= maxConsoleSessionsPerContainer {
		return false
	}
	consoleSessions.byContainer[containerName]++
	return true
}

// releaseConsoleSession 释放一个会话名额（与 acquireConsoleSession 配对）。
func releaseConsoleSession(containerName string) {
	consoleSessions.Lock()
	defer consoleSessions.Unlock()
	if n := consoleSessions.byContainer[containerName]; n <= 1 {
		delete(consoleSessions.byContainer, containerName)
		return
	}
	consoleSessions.byContainer[containerName]--
}