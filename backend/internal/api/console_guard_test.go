package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestAcquireConsoleSessionEnforcesLimit 验证单容器并发控制台会话上限与释放逻辑。
func TestAcquireConsoleSessionEnforcesLimit(t *testing.T) {
	const name = "guard-test-container"
	t.Cleanup(func() {
		consoleSessions.Lock()
		delete(consoleSessions.byContainer, name)
		consoleSessions.Unlock()
	})

	for i := 0; i < maxConsoleSessionsPerContainer; i++ {
		if !acquireConsoleSession(name) {
			t.Fatalf("第 %d 个会话应被允许", i+1)
		}
	}
	if acquireConsoleSession(name) {
		t.Fatalf("超过上限 %d 后不应再允许新会话", maxConsoleSessionsPerContainer)
	}

	releaseConsoleSession(name)
	if !acquireConsoleSession(name) {
		t.Fatal("释放一个名额后应可再次获取")
	}
	releaseConsoleSession(name)
}

// TestReleaseConsoleSessionUnknownContainerIsSafe 验证释放不存在的会话不会出现负数或残留条目。
func TestReleaseConsoleSessionUnknownContainerIsSafe(t *testing.T) {
	releaseConsoleSession("never-acquired")
	consoleSessions.Lock()
	defer consoleSessions.Unlock()
	if _, ok := consoleSessions.byContainer["never-acquired"]; ok {
		t.Fatal("未获取过的容器不应留下计数条目")
	}
}

// TestHardenWebSocketEchoesTraffic 验证加固后正常收发不受影响（读上限/读超时/保活不破坏转发）。
func TestHardenWebSocketEchoesTraffic(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		stop := hardenWebSocket(ws)
		defer stop()
		var mu sync.Mutex
		for {
			messageType, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := writeConsoleMessage(ws, &mu, messageType, msg); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer client.Close()

	if err := client.WriteMessage(websocket.TextMessage, []byte("console-payload")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	messageType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if messageType != websocket.TextMessage || string(msg) != "console-payload" {
		t.Fatalf("回显异常: type=%d msg=%q", messageType, string(msg))
	}
}