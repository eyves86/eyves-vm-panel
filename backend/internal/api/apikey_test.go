package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// TestApiKeyLastUsedThrottle 锁定 #110 修复：api_key.last_used 在**每一次** API 请求
// 上都会走，30 秒窗口内的重复请求不得再次落库（否则 10w 目录下每请求持写锁约 153ms）。
func TestApiKeyLastUsedThrottle(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	config.AppConfig = &config.EyvescloudConfig{ApiKeys: []config.ApiKeyConfig{{
		ID:       "key-throttle",
		Name:     "t",
		LastUsed: "OLD_SENTINEL",
	}}}

	// 窗口内：鉴权快照的 LastUsed 是「刚刚」→ 跳过落库，库内值原样不动。
	recent := time.Now().Format("2006-01-02 15:04:05")
	updateApiKeyLastUsedForKey(&config.ApiKeyConfig{ID: "key-throttle", LastUsed: recent}, "10.0.0.1")
	if got := config.AppConfig.ApiKeys[0]; got.LastUsed != "OLD_SENTINEL" || got.LastUsedIP != "" {
		t.Fatalf("窗口内重复请求不应落库，got LastUsed=%q LastUsedIP=%q", got.LastUsed, got.LastUsedIP)
	}

	// 窗口外：快照陈旧 → 必须更新。
	stale := time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")
	updateApiKeyLastUsedForKey(&config.ApiKeyConfig{ID: "key-throttle", LastUsed: stale}, "10.0.0.2")
	got := config.AppConfig.ApiKeys[0]
	if got.LastUsed == "OLD_SENTINEL" || got.LastUsed == "" {
		t.Fatalf("窗口外请求应更新 LastUsed，got %q", got.LastUsed)
	}
	if got.LastUsedIP != "10.0.0.2" {
		t.Fatalf("窗口外请求应更新 LastUsedIP，got %q", got.LastUsedIP)
	}
}

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

// TestApiKeyFingerprintPreScreensInvalidKeys 验证指纹筛选：不匹配的 Key 在 O(1) 处被
// 排除（错误 Key 拒绝、不触发任何验证）；指纹命中的即凭证成立（P4 切片 2 后为直验，
// 原语义为「命中后再做 argon2 验证」——两种实现下本测试都必须通过）。
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

// TestApiKeyFingerprintAloneAuthenticates 锁定 P4 切片 2 语义：128-bit 随机 Key 的
// SHA-256 指纹相等即凭证成立（GitHub/Stripe 惯例），不查 argon2 哈希——把 KeyHash
// 换成垃圾数据仍必须通过；错误 Key 指纹不命中必须拒绝。
func TestApiKeyFingerprintAloneAuthenticates(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: []config.ApiKeyConfig{{
		ID:             "key-fp",
		Name:           "fp",
		KeyHash:        "corrupted-on-purpose", // argon2 无法验证：证明指纹路径不查哈希
		KeyFingerprint: apiKeyFingerprint(raw),
	}}}

	if !validateApiKey(raw, "127.0.0.1") {
		t.Fatal("fingerprint match must authenticate without consulting the argon2 hash")
	}
	if validateApiKey(raw+"x", "127.0.0.1") {
		t.Fatal("wrong key must be rejected")
	}
}

// TestApiKeyBackfillsFingerprintOnFirstUse 旧 Key（无指纹、argon2 哈希）首用成功后
// 必须回填指纹，让后续请求走快验路径；回填后鉴权结果不受影响。
func TestApiKeyBackfillsFingerprintOnFirstUse(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: []config.ApiKeyConfig{{
		ID:      "key-legacy-argon2",
		Name:    "legacy",
		KeyHash: hash,
	}}}

	if !validateApiKey(raw, "127.0.0.1") {
		t.Fatal("valid legacy argon2 key must authenticate")
	}
	if got := config.AppConfig.ApiKeys[0].KeyFingerprint; got != apiKeyFingerprint(raw) {
		t.Fatalf("fingerprint must be backfilled on first use, got %q", got)
	}
	if !validateApiKey(raw, "127.0.0.1") {
		t.Fatal("key must still authenticate after fingerprint backfill")
	}
}

// BenchmarkMatchApiKeyFingerprint 量化鉴权热路径（P4 切片 2 后）：100 把 Key 中匹配
// 最后一把，单次校验应停留在微秒级；切片 2 之前每个有效 Key 请求都要跑一次
// argon2id（m=64MB,t=3，约百毫秒级 CPU）。
func BenchmarkMatchApiKeyFingerprint(b *testing.B) {
	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	keys := make([]config.ApiKeyConfig, 100)
	for i := range keys {
		keys[i] = config.ApiKeyConfig{ID: fmt.Sprintf("decoy-%d", i), KeyFingerprint: apiKeyFingerprint(raw + fmt.Sprint(i))}
	}
	keys[99].KeyFingerprint = apiKeyFingerprint(raw)
	previous := config.AppConfig
	b.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: keys}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if idx, _ := matchApiKey(raw); idx != 99 {
			b.Fatalf("expected index 99, got %d", idx)
		}
	}
}

// TestApiKeyRateLimitMiddlewareReturns429 验证单 key 超过 RateLimitPerMinute
// 时 ApiKeyMiddleware 直接返回 429 + Retry-After，不进入下游 handler。
func TestApiKeyRateLimitMiddlewareReturns429(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: []config.ApiKeyConfig{{
		ID:                 "key-rl",
		Name:               "rate-limited",
		KeyHash:            hash,
		KeyFingerprint:     apiKeyFingerprint(raw),
		RateLimitPerMinute: 2,
	}}}

	// 重置全局限流器，避免被其他测试污染
	prevLimiter := apiKeyLimiter
	t.Cleanup(func() { apiKeyLimiter = prevLimiter })
	apiKeyLimiter = newFreshLimiter()

	var downstreamCalled int
	downstream := func(w http.ResponseWriter, r *http.Request) {
		downstreamCalled++
		w.WriteHeader(http.StatusOK)
	}
	mw := ApiKeyMiddleware(downstream)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
		req.Header.Set("X-API-Key", raw)
		rr := httptest.NewRecorder()
		mw(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d code = %d, want 200", i+1, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	req.Header.Set("X-API-Key", raw)
	rr := httptest.NewRecorder()
	mw(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request code = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header must be set on 429")
	}
	if downstreamCalled != 2 {
		t.Fatalf("downstream called %d times, want 2", downstreamCalled)
	}
}

// TestAuthMiddlewareEnforcesAPIKeyRateLimit 锁定限流修复：真正的鉴权入口
// AuthMiddleware（所有路由都经它）也必须执行单 key 限流，否则限流形同虚设。
func TestAuthMiddlewareEnforcesAPIKeyRateLimit(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	raw := "eyvescloud_sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig = &config.EyvescloudConfig{ApiKeys: []config.ApiKeyConfig{{
		ID:                 "key-auth-rl",
		Name:               "rate-limited",
		KeyHash:            hash,
		KeyFingerprint:     apiKeyFingerprint(raw),
		RateLimitPerMinute: 1,
	}}}

	prevLimiter := apiKeyLimiter
	t.Cleanup(func() { apiKeyLimiter = prevLimiter })
	apiKeyLimiter = newFreshLimiter()

	var downstreamCalled int
	mw := AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		downstreamCalled++
		w.WriteHeader(http.StatusOK)
	})

	// 第 1 次放行
	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	req.Header.Set("X-API-Key", raw)
	rr := httptest.NewRecorder()
	mw(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("1st request code = %d, want 200", rr.Code)
	}
	// 第 2 次超限 → 429，且不进入下游
	req = httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	req.Header.Set("X-API-Key", raw)
	rr = httptest.NewRecorder()
	mw(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("2nd request code = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header must be set on 429")
	}
	if downstreamCalled != 1 {
		t.Fatalf("downstream called %d times, want 1", downstreamCalled)
	}
}
