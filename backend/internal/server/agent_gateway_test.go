package server

// agent_gateway_test.go —— 独立 Agent 网关（入口削峰）监听器回归。
//
// 锁定语义：网关监听器只服务「节点 → 主控」心跳入站；面板/管理/API v2 等一切
// 其它路径在该端口上必须 404，否则把海量节点入站从面板剥离反而扩大了暴露面。
// 这里用真实 TCP 监听（127.0.0.1:0）验证，而不是只测 mux —— 中间件链与监听器
// 都在链路上。

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// gatewayBaseURL 在随机端口起一个真实网关监听器，返回 base URL。
func gatewayBaseURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	serveAgentGateway(ln, nil)
	t.Cleanup(func() { _ = ln.Close() })
	return "http://" + ln.Addr().String()
}

func TestAgentGatewayListenerExposesOnlyHeartbeat(t *testing.T) {
	base := gatewayBaseURL(t)
	client := &http.Client{Timeout: 5 * time.Second}

	// 主动轮询健康检查，等待后台 Serve goroutine 就绪（避免偶发 connection refused）。
	waitFor(t, client, base+"/api/agent/gateway/health")

	t.Run("健康检查 200", func(t *testing.T) {
		resp, err := client.Get(base + "/api/agent/gateway/health")
		if err != nil {
			t.Fatalf("请求失败：%v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("健康检查应 200，实际 %d", resp.StatusCode)
		}
	})

	t.Run("心跳路由可达（无有效节点令牌 → 401，而非 404）", func(t *testing.T) {
		resp, err := client.Post(base+"/api/nodes/no-such-node/heartbeat", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("请求失败：%v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("心跳路由应可达并因令牌无效返回 401，实际 %d", resp.StatusCode)
		}
	})

	// 网关端口绝不暴露面板与管理面。
	for _, path := range []string{
		"/",
		"/api/v1/dashboard",
		"/api/v2/instances/1/metrics",
		"/api/agent/containers",
		"/api/nodes/no-such-node/containers",
		"/api/nodes/no-such-node/install-script",
		"/admin-abc/containers",
	} {
		path := path
		t.Run("非心跳路径 404: "+path, func(t *testing.T) {
			resp, err := client.Get(base + path)
			if err != nil {
				t.Fatalf("请求失败：%v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("网关不应暴露 %s，应 404 实际 %d", path, resp.StatusCode)
			}
		})
	}
}

// waitFor 轮询 url 直到得到任意 HTTP 响应（含 4xx/5xx）或超时。
func waitFor(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待网关就绪超时：%s", url)
}
