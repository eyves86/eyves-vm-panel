package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

func TestHashAPIKeyUsesSaltedArgon2idHash(t *testing.T) {
	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"

	h1, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}

	if h1 == h2 {
		t.Fatal("expected salted hashes to differ")
	}
	if !strings.HasPrefix(h1, apiKeyHashPrefix+"$") || !strings.HasPrefix(h2, apiKeyHashPrefix+"$") {
		t.Fatalf("expected argon2id hashes, got %q and %q", h1, h2)
	}
	if !verifyAPIKeyHash(raw, h1) || !verifyAPIKeyHash(raw, h2) {
		t.Fatal("argon2id hashes did not verify")
	}
	if verifyAPIKeyHash(raw+"x", h1) {
		t.Fatal("argon2id hash verified wrong key")
	}
}

func TestValidateApiKeyAllowsArgon2idAndUpdatesLastUsed(t *testing.T) {
	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{
		ApiKeys: []config.ApiKeyConfig{{
			ID:      "key1",
			Name:    "test",
			KeyHash: hash,
		}},
	}

	if !validateApiKey(raw, "127.0.0.1") {
		t.Fatal("validateApiKey rejected valid argon2id key")
	}
	updateApiKeyLastUsed(raw)
	if config.AppConfig.ApiKeys[0].LastUsed == "" {
		t.Fatal("LastUsed was not updated")
	}
}

func TestValidateApiKeyMigratesLegacyHash(t *testing.T) {
	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	config.AppConfig = &config.EyvescloudConfig{
		ApiKeys: []config.ApiKeyConfig{{
			ID:      "legacy",
			Name:    "legacy",
			KeyHash: legacyHashKey(raw),
		}},
	}

	if !validateApiKey(raw, "127.0.0.1") {
		t.Fatal("validateApiKey rejected valid legacy key")
	}
	migrated := config.AppConfig.ApiKeys[0].KeyHash
	if migrated == legacyHashKey(raw) {
		t.Fatal("legacy key hash was not migrated")
	}
	if !verifyAPIKeyHash(raw, migrated) {
		t.Fatal("migrated key hash does not verify")
	}
}

func TestValidateApiKeyAppliesIPWhitelist(t *testing.T) {
	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{
		ApiKeys: []config.ApiKeyConfig{{
			ID:          "key1",
			Name:        "test",
			KeyHash:     hash,
			IPWhitelist: "192.0.2.10",
		}},
	}

	if validateApiKey(raw, "198.51.100.10") {
		t.Fatal("validateApiKey allowed disallowed IP")
	}
	if !validateApiKey(raw, "192.0.2.10") {
		t.Fatal("validateApiKey rejected allowed IP")
	}
}

// TestApiKeyContainerBindingEnforced 锁定 API Key 的容器绑定语义：绑定到 uuidA 的
// Key 能访问 A、不能访问 B；未绑定的 Key（有 scope）按 H3 设计放行全部。
func TestApiKeyContainerBindingEnforced(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	config.AppConfig = &config.EyvescloudConfig{
		Containers: []config.Container{
			{UUID: "uuid-aaaa", Name: "c-a", ID: 1},
			{UUID: "uuid-bbbb", Name: "c-b", ID: 2},
		},
	}

	newRequest := func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v1/containers/uuid-aaaa", nil)
	}

	// 绑定到 A 的 Key：A 可访问，B 拒绝。
	boundReqs := newRequest()
	boundReqs = withAuthContext(boundReqs, AuthContext{Type: authTypeAPIKey, Scopes: []string{"container:read"}, ContainerUUIDs: []string{"uuid-aaaa"}})
	if !isContainerAllowedForRequest(boundReqs, "uuid-aaaa") {
		t.Fatal("bound key should access its bound container")
	}
	if isContainerAllowedForRequest(boundReqs, "uuid-bbbb") {
		t.Fatal("bound key must NOT access an unbound container")
	}

	// 未绑定但持 container:read scope 的 Key：按 H3 设计不设容器限制（限制交由 scope）。
	unboundReqs := newRequest()
	unboundReqs = withAuthContext(unboundReqs, AuthContext{Type: authTypeAPIKey, Scopes: []string{"container:read"}})
	if !isContainerAllowedForRequest(unboundReqs, "uuid-bbbb") {
		t.Fatal("unbound key uses scope-based control (H3 design), should allow")
	}
}

// TestApiKeyEmptyScopeUpgradeIsLegacyOnly 说明：空 scope 仅在“存量 legacy 密钥”时于认证
// 阶段升级为 “*”（向后兼容存量全权 Key）；新建 Key 的空 scope 会走只读默认 scope，不会全权。
func TestApiKeyCreateNormalizesEmptyScopeToReadOnlyFallback(t *testing.T) {
	got := normalizeRequestedScopes(nil, defaultApiKeyScopes)
	if len(got) != len(defaultApiKeyScopes) {
		t.Fatalf("empty create scopes should fall back to read-only defaults, got %v", got)
	}
	for _, def := range defaultApiKeyScopes {
		found := false
		for _, g := range got {
			if g == def {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("default scope %q missing from normalized create scopes (%v)", def, got)
		}
	}
}

// TestApiKeyFingerprintPreScreensInvalidKeys 验证 K1 修复：指纹预筛让不匹配的 Key
// 在 O(1) 处被排除，不触发 argon2 慢哈希；只有指纹命中的候选才做昂贵验证。
func TestApiKeyFingerprintPreScreensInvalidKeys(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}

	// 构造大量 Key：只有第一个真实、带指纹，其余均为带错误指纹的诱饵。
	keys := []config.ApiKeyConfig{{
		ID:             "key1",
		Name:           "test",
		KeyHash:        hash,
		KeyFingerprint: apiKeyFingerprint(raw),
	}}
	for i := 0; i < 20; i++ {
		keys = append(keys, config.ApiKeyConfig{
			ID:             fmt.Sprintf("decoy-%d", i),
			Name:           "decoy",
			KeyHash:        "invalid_on_purpose",
			KeyFingerprint: apiKeyFingerprint(raw) + "aa", // 错误的指纹
		})
	}
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: keys}

	// 一个毫不起眼的伪造 Key，指纹不命中任何存储指纹 → 不应被匹配。
	idx, _ := matchApiKey(raw + "x")
	if idx != -1 {
		t.Fatalf("wrong key should not match, got index %d", idx)
	}

	// 真实 Key 指纹命中 → 验证成功。
	idx, _ = matchApiKey(raw)
	if idx != 0 {
		t.Fatalf("valid key should match at index 0, got %d", idx)
	}
}
