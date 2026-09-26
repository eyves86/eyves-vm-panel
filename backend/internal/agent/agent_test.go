package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// fakeController 模拟主控的 /api/nodes/register 端点，记录注册载荷。
func fakeController(t *testing.T) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	var mu sync.Mutex
	payloads := &[]map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/register" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		*payloads = append(*payloads, payload)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"ok","data":{"node_id":"node-test","token":"tok-123"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, payloads
}

// runAgentWithTimeout 在 goroutine 中执行 agent.Run，超时则判定为阻塞失败。
// 这直接对应本次修复的缺陷形态：完整 agent 最后进入 server.Run() 永久阻塞。
func runAgentWithTimeout(t *testing.T, args []string, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		Run(args)
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(timeout):
		t.Fatalf("agent.Run did not return within %v (blocking regression)", timeout)
	}
}

// TestRegisterOnlyExitsAfterRegistration 验证 --register-only：
// 注册落盘后 Run 必须返回（而不是进入心跳/本地面板的永久阻塞），
// 且重复执行时跳过注册直接返回（幂等，安装脚本可安全重跑）。
func TestRegisterOnlyExitsAfterRegistration(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("EYVESCLOUD_DATA_DIR", dataDir)
	config.SetConfigPath(filepath.Join(dataDir, "config.json"))

	srv, payloads := fakeController(t)

	// 首次注册：必须落盘 agent.json 并返回
	runAgentWithTimeout(t, []string{
		"--register-only",
		"--controller=" + srv.URL,
		"--install-key=key-abc",
		"--name=test-node",
		"--allow-insecure-http",
	}, 15*time.Second)

	cfgPath := filepath.Join(dataDir, "agent.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("agent.json not written: %v", err)
	}
	var ac agentConfig
	if err := json.Unmarshal(data, &ac); err != nil {
		t.Fatalf("agent.json invalid: %v", err)
	}
	if ac.NodeID != "node-test" || ac.Token != "tok-123" {
		t.Fatalf("agent.json = %+v, want node-test/tok-123", ac)
	}
	if ac.Controller != srv.URL {
		t.Fatalf("controller = %q, want %q", ac.Controller, srv.URL)
	}
	if len(*payloads) != 1 {
		t.Fatalf("registration count = %d, want 1", len(*payloads))
	}
	if got := (*payloads)[0]["install_key"]; got != "key-abc" {
		t.Fatalf("install_key = %q, want key-abc", got)
	}
	if got := (*payloads)[0]["name"]; got != "test-node" {
		t.Fatalf("name = %q, want test-node", got)
	}

	// 重复执行（安装脚本重跑场景）：跳过注册（不再次调用主控）且直接返回
	runAgentWithTimeout(t, []string{
		"--register-only",
		"--controller=" + srv.URL,
		"--install-key=key-abc",
		"--name=test-node",
		"--allow-insecure-http",
	}, 15*time.Second)
	if len(*payloads) != 1 {
		t.Fatalf("re-run should skip registration, got %d calls", len(*payloads))
	}
}
