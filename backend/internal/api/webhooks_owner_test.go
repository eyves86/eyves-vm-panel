package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"eyvescloud/internal/config"
)

// withActor 把 actor/type 注入到请求 ctx 中，模拟 authMiddleware 的副作用，
// 用于测试 currentActor / canAccessWebhook 的归属判定逻辑。
func withActor(r *http.Request, actor string, isAdmin bool) *http.Request {
	ctxType := "sub-user"
	if isAdmin {
		ctxType = "admin"
	}
	return withAuthContext(r, AuthContext{Type: ctxType, Actor: actor})
}

func TestCanAccessWebhook_AdminCanReadGlobal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhooks/wh-1", nil)
	req = withActor(req, "admin-1", true)
	wh := config.WebhookSubscription{ID: "wh-1", OwnerSubject: ""}
	if !canAccessWebhook(req, wh) {
		t.Fatal("admin should access global webhook")
	}
}

func TestCanAccessWebhook_SubUserCannotReadGlobal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhooks/wh-1", nil)
	req = withActor(req, "user-alice", false)
	wh := config.WebhookSubscription{ID: "wh-1", OwnerSubject: ""}
	if canAccessWebhook(req, wh) {
		t.Fatal("sub-user must not access admin-created global webhook")
	}
}

func TestCanAccessWebhook_OwnerReadOwn(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhooks/wh-1", nil)
	req = withActor(req, "user-alice", false)
	wh := config.WebhookSubscription{ID: "wh-1", OwnerSubject: "user-alice"}
	if !canAccessWebhook(req, wh) {
		t.Fatal("owner should access own webhook")
	}
}

func TestCanAccessWebhook_OtherUserBlocked(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhooks/wh-1", nil)
	req = withActor(req, "user-bob", false)
	wh := config.WebhookSubscription{ID: "wh-1", OwnerSubject: "user-alice"}
	if canAccessWebhook(req, wh) {
		t.Fatal("non-owner must not access another's webhook (EVE-001)")
	}
}

func TestUpdateWebhook_RejectsNonOwner(t *testing.T) {
	// 准备：存在一个 alice 创建的 webhook；bob 调用 DELETE 应被拒。
	config.AppConfigMu.Lock()
	config.AppConfig = &config.EyvescloudConfig{
		Webhooks: []config.WebhookSubscription{
			{ID: "wh-1", Name: "alice-hook", URL: "https://example.com/hook", OwnerSubject: "user-alice"},
		},
	}
	config.AppConfigMu.Unlock()

	req := httptest.NewRequest(http.MethodDelete, "/api/webhooks/wh-1", nil)
	req = withActor(req, "user-bob", false)
	rr := httptest.NewRecorder()
	deleteWebhook(rr, req, "wh-1")

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-owner delete, got %d body=%s", rr.Code, rr.Body.String())
	}

	// 全局 webhook 仍只有 alice 自己的那条，bob 未删除任何东西。
	config.AppConfigMu.RLock()
	if len(config.AppConfig.Webhooks) != 1 {
		t.Fatalf("webhook should not be deleted; len=%d", len(config.AppConfig.Webhooks))
	}
	config.AppConfigMu.RUnlock()
}

// canAccessWebhook 已通过 TestCanAccessWebhook_* 系列覆盖 admin/global/owner/other
// 四个判定组合，本处不再额外集成测试 update/delete 的 200 路径——它们会触发
// SQLite 写入（SaveConfig），需真实数据库，与单元测试关注点正交。

// 防止编译时方法未使用的告警（withActor / withAuthContext 在测试中被用到）。
var _ = context.Background