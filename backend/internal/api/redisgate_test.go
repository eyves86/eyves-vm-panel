package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/redisclient"
)

// testRedisAddr 返回集成测试用 Redis 地址；未设置 EYVESCLOUD_REDIS_TEST_ADDR
// 时跳过依赖真 Redis 的用例（单机/CI 无 Redis 也能跑完整回归）。
func testRedisAddrOrSkip(t testing.TB) *redisclient.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("EYVESCLOUD_REDIS_TEST_ADDR"))
	if addr == "" {
		t.Skip("EYVESCLOUD_REDIS_TEST_ADDR 未设置，跳过分布式模式用例")
	}
	c := redisclient.Open(addr, "")
	t.Cleanup(c.Close)
	return c
}

// useRedis 替换包级 redisShared 并在测试结束后还原为 nil（未启用分布式）。
func useRedis(t testing.TB, c *redisclient.Client) {
	t.Helper()
	previous := redisShared
	redisShared = c
	t.Cleanup(func() { redisShared = previous })
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// TestRedisGateFailOpenOffline 无 Redis 可达时必须 fail-open：限流与吊销复核
// 退化为进程内语义，不得因 Redis 故障拒绝所有请求。
func TestRedisGateFailOpenOffline(t *testing.T) {
	useRedis(t, redisclient.Open("127.0.0.1:1", "")) // 无人监听

	c := redisclient.Open("127.0.0.1:1", "")
	defer c.Close()
	if !redisConfirmSharedVersion(c, "k", 1) {
		t.Fatal("Redis 不可达时复核必须放行（fail-open）")
	}

	// 限流退化为进程内滑动窗口：计数落在内存 map。
	l := &loginRateLimiter{window: time.Minute, maxFail: 2, maxKeys: 100, fails: map[string][]time.Time{}}
	key := uniqueKey("t|")
	for i := 0; i < 2; i++ {
		if !l.allow(key) {
			t.Fatal("未达阈值必须放行")
		}
		l.recordFail(key)
	}
	if l.allow(key) {
		t.Fatal("达阈值后（进程内路径）必须拒绝")
	}
	l.reset(key)
	if !l.allow(key) {
		t.Fatal("reset 后必须放行")
	}
}

// TestCrossReplicaSubUserRevocation 钉死分布式吊销语义：副本 B 的陈旧快照
// 放行的令牌，会被副本 A 写入 Redis 的轮换/墓碑信号拦下。
func TestCrossReplicaSubUserRevocation(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	name := uniqueKey("su-x-")
	revKey := redisRevSubKey(name)
	t.Cleanup(func() { _ = c.Del(revKey) })

	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		SubUsers:  []config.SubUser{{Username: name, TokenVersion: 1}},
	}

	tok1 := newSubUserToken(name, nil, time.Now().Add(time.Hour), 1)
	if _, ok := claimsFromToken(tok1); !ok {
		t.Fatal("基线：内存与令牌版本一致必须有效")
	}

	// 副本 A 轮换到版本 2（本副本快照仍是 1 —— 模拟跨副本陈旧）。
	if err := c.SetEx(revKey, "2", 60); err != nil {
		t.Fatalf("SetEx: %v", err)
	}
	if _, ok := claimsFromToken(tok1); ok {
		t.Fatal("共享信号版本更新（2）后，陈旧快照放行的旧令牌必须失效")
	}

	// 信号过期（键不存在）→ fail-open 回内存判定。
	if err := c.Del(revKey); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, ok := claimsFromToken(tok1); !ok {
		t.Fatal("信号键不存在时必须回退内存判定（1==1 放行）")
	}

	// 删除墓碑：任何版本都拦下。
	if err := c.SetEx(revKey, redisRevTombstone, 60); err != nil {
		t.Fatalf("SetEx tombstone: %v", err)
	}
	if _, ok := claimsFromToken(tok1); ok {
		t.Fatal("墓碑必须拦下存量令牌")
	}
}

// TestCrossReplicaNotifyWrites 共写点广播助手写出的信号值必须与轮换后的
// 内存版本一致，且删除写墓碑。
func TestCrossReplicaNotifyWrites(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	name := uniqueKey("su-notify-")
	revKey := redisRevSubKey(name)
	t.Cleanup(func() { _ = c.Del(revKey) })

	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		SubUsers:  []config.SubUser{{Username: name, TokenVersion: 0}},
	}

	// 新建账号（v0）→ 写 "0"（清除可能的同名墓碑）。
	notifySubUserRotated(name)
	if v, ok, err := c.Get(revKey); err != nil || !ok || v != "0" {
		t.Fatalf("创建后信号应为 \"0\"，got %q ok=%v err=%v", v, ok, err)
	}
	tok0 := newSubUserToken(name, nil, time.Now().Add(time.Hour), 0)
	if _, ok := claimsFromToken(tok0); !ok {
		t.Fatal("新建账号 v0 令牌必须有效（信号 0 == 令牌 0）")
	}

	// 轮换到 v3 → 信号 "3"。
	config.AppConfigMu.Lock()
	if i, ok := config.FindSubUserIndexByNameUnlocked(name); ok {
		config.AppConfig.SubUsers[i].TokenVersion = 3
	}
	config.AppConfigMu.Unlock()
	notifySubUserRotated(name)
	if v, _, err := c.Get(revKey); err != nil || v != "3" {
		t.Fatalf("轮换后信号应为 \"3\"，got %q err=%v", v, err)
	}
	if _, ok := claimsFromToken(tok0); ok {
		t.Fatal("轮换后 v0 令牌必须被共享信号拦下")
	}

	// 删除 → 墓碑。
	notifySubUserDeleted(name)
	if v, _, err := c.Get(revKey); err != nil || v != redisRevTombstone {
		t.Fatalf("删除后应为墓碑 %q，got %q err=%v", redisRevTombstone, v, err)
	}
	if _, ok := claimsFromToken(newSubUserToken(name, nil, time.Now().Add(time.Hour), 3)); ok {
		t.Fatal("墓碑必须拦下 v3 令牌（即使本副本快照仍是 3）")
	}
}

// TestCrossReplicaAdminGlobalRevocation 主管理员全局版本信号。
func TestCrossReplicaAdminGlobalRevocation(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	admKey := redisRevAdminGlobalKey()
	// 全局键固定：先清掉旧值，测试结束再清理，避免污染其他用例。
	_ = c.Del(admKey)
	t.Cleanup(func() { _ = c.Del(admKey) })

	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{JWTSecret: "test-secret", AdminTokenVersion: 2}

	tok2, err := signAdminToken("admin", "", "", 2)
	if err != nil {
		t.Fatalf("signAdminToken: %v", err)
	}
	if _, ok := claimsFromToken(tok2); !ok {
		t.Fatal("基线：全局版本 2 的管理员令牌必须有效")
	}

	// 副本 A 已轮换到 3（本副本快照 2）→ 令牌被共享信号拦下。
	if err := c.SetEx(admKey, "3", 60); err != nil {
		t.Fatalf("SetEx: %v", err)
	}
	if _, ok := claimsFromToken(tok2); ok {
		t.Fatal("共享全局版本更新后，陈旧快照放行的管理员令牌必须失效")
	}

	// notifyAdminGlobalRotated 用变更后的内存版本刷新信号。
	config.AppConfigMu.Lock()
	config.AppConfig.AdminTokenVersion = 3
	config.AppConfigMu.Unlock()
	notifyAdminGlobalRotated()
	if v, _, err := c.Get(admKey); err != nil || v != "3" {
		t.Fatalf("广播后信号应为 \"3\"，got %q err=%v", v, err)
	}
	tok3, _ := signAdminToken("admin", "", "", 3)
	if _, ok := claimsFromToken(tok3); !ok {
		t.Fatal("全局版本 3（快照与信号一致）的令牌必须有效")
	}
}

// TestCrossReplicaAdminAccountRevocation 额外管理员：轮换信号 + 删除墓碑。
func TestCrossReplicaAdminAccountRevocation(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	name := uniqueKey("adm-notify-")
	revKey := redisRevAdminKey(name)
	t.Cleanup(func() { _ = c.Del(revKey) })

	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		Admins:    []config.AdminAccount{{ID: "adm-1", Username: name, TokenVersion: 1}},
	}

	tok1, _ := signAdminToken(name, "adm-1", "operator", 1)
	if _, ok := claimsFromToken(tok1); !ok {
		t.Fatal("基线：额外管理员令牌必须有效")
	}

	// 其他副本轮换到 2 → 本副本（快照 1）必须拦下。
	if err := c.SetEx(revKey, "2", 60); err != nil {
		t.Fatalf("SetEx: %v", err)
	}
	if _, ok := claimsFromToken(tok1); ok {
		t.Fatal("共享信号更新后额外管理员旧令牌必须失效")
	}

	// notifyAdminRotatedByID 以本副本（已轮换）内存为准广播。
	config.AppConfigMu.Lock()
	for i := range config.AppConfig.Admins {
		if config.AppConfig.Admins[i].ID == "adm-1" {
			config.AppConfig.Admins[i].TokenVersion = 2
		}
	}
	config.AppConfigMu.Unlock()
	notifyAdminRotatedByID("adm-1")
	if v, _, err := c.Get(revKey); err != nil || v != "2" {
		t.Fatalf("广播后信号应为 \"2\"，got %q err=%v", v, err)
	}
	tok2, _ := signAdminToken(name, "adm-1", "operator", 2)
	if _, ok := claimsFromToken(tok2); !ok {
		t.Fatal("轮换后的额外管理员令牌必须有效")
	}

	// 删除 → 墓碑拦下所有版本。
	notifyAdminDeleted(name)
	if _, ok := claimsFromToken(tok2); ok {
		t.Fatal("墓碑必须拦下额外管理员令牌")
	}
}

// TestLoginLimiterRedisShared 分布式登录限流：失败计数走 Redis 固定窗口，
// 进程内 map 保持零写入（否则多副本下阈值失真且内存无限增长）。
func TestLoginLimiterRedisShared(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	key := uniqueKey("t|")
	redisKey := redisLoginWindowKey(key)
	t.Cleanup(func() { _ = c.Del(redisKey) })

	l := &loginRateLimiter{window: time.Minute, maxFail: 2, maxKeys: 100, fails: map[string][]time.Time{}}
	if !l.allow(key) {
		t.Fatal("空计数必须放行")
	}
	l.recordFail(key)
	l.recordFail(key)
	if n, _, err := c.Get(redisKey); err != nil || n != "2" {
		t.Fatalf("Redis 计数应为 \"2\"，got %q err=%v", n, err)
	}
	if l.allow(key) {
		t.Fatal("达到 maxFail 后必须拒绝（跨进程计数）")
	}
	if len(l.fails) != 0 {
		t.Fatalf("Redis 模式下进程内 map 必须为空，got %d 条", len(l.fails))
	}
	l.reset(key)
	if !l.allow(key) {
		t.Fatal("reset（DEL）后必须放行")
	}
}

// TestAPIKeyRateLimitRedisShared API Key 分布式限流：n ≤ perMinute 放行，
// 超限 429；Redis 不可达时回退进程内滑动窗口。
func TestAPIKeyRateLimitRedisShared(t *testing.T) {
	c := testRedisAddrOrSkip(t)
	useRedis(t, c)
	id := uniqueKey("ak-")
	t.Cleanup(func() { _ = c.Del(redisAPIKeyWindowKey(id)) })

	key := &config.ApiKeyConfig{ID: id, RateLimitPerMinute: 2}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		if !enforceAPIKeyRateLimit(w, key) {
			t.Fatalf("第 %d 次请求（≤ perMinute）必须放行", i+1)
		}
	}
	w3 := httptest.NewRecorder()
	if enforceAPIKeyRateLimit(w3, key) {
		t.Fatal("第 3 次请求必须被限流")
	}
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("限流响应应为 429，got %d", w3.Code)
	}
	if w3.Header().Get("Retry-After") == "" {
		t.Fatal("限流响应必须带 Retry-After")
	}

	// 另一个 Key 不受影响（键按 ID 隔离）。
	other := &config.ApiKeyConfig{ID: uniqueKey("ak-"), RateLimitPerMinute: 2}
	t.Cleanup(func() { _ = c.Del(redisAPIKeyWindowKey(other.ID)) })
	wOther := httptest.NewRecorder()
	if !enforceAPIKeyRateLimit(wOther, other) {
		t.Fatal("其他 Key 不应被连带限流")
	}
}

// TestRateLimitKeysStable 契约守卫（ci-check #3 的进程内对应物）：限流键
// 不得包含攻击者可控输入，只允许 IP/内部 ID。
func TestRateLimitKeysStable(t *testing.T) {
	ipKey := "1.2.3.4|v2-admin"
	if !strings.HasPrefix(redisLoginWindowKey(ipKey), "eyves:rl:login:1.2.3.4|") {
		t.Fatal("登录限流键必须以固定前缀 + IP 开头")
	}
	if strings.Contains(redisLoginWindowKey("x|v2-client"), "password") {
		t.Fatal("限流键不得包含凭据类输入")
	}
	if redisAPIKeyWindowKey("abc") != "eyves:rl:apik:abc" {
		t.Fatal("API Key 限流键前缀必须稳定")
	}
}
