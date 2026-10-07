package config

// store_collections_persist_test.go —— 集合类配置的持久化回归测试。
//
// 背景（#96 审计期间发现）：以下 7 个集合字段在 EyvescloudConfig 里存在、也有
// 创建/修改的 API（webhooks.go / ssh_keys.go / recipes.go / config.go 的
// ScheduledActions、node_groups.go、clusters、notify.go），但 **既不在
// metaEntries 的产出清单里，也没有独立表，loadConfigFromDB 也不读** ——
// 也就是说面板重启后它们凭空消失：管理员加的 SSH 公钥、事件回调、脚本模板、
// 定时启停、节点分组、集群、告警设置全部静默丢失（典型「假 UI」）。
//
// 生产库的 80 个 app_meta 键里确实没有任何 ssh_keys / webhooks / recipes /
// scheduled_actions / node_groups / clusters / notifications。

import (
	"strings"
	"testing"
)

// withCollectionsPersistTest 建一个干净配置库，并注册重启语义的清理。
func withCollectionsPersistTest(t *testing.T) {
	t.Helper()
	withSecGroupPersistTest(t)
}

// TestCollectionsSurviveRestart 上述 7 个集合必须跨重启保留。
func TestCollectionsSurviveRestart(t *testing.T) {
	withCollectionsPersistTest(t)

	if err := MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.SSHKeys = []SSHKey{{
			ID: "sk-1", Name: "laptop", PublicKey: "ssh-ed25519 AAAA test",
			Fingerprint: "SHA256:abc", Type: "admin", CreatedAt: "2026-01-01 00:00:00 UTC",
		}}
		cfg.Webhooks = []WebhookSubscription{{
			ID: "wh-1", Name: "hook", URL: "https://example.com/hook",
			Secret: "topsecret", Enabled: true, CreatedAt: "2026-01-01 00:00:00 UTC",
		}}
		cfg.Recipes = []Recipe{{
			ID: "recipe-1", Name: "hello", Script: "echo hi",
			OwnerID: "admin", OwnerType: "admin", Scope: "shared",
			CreatedAt: "2026-01-01 00:00:00 UTC", UpdatedAt: "2026-01-01 00:00:00 UTC",
		}}
		cfg.ScheduledActions = []ScheduledAction{{
			ID: "sa-1", ContainerID: 7, Type: "stop",
			ExecuteAt: "2026-01-02T00:00:00Z", Enabled: true,
		}}
		cfg.NodeGroups = []NodeGroup{{ID: "ng-1", Name: "pool"}}
		cfg.Clusters = []Cluster{{ID: "cl-1", Name: "cluster-a"}}
		cfg.Notifications = NotificationConfig{
			SecurityAlertsEnabled: true, MinSeverity: "high",
			WebhookURL: "https://example.com/alert",
		}
	}); err != nil {
		t.Fatalf("写入集合失败: %v", err)
	}

	cfg := reopenConfig(t)

	if n := len(cfg.SSHKeys); n != 1 {
		t.Errorf("重启后 SSHKeys 应保留 1 个，实际 %d 个", n)
	} else if cfg.SSHKeys[0].PublicKey != "ssh-ed25519 AAAA test" {
		t.Errorf("SSHKeys 内容不一致: %+v", cfg.SSHKeys[0])
	}
	if n := len(cfg.Webhooks); n != 1 {
		t.Errorf("重启后 Webhooks 应保留 1 个，实际 %d 个", n)
	} else if cfg.Webhooks[0].URL != "https://example.com/hook" {
		t.Errorf("Webhooks 内容不一致: %+v", cfg.Webhooks[0])
	}
	if n := len(cfg.Recipes); n != 1 {
		t.Errorf("重启后 Recipes 应保留 1 个，实际 %d 个", n)
	}
	if n := len(cfg.ScheduledActions); n != 1 {
		t.Errorf("重启后 ScheduledActions 应保留 1 个，实际 %d 个", n)
	}
	if n := len(cfg.NodeGroups); n != 1 {
		t.Errorf("重启后 NodeGroups 应保留 1 个，实际 %d 个", n)
	}
	if n := len(cfg.Clusters); n != 1 {
		t.Errorf("重启后 Clusters 应保留 1 个，实际 %d 个", n)
	}
	if !cfg.Notifications.SecurityAlertsEnabled || cfg.Notifications.MinSeverity != "high" {
		t.Errorf("Notifications 内容不一致: %+v", cfg.Notifications)
	}
}

// TestWebhookSecretEncryptedAtRest Webhook HMAC 密钥是可伪造回调的凭据，
// 落库必须是密文（enc:v1: 前缀），不能明文躺在 SQLite 里。
func TestWebhookSecretEncryptedAtRest(t *testing.T) {
	withCollectionsPersistTest(t)

	if err := MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.Webhooks = []WebhookSubscription{{
			ID: "wh-sec", Name: "h", URL: "https://example.com/h",
			Secret: "super-secret-value", Enabled: true,
		}}
	}); err != nil {
		t.Fatalf("写入 webhook 失败: %v", err)
	}

	dbMu.Lock()
	var raw string
	err := db.QueryRow(`SELECT value FROM app_meta WHERE key='webhooks'`).Scan(&raw)
	dbMu.Unlock()
	if err != nil {
		t.Fatalf("读取 webhooks 键失败: %v", err)
	}
	if !strings.Contains(raw, "enc:v1:") {
		t.Fatalf("webhook secret 未加密落库（明文形态）: %s", raw)
	}
	if strings.Contains(raw, "super-secret-value") {
		t.Fatalf("webhook secret 明文出现在库里: %s", raw)
	}

	// 重启后仍应还原为明文可用。
	cfg := reopenConfig(t)
	if len(cfg.Webhooks) != 1 || cfg.Webhooks[0].Secret != "super-secret-value" {
		t.Fatalf("重启后 webhook secret 未正确解密: %+v", cfg.Webhooks)
	}
}
