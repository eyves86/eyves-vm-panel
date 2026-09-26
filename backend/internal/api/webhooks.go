package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"net/url"
)

// 事件订阅（Webhook）端点：企业集成方订阅容器状态变更回调。
//
// 契约（类比 GitHub / Stripe Webhooks）：
//   - POST <callback_url>，Content-Type: application/json
//   - 载荷：{"event_type","timestamp","data":{"container_id","name","old_status","new_status"}}
//   - 签名：X-EyvesCloud-Signature: sha256=<hex(HMAC-SHA256(secret, body))>
//   - 事件 ID：X-EyvesCloud-Delivery（每次投递唯一，用于接收方去重）
//   - 接收方须返回 2xx；非 2xx 触发重试（3 次指数退避 1s/5s/25s）
//
// 可靠性：连续失败 10 次自动停用并记录原因，防止向故障端点无限重试造成雪崩。

const (
	webhookEventTypeStatusChanged = "container.status_changed"
	webhookAutoDisableThreshold   = 10
	webhookMaxRetries             = 3
	webhookHTTPTimeout            = 10 * time.Second
)

// HandleWebhooks 处理 /api/webhooks（列表 + 创建）。仅管理员。
func HandleWebhooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listWebhooks(w, r)
	case http.MethodPost:
		createWebhook(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleWebhookItem 处理 /api/webhooks/{id}（获取 + 更新 + 删除 + 测试投递）。
func HandleWebhookItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/webhooks/")
	if strings.HasPrefix(r.URL.Path, "/api/v1/webhooks/") {
		rest = strings.TrimPrefix(r.URL.Path, "/api/v1/webhooks/")
	}
	if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 && parts[1] == "test" {
		testWebhookDelivery(w, r, parts[0])
		return
	}
	if rest == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "webhook id required"})
		return
	}
	id := strings.SplitN(rest, "/", 2)[0]
	switch r.Method {
	case http.MethodGet:
		getWebhook(w, r, id)
	case http.MethodPut:
		updateWebhook(w, r, id)
	case http.MethodDelete:
		deleteWebhook(w, r, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// ---- helpers ----

func genWebhookID() string {
	return "wh-" + randomHex(12)
}

func genWebhookSecret() string {
	return "whsec_" + randomHex(24)
}

func findWebhook(id string) (config.WebhookSubscription, int, bool) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i, wh := range config.AppConfig.Webhooks {
		if wh.ID == id {
			return wh, i, true
		}
	}
	return config.WebhookSubscription{}, -1, false
}

// validateWebhookURLRequired 校验回调 URL：非空 + 复用 notify.go 中带 SSRF
// 防护的 validateWebhookURL（拒绝非 HTTP scheme / 内嵌凭据 / 链路本地地址）。
func validateWebhookURLRequired(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	if len(raw) > 2048 {
		return fmt.Errorf("url too long (max 2048 chars)")
	}
	return validateWebhookURL(raw)
}

// sanitizeWebhook 输出时隐藏 Secret（创建响应里明文返回一次，之后只回显前缀）。
func sanitizeWebhook(wh config.WebhookSubscription) config.WebhookSubscription {
	if wh.Secret != "" {
		if len(wh.Secret) > 12 {
			wh.Secret = wh.Secret[:12] + "..."
		}
	}
	return wh
}

// ---- CRUD ----

// listWebhooks 列出当前调用者可见的订阅：管理员看全部，sub-user / 受限
// API Key 仅看 OwnerSubject 与自身 Actor 匹配的订阅，避免泄漏他人配置。
func listWebhooks(w http.ResponseWriter, r *http.Request) {
	actor, isAdmin := currentActor(r)
	config.AppConfigMu.RLock()
	out := make([]config.WebhookSubscription, 0, len(config.AppConfig.Webhooks))
	for _, wh := range config.AppConfig.Webhooks {
		if !isAdmin && wh.OwnerSubject != "" && wh.OwnerSubject != actor {
			continue
		}
		out = append(out, sanitizeWebhook(wh))
	}
	config.AppConfigMu.RUnlock()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
}

// currentActor 提取当前请求的 Actor 与是否管理员，缺省（未认证）视为非 admin。
func currentActor(r *http.Request) (string, bool) {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		return "", false
	}
	return ctx.Actor, ctx.Type == "admin"
}

// canAccessWebhook 判断当前请求主体是否可以读/写指定 webhook：
//   - admin 可访问全部
//   - 拥有 OwnerSubject == actor 的订阅
//   - 全局订阅（OwnerSubject == ""）仅 admin 可访问
func canAccessWebhook(r *http.Request, wh config.WebhookSubscription) bool {
	actor, isAdmin := currentActor(r)
	if isAdmin {
		return true
	}
	if wh.OwnerSubject == "" {
		return false
	}
	return wh.OwnerSubject == actor
}

func getWebhook(w http.ResponseWriter, r *http.Request, id string) {
	wh, _, ok := findWebhook(id)
	if !ok {
		errResponse(w, http.StatusNotFound, "NOT_FOUND", "Webhook not found")
		return
	}
	if !canAccessWebhook(r, wh) {
		errResponse(w, http.StatusForbidden, "FORBIDDEN", "Not allowed to access this webhook")
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: sanitizeWebhook(wh)})
}

func createWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string   `json:"name"`
		URL        string   `json:"url"`
		EventTypes []string `json:"event_types"`
		Enabled    *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if len(req.Name) > 128 {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "name too long (max 128 chars)")
		return
	}
	if err := validateWebhookURLRequired(req.URL); err != nil {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if len(req.EventTypes) > 20 {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "too many event types (max 20)")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	// URL 去重：同一 URL 只允许一个订阅，防止重复投递。
	if _, _, exists := findWebhookByURL(strings.TrimSpace(req.URL)); exists {
		errResponse(w, http.StatusConflict, "ALREADY_EXISTS", "a webhook with this URL already exists")
		return
	}

	wh := config.WebhookSubscription{
		ID:           genWebhookID(),
		Name:         req.Name,
		URL:          strings.TrimSpace(req.URL),
		Secret:       genWebhookSecret(),
		EventTypes:   req.EventTypes,
		Enabled:      enabled,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if actor, isAdmin := currentActor(r); !isAdmin {
		// 非管理员创建的订阅绑定 OwnerSubject：仅本人或更高权限主体可改/删。
		wh.OwnerSubject = actor
		if ctx, ok := authContextFromRequest(r); ok {
			wh.OwnerType = ctx.Type
		}
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Webhooks = append(cfg.Webhooks, wh)
	})
	if err := config.SaveConfig(); err != nil {
		errResponse(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save config")
		return
	}
	auditRequest(r, "webhook.create", wh.Name, "id="+wh.ID+" url="+wh.URL, true, "")
	// Secret 仅在创建响应里明文返回一次（之后 sanitize 掩码）。
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Message: "Webhook created. Save the secret now — it will not be shown again.", Data: wh})
}

func findWebhookByURL(url string) (config.WebhookSubscription, int, bool) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i, wh := range config.AppConfig.Webhooks {
		if wh.URL == url {
			return wh, i, true
		}
	}
	return config.WebhookSubscription{}, -1, false
}

func updateWebhook(w http.ResponseWriter, r *http.Request, id string) {
	existing, _, ok := findWebhook(id)
	if !ok {
		errResponse(w, http.StatusNotFound, "NOT_FOUND", "Webhook not found")
		return
	}
	if !canAccessWebhook(r, existing) {
		errResponse(w, http.StatusForbidden, "FORBIDDEN", "Not allowed to update this webhook")
		return
	}
	var req struct {
		Name       *string  `json:"name,omitempty"`
		URL        *string  `json:"url,omitempty"`
		EventTypes []string `json:"event_types,omitempty"`
		Enabled    *bool    `json:"enabled,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	if req.URL != nil {
		if err := validateWebhookURLRequired(*req.URL); err != nil {
			errResponse(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		trimmed := strings.TrimSpace(*req.URL)
		// URL 变更时也要查重（排除自身）。
		if other, _, exists := findWebhookByURL(trimmed); exists && other.ID != id {
			errResponse(w, http.StatusConflict, "ALREADY_EXISTS", "a webhook with this URL already exists")
			return
		}
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Webhooks {
			if cfg.Webhooks[i].ID != id {
				continue
			}
			if req.Name != nil {
				name := strings.TrimSpace(*req.Name)
				if name != "" && len(name) <= 128 {
					cfg.Webhooks[i].Name = name
				}
			}
			if req.URL != nil {
				cfg.Webhooks[i].URL = strings.TrimSpace(*req.URL)
			}
			if req.EventTypes != nil {
				cfg.Webhooks[i].EventTypes = req.EventTypes
			}
			if req.Enabled != nil {
				cfg.Webhooks[i].Enabled = *req.Enabled
				if *req.Enabled {
					// 手动重新启用时清零失败计数与自动停用标记。
					cfg.Webhooks[i].ConsecutiveFailures = 0
					cfg.Webhooks[i].AutoDisabledReason = ""
				}
			}
			break
		}
	})
	if err := config.SaveConfig(); err != nil {
		errResponse(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save config")
		return
	}
	auditRequest(r, "webhook.update", existing.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func deleteWebhook(w http.ResponseWriter, r *http.Request, id string) {
	existing, _, ok := findWebhook(id)
	if !ok {
		errResponse(w, http.StatusNotFound, "NOT_FOUND", "Webhook not found")
		return
	}
	if !canAccessWebhook(r, existing) {
		errResponse(w, http.StatusForbidden, "FORBIDDEN", "Not allowed to delete this webhook")
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := make([]config.WebhookSubscription, 0, len(cfg.Webhooks))
		for _, wh := range cfg.Webhooks {
			if wh.ID != id {
				out = append(out, wh)
			}
		}
		cfg.Webhooks = out
	})
	if err := config.SaveConfig(); err != nil {
		errResponse(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save config")
		return
	}
	auditRequest(r, "webhook.delete", existing.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

// ---- 投递引擎 ----

// webhookEvent 是投递给订阅方的事件载荷。
type webhookEvent struct {
	EventType string                 `json:"event_type"`
	Timestamp string                 `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// webhookDeliveryOnce 执行一次投递尝试（签名 + POST + 状态码判定）。
func webhookDeliveryOnce(wh config.WebhookSubscription, evt webhookEvent, deliveryID string) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	parsed, err := url.ParseRequestURI(wh.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("invalid webhook URL")
	}
	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EyvesCloud-Webhook/1.0")
	req.Header.Set("X-EyvesCloud-Event", evt.EventType)
	req.Header.Set("X-EyvesCloud-Delivery", deliveryID)
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(body)
		req.Header.Set("X-EyvesCloud-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	client := &http.Client{Timeout: webhookHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback returned status %d", resp.StatusCode)
	}
	return nil
}

// webhookDeliver 带重试的投递：3 次指数退避（1s/5s/25s），全部失败后更新
// 失败计数；连续失败达到阈值自动停用订阅（记录原因）。成功则清零计数。
// 每次投递有独立 delivery ID，接收方可幂等去重。
func webhookDeliver(wh config.WebhookSubscription, evt webhookEvent) {
	deliveryID := "dlv-" + randomHex(12)
	backoffs := []time.Duration{0, time.Second, 5 * time.Second, 25 * time.Second}
	var lastErr error
	for attempt := 0; attempt < webhookMaxRetries; attempt++ {
		time.Sleep(backoffs[attempt])
		if err := webhookDeliveryOnce(wh, evt, deliveryID); err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}

	now := time.Now().UTC().Format(time.RFC3339)
	statusStr := "ok"
	if lastErr != nil {
		statusStr = "error: " + lastErr.Error()
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		// 异步投递可能在进程关闭 / 测试回收全局配置后仍被执行，此处必须容忍 nil，
		// 否则一次已排队的失败重试会以空指针崩溃拖垮整个进程。
		if cfg == nil {
			return
		}
		for i := range cfg.Webhooks {
			if cfg.Webhooks[i].ID != wh.ID {
				continue
			}
			cfg.Webhooks[i].LastDeliveryAt = now
			cfg.Webhooks[i].LastDeliveryStatus = statusStr
			if lastErr != nil {
				cfg.Webhooks[i].ConsecutiveFailures++
				if cfg.Webhooks[i].ConsecutiveFailures >= webhookAutoDisableThreshold {
					cfg.Webhooks[i].Enabled = false
					cfg.Webhooks[i].AutoDisabledReason = fmt.Sprintf(
						"auto-disabled after %d consecutive failed deliveries, last error: %v",
						cfg.Webhooks[i].ConsecutiveFailures, lastErr)
				}
			} else {
				cfg.Webhooks[i].ConsecutiveFailures = 0
			}
			break
		}
	})
	_ = config.SaveConfig()
}

// webhookDispatch 将事件分发给所有匹配的启用订阅（异步，不阻塞调用方）。
func webhookDispatch(evt webhookEvent) {
	config.AppConfigMu.RLock()
	targets := make([]config.WebhookSubscription, 0)
	if config.AppConfig != nil {
		for _, wh := range config.AppConfig.Webhooks {
			if !wh.Enabled {
				continue
			}
			// 事件类型过滤：订阅列表为空 = 订阅全部事件。
			if len(wh.EventTypes) > 0 {
				matched := false
				for _, t := range wh.EventTypes {
					if t == evt.EventType {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			targets = append(targets, wh)
		}
	}
	config.AppConfigMu.RUnlock()
	for _, wh := range targets {
		go webhookDeliver(wh, evt)
	}
}

// containerStatusChange 记录一次在配置锁内发生的状态迁移，
// 供锁外统一触发钩子（锁内触发会与 webhookDispatch 的读锁死锁）。
type containerStatusChange struct {
	id   int
	name string
	old  string
	new  string
}

// webhookStatusHook 把容器状态变更接入事件分发。
func webhookStatusHook(containerID int, name, oldStatus, newStatus string) {
	// Agent 模式守卫：agent 也运行 server.Run() 注册本钩子，但其本地
	// UpdateContainerStatus 与主控心跳同步（syncAgentContainers 锁外触发）
	// 会对同一事件双重投递。跨节点容器事件以主控心跳同步为唯一投递路径，
	// agent 侧（token 非空）一律跳过。
	if config.AgentToken() != "" {
		return
	}
	webhookDispatch(webhookEvent{
		EventType: webhookEventTypeStatusChanged,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data: map[string]interface{}{
			"container_id": containerID,
			"name":         name,
			"old_status":   oldStatus,
			"new_status":   newStatus,
		},
	})
}

var webhookHookOnce sync.Once

// StartWebhookEngine 注册容器状态钩子（幂等，可安全多次调用）。
func StartWebhookEngine() {
	webhookHookOnce.Do(func() {
		config.ContainerStatusHook = webhookStatusHook
	})
}

// testWebhookDelivery 手动触发一次测试投递（管理员验证端点连通性 + 验签逻辑）。
// POST /api/webhooks/{id}/test → 向订阅 URL 发送 event_type=webhook.test 事件。
func testWebhookDelivery(w http.ResponseWriter, r *http.Request, id string) {
	wh, _, ok := findWebhook(id)
	if !ok {
		errResponse(w, http.StatusNotFound, "NOT_FOUND", "Webhook not found")
		return
	}
	evt := webhookEvent{
		EventType: "webhook.test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data: map[string]interface{}{
			"message": "This is a test delivery from EyvesCloud",
		},
	}
	if err := webhookDeliveryOnce(wh, evt, "dlv-test-"+randomHex(8)); err != nil {
		auditRequest(r, "webhook.test", wh.Name, "id="+id+" err="+err.Error(), false, err.Error())
		errResponse(w, http.StatusBadGateway, "DELIVERY_FAILED", "Test delivery failed: "+err.Error())
		return
	}
	auditRequest(r, "webhook.test", wh.Name, "id="+id, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Test delivery succeeded"})
}
