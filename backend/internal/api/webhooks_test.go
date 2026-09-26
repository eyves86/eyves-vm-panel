package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// TestWebhookDeliveryContract 验证投递契约：POST + JSON 载荷 + HMAC 签名头 + 事件头。
func TestWebhookDeliveryContract(t *testing.T) {
	type receivedEvent struct {
		Headers http.Header
		Body    []byte
	}
	got := make(chan receivedEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- receivedEvent{Headers: r.Header.Clone(), Body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := config.WebhookSubscription{ID: "wh-test", URL: srv.URL, Secret: "whsec_test"}
	evt := webhookEvent{
		EventType: webhookEventTypeStatusChanged,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      map[string]interface{}{"container_id": 7, "name": "vm-7", "old_status": "stopped", "new_status": "running"},
	}
	if err := webhookDeliveryOnce(wh, evt, "dlv-test-1"); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}

	select {
	case recv := <-got:
		// 载荷结构
		var payload map[string]interface{}
		if err := json.Unmarshal(recv.Body, &payload); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		if payload["event_type"] != webhookEventTypeStatusChanged {
			t.Errorf("event_type = %v, want %s", payload["event_type"], webhookEventTypeStatusChanged)
		}
		data, _ := payload["data"].(map[string]interface{})
		if data["new_status"] != "running" {
			t.Errorf("data.new_status = %v, want running", data["new_status"])
		}
		// 契约头
		if ct := recv.Headers.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if e := recv.Headers.Get("X-EyvesCloud-Event"); e != webhookEventTypeStatusChanged {
			t.Errorf("X-EyvesCloud-Event = %q", e)
		}
		if d := recv.Headers.Get("X-EyvesCloud-Delivery"); d != "dlv-test-1" {
			t.Errorf("X-EyvesCloud-Delivery = %q, want dlv-test-1", d)
		}
		// HMAC 签名可验证
		sig := recv.Headers.Get("X-EyvesCloud-Signature")
		if !strings.HasPrefix(sig, "sha256=") {
			t.Fatalf("X-EyvesCloud-Signature = %q, want sha256=<hex> prefix", sig)
		}
		if !verifyTestSignature("whsec_test", recv.Body, strings.TrimPrefix(sig, "sha256=")) {
			t.Error("HMAC signature does not verify against raw body")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback server did not receive the delivery")
	}
}

// swapAppConfig 在配置锁内替换全局配置，并注册恢复。
// 直接给 config.AppConfig 赋值会与异步 webhook 投递（锁内读配置）产生数据竞争，
// 因此这里统一走 AppConfigMu。
func swapAppConfig(t *testing.T, cfg *config.EyvescloudConfig) {
	t.Helper()
	config.AppConfigMu.Lock()
	prev := config.AppConfig
	config.AppConfig = cfg
	config.AppConfigMu.Unlock()
	t.Cleanup(func() {
		config.AppConfigMu.Lock()
		config.AppConfig = prev
		config.AppConfigMu.Unlock()
	})
}

// TestWebhookDispatchRouting 验证事件类型过滤与禁用订阅不分发。
func TestWebhookDispatchRouting(t *testing.T) {
	hits := make(chan string, 4)
	match := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- "match"
	}))
	defer match.Close()
	nomatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- "nomatch"
	}))
	defer nomatch.Close()
	disabled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- "disabled"
	}))
	defer disabled.Close()

	swapAppConfig(t, &config.EyvescloudConfig{
		Webhooks: []config.WebhookSubscription{
			{ID: "wh-a", URL: match.URL, Enabled: true, EventTypes: []string{webhookEventTypeStatusChanged}},
			{ID: "wh-b", URL: nomatch.URL, Enabled: true, EventTypes: []string{"some.other.event"}},
			{ID: "wh-c", URL: disabled.URL, Enabled: false},
		},
	})

	webhookDispatch(webhookEvent{
		EventType: webhookEventTypeStatusChanged,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      map[string]interface{}{"container_id": 1},
	})

	select {
	case which := <-hits:
		if which != "match" {
			t.Errorf("unexpected endpoint hit: %s (want only 'match')", which)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no endpoint was hit")
	}
	// 只应有 match 收到；短暂等待确认没有多余投递。
	for {
		select {
		case which := <-hits:
			t.Errorf("endpoint %s should not have been hit", which)
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

// TestParseDryRun 验证 DryRun 标志解析的两种形式与容错。
func TestParseDryRun(t *testing.T) {
	mkReq := func(query string) *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/containers"+query, nil)
	}
	if !parseDryRun(map[string]json.RawMessage{"dry_run": json.RawMessage("true")}, mkReq("")) {
		t.Error("body dry_run:true should parse as true")
	}
	if !parseDryRun(map[string]json.RawMessage{"dry_run": json.RawMessage(`"true"`)}, mkReq("")) {
		t.Error("body dry_run:\"true\" (string) should parse as true")
	}
	if !parseDryRun(nil, mkReq("?dry_run=true")) {
		t.Error("query ?dry_run=true should parse as true")
	}
	if !parseDryRun(nil, mkReq("?dry_run=1")) {
		t.Error("query ?dry_run=1 should parse as true")
	}
	if parseDryRun(map[string]json.RawMessage{"dry_run": json.RawMessage("false")}, mkReq("?dry_run=false")) {
		t.Error("explicit false should parse as false")
	}
	if parseDryRun(nil, mkReq("")) {
		t.Error("absent flag should parse as false")
	}
}

// TestWebhookAgentModeGuard 验证 agent 模式不投递（主控心跳同步是唯一
// 投递路径），主控模式恢复投递。
func TestWebhookAgentModeGuard(t *testing.T) {
	hits := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- "hit"
	}))
	defer srv.Close()

	swapAppConfig(t, &config.EyvescloudConfig{
		Webhooks: []config.WebhookSubscription{
			{ID: "wh-guard", URL: srv.URL, Enabled: true},
		},
	})
	t.Cleanup(func() { config.SetAgentToken("") })

	// agent 模式（token 非空）：本地钩子不投递
	config.SetAgentToken("agent-token")
	webhookStatusHook(1, "vm-1", "stopped", "running")
	select {
	case <-hits:
		t.Error("agent mode should not deliver webhook locally")
	case <-time.After(300 * time.Millisecond):
	}

	// 主控模式（token 为空）：恢复投递
	config.SetAgentToken("")
	webhookStatusHook(1, "vm-1", "stopped", "running")
	select {
	case <-hits:
	case <-time.After(2 * time.Second):
		t.Error("master mode should deliver webhook")
	}
}

// verifyTestSignature 用与生产一致的 HMAC-SHA256 算法验证签名（测试侧独立实现）。
func verifyTestSignature(secret string, body []byte, hexSig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil) != nil && hex.EncodeToString(mac.Sum(nil)) == hexSig
}
