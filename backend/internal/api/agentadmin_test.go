package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"eyvescloud/internal/config"
)

// agentAdminTestConfig 把 AppConfig 指向临时 DataDir（agent.json 隔离）。
func agentAdminTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{JWTSecret: "s", AdminUser: "admin", DataDir: dir}
	return dir
}

// fakeControllerServer 模拟目标主控的 /api/nodes/register。
func fakeControllerServer(t *testing.T, success bool, message string) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	var mu sync.Mutex
	payloads := &[]map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		*payloads = append(*payloads, payload)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if success {
			_, _ = w.Write([]byte(`{"success":true,"message":"ok","data":{"node_id":"node-new","token":"tok-new"}}`))
		} else {
			_, _ = w.Write([]byte(`{"success":false,"message":"` + message + `"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, payloads
}

func agentRegisterBody(controller, installKey string, allowInsecure bool) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"controller":          controller,
		"install_key":         installKey,
		"allow_insecure_http": allowInsecure,
	})
	return body
}

// TestAgentStatusUnregistered 未注册（无 agent.json）时 registered=false。
func TestAgentStatusUnregistered(t *testing.T) {
	agentAdminTestConfig(t)
	rec := httptest.NewRecorder()
	HandleAgentStatus(rec, asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/agent/status", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data AgentRegistration `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Registered {
		t.Fatalf("registered = true, want false")
	}
}

// TestAgentStatusRegistered 已注册时返回主控地址与节点凭据（面板「节点接入」数据源）。
func TestAgentStatusRegistered(t *testing.T) {
	agentAdminTestConfig(t)
	if err := config.SaveAgentConfig(&config.AgentConfig{
		Controller:        "http://154.16.173.136:8999",
		NodeID:            "node-1",
		Token:             "tok-1",
		Name:              "节点1",
		Address:           "http://10.0.0.5:8999",
		AllowInsecureHTTP: true, // agent 对 http 主控恒持久化豁免
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	HandleAgentStatus(rec, asAdminRequest(httptest.NewRequest(http.MethodGet, "/api/agent/status", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data AgentRegistration `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Data.Registered || resp.Data.Controller != "http://154.16.173.136:8999" ||
		resp.Data.NodeID != "node-1" || resp.Data.Token != "tok-1" || resp.Data.Name != "节点1" {
		t.Fatalf("unexpected status payload: %+v", resp.Data)
	}
	if !resp.Data.AllowInsecureHTTP {
		t.Fatalf("allow_insecure_http should persist for http controller")
	}
}

// TestAgentRegisterSwitchesController 核心场景：填写新主控地址 + install key，
// 注册成功后 agent.json 指向新主控（含新 node_id/token）。
func TestAgentRegisterSwitchesController(t *testing.T) {
	agentAdminTestConfig(t)
	// 已接入旧主控
	if err := config.SaveAgentConfig(&config.AgentConfig{
		Controller: "http://old-master:8999",
		NodeID:     "node-old",
		Token:      "tok-old",
		Name:       "节点1",
	}); err != nil {
		t.Fatal(err)
	}
	srv, payloads := fakeControllerServer(t, true, "")

	rec := httptest.NewRecorder()
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(agentRegisterBody(srv.URL, "key-new", true))))
	HandleAgentRegister(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	ac := config.LoadAgentConfig()
	if ac == nil {
		t.Fatal("agent.json missing after register")
	}
	if ac.Controller != srv.URL || ac.NodeID != "node-new" || ac.Token != "tok-new" {
		t.Fatalf("agent.json = %+v, want new controller/node-new/tok-new", ac)
	}
	// 名称/地址默认沿用旧配置
	if ac.Name != "节点1" {
		t.Fatalf("name = %q, want 节点1 (default keep)", ac.Name)
	}
	// 注册载荷带上了 install key 与默认名称
	if len(*payloads) != 1 {
		t.Fatalf("register calls = %d, want 1", len(*payloads))
	}
	if got := (*payloads)[0]["install_key"]; got != "key-new" {
		t.Fatalf("install_key = %q, want key-new", got)
	}
	if got := (*payloads)[0]["name"]; got != "节点1" {
		t.Fatalf("name payload = %q, want 节点1", got)
	}
}

// TestAgentRegisterRejectsHTTPWithoutConsent 明文 http 主控未勾选豁免 → 400。
func TestAgentRegisterRejectsHTTPWithoutConsent(t *testing.T) {
	agentAdminTestConfig(t)
	srv, _ := fakeControllerServer(t, true, "")

	rec := httptest.NewRecorder()
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(agentRegisterBody(srv.URL, "k", false))))
	HandleAgentRegister(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if config.LoadAgentConfig() != nil {
		t.Fatalf("agent.json must not be written on rejected request")
	}
}

// TestAgentRegisterPropagatesMasterError 主控返回失败（key 无效）→ 400 且透出原因。
func TestAgentRegisterPropagatesMasterError(t *testing.T) {
	agentAdminTestConfig(t)
	srv, _ := fakeControllerServer(t, false, "invalid install key")

	rec := httptest.NewRecorder()
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(agentRegisterBody(srv.URL, "bad", true))))
	HandleAgentRegister(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Message == "" || !bytes.Contains([]byte(resp.Message), []byte("invalid install key")) {
		t.Fatalf("message = %q, want master error propagated", resp.Message)
	}
	if config.LoadAgentConfig() != nil {
		t.Fatalf("agent.json must not be written when master rejects")
	}
}

// TestAgentRegisterValidatesInput 地址格式 / 空 key 校验。
func TestAgentRegisterValidatesInput(t *testing.T) {
	agentAdminTestConfig(t)
	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"no scheme", map[string]interface{}{"controller": "154.16.173.136:8999", "install_key": "k", "allow_insecure_http": true}},
		{"empty key", map[string]interface{}{"controller": "http://m:1", "install_key": "", "allow_insecure_http": true}},
	}
	for _, tc := range cases {
		body, _ := json.Marshal(tc.body)
		rec := httptest.NewRecorder()
		HandleAgentRegister(rec, asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(body))))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", tc.name, rec.Code)
		}
	}
}

// TestNodeAdoptEndToEnd 端到端「对接已有面板」全链路（两侧 handler 均为真实实现）：
// 主控 handleNodeAdopt → 被控面板 HandleAgentRegister（校验一次性对接密钥）→
// 回连主控 handleNodeRegister（install key 换 node token）→ agent.json 落盘、
// 对接密钥即焚、install key 一次性清空、节点自动上线；重放同一密钥被拒绝。
func TestNodeAdoptEndToEnd(t *testing.T) {
	agentAdminTestConfig(t)
	// 模拟全新安装：临时数据目录 + 首启 InitConfig（自动生成管理员凭据与
	// 24h 一次性对接密钥，即安装脚本 node-link 展示的那把）。
	dir := t.TempDir()
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		config.CloseConfigDB()
		if home, herr := os.UserHomeDir(); herr == nil {
			config.SetConfigPath(filepath.Join(home, ".eyvescloud", "config.json"))
		}
	})

	// 被控面板：真实 HandleAgentRegister（凭一次性对接密钥接受注册）。
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/register" {
			HandleAgentRegister(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(agentSrv.Close)

	// 主控面板：真实 handleNodeRegister（install_key → node token 的一次性兑换）。
	controllerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/nodes/register" {
			handleNodeRegister(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(controllerSrv.Close)

	// 首启生成的对接密钥即「节点管理 → 节点接入」展示的那把（64 位、24h 有效）。
	pairingKey := cfg.AgentPairingKey
	if len(pairingKey) != 64 {
		t.Fatalf("first boot should generate a 64-char pairing key, got %d chars: %q", len(pairingKey), pairingKey)
	}

	adoptBody, _ := json.Marshal(map[string]interface{}{
		"panel_url":     agentSrv.URL,
		"pairing_key":   pairingKey,
		"name":          "被控A",
		"allow_private": true, // httptest 环回地址需 SSRF 豁免
	})
	rec := httptest.NewRecorder()
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, controllerSrv.URL+"/api/nodes/adopt", bytes.NewReader(adoptBody)))
	handleNodeAdopt(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Node struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Data.Node.ID == "" {
		t.Fatalf("adopt not successful: %+v; body=%s", resp, rec.Body.String())
	}
	if resp.Data.Node.Status != "online" {
		t.Fatalf("node status = %q, want online (register marks it online before adopt replies)", resp.Data.Node.Status)
	}

	// 被控侧：agent.json 落盘且指向主控（node_id/token 与主控一致）。
	ac := config.LoadAgentConfig()
	if ac == nil {
		t.Fatal("agent.json not written")
	}
	if ac.Controller != controllerSrv.URL || ac.NodeID != resp.Data.Node.ID || ac.Token == "" || ac.Name != "被控A" {
		t.Fatalf("unexpected agent.json: %+v (want controller=%s node=%s)", ac, controllerSrv.URL, resp.Data.Node.ID)
	}
	if !ac.AllowInsecureHTTP {
		t.Fatal("allow_insecure_http should persist for http controller")
	}

	// 主控侧：install key 已一次性清空；节点 token 与被控持有一致。
	node, ok := config.FindNode(resp.Data.Node.ID)
	if !ok {
		t.Fatalf("controller node %s missing after adopt", resp.Data.Node.ID)
	}
	if node.Token != ac.Token {
		t.Fatalf("controller token %q != agent token %q", node.Token, ac.Token)
	}
	if node.InstallKey != "" {
		t.Fatalf("install key must be cleared after one-time use, got %q", node.InstallKey)
	}

	// 对接密钥一次性：成功对接后即焚毁，重放同一密钥被被控面板拒绝且回滚节点。
	if _, valid := agentPairingKeyValid(); valid {
		t.Fatal("pairing key must be consumed after successful adopt")
	}
	config.AppConfigMu.RLock()
	before := len(config.AppConfig.Nodes)
	config.AppConfigMu.RUnlock()
	rec2 := httptest.NewRecorder()
	handleNodeAdopt(rec2, asAdminRequest(httptest.NewRequest(http.MethodPost, controllerSrv.URL+"/api/nodes/adopt", bytes.NewReader(adoptBody))))
	if rec2.Code == http.StatusOK {
		t.Fatalf("replayed adopt must fail, got 200: %s", rec2.Body.String())
	}
	config.AppConfigMu.RLock()
	after := len(config.AppConfig.Nodes)
	config.AppConfigMu.RUnlock()
	if after != before {
		t.Fatalf("failed adopt must roll back node creation: %d nodes before replay, %d after", before, after)
	}
}
