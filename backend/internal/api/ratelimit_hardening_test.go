package api

// ratelimit_hardening_test.go —— 限流桶键加固 + v2 CSRF 纵深防御回归测试。
//
// 背景（审计复核发现）：
//
//	v1 的三个登录入口把「攻击者可控的输入串」直接拼进限流桶键——
//	  管理员登录   ip|admin:<username>
//	  子用户登录   ip|user:<username>
//	  访问码登录   ip|code:<code>
//	攻击者轮换用户名大小写 / 邮箱形态，或每猜一个访问码就换一个值，
//	即可各自拿到一份全新配额，等于绕过限流（渗透实测：47 个随机访问码 0 次 429）。
//	修复后桶键只按 IP 计；本文件把这个契约钉死，防止回退。
//
//	另：v2 整棵路由树此前没有调用 cookieCSRFGuard（全库只有 v1 的
//	AuthMiddleware 调），这里一并锁定。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// postJSON 构造一次 POST 并直接调用 handler，RemoteAddr 用于确定 clientIP。
func postJSON(h http.HandlerFunc, path, remoteAddr string, payload any) *httptest.ResponseRecorder {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// assertSharedBucket 用一串"同一身份的等价输入"打 maxFail+1 次，断言最后一次被限流。
// 若桶键里含了这些输入串，它们会各占一个桶，最后一次仍会放行——测试即失败。
func assertSharedBucket(t *testing.T, h http.HandlerFunc, path, ip, bucketKey string, variants []string) {
	t.Helper()
	loginLimiter.reset(bucketKey)
	t.Cleanup(func() { loginLimiter.reset(bucketKey) })

	if len(variants) != loginLimiter.maxFail+1 {
		t.Fatalf("测试用例需提供 maxFail+1=%d 个变体，实际 %d", loginLimiter.maxFail+1, len(variants))
	}
	for i, v := range variants {
		payload := map[string]string{"password": "definitely-wrong"}
		if strings.Contains(path, "access") {
			payload["code"] = v
		} else {
			payload["username"] = v
		}
		rec := postJSON(h, path, ip+":5555", payload)
		limited := rec.Code == http.StatusTooManyRequests
		if i < loginLimiter.maxFail && limited {
			t.Fatalf("第 %d 次（输入 %q）不该被限流——说明桶键里仍含该输入串", i+1, v)
		}
		if i == loginLimiter.maxFail && !limited {
			t.Fatalf("第 %d 次（输入 %q）应被限流却返回 %d——桶键含攻击者可控输入，可轮换绕过",
				i+1, v, rec.Code)
		}
	}
}

// TestAdminLoginRateKeyIgnoresUsernameVariants 管理员登录：桶键只按 IP。
func TestAdminLoginRateKeyIgnoresUsernameVariants(t *testing.T) {
	previous := config.GetTestConfig()
	t.Cleanup(func() { config.RestoreTestConfig(previous) })
	config.SetTestConfig(&config.EyvescloudConfig{
		AdminUser:     "admin",
		AdminPassHash: "not-a-valid-bcrypt-hash",
		JWTSecret:     "ratelimit-hardening-secret",
	})

	assertSharedBucket(t, HandleLogin, "/api/login", "10.77.1.1", "10.77.1.1|admin",
		[]string{"admin", "ADMIN", "Admin", " admin", "admin ", "admin@example.com"})
}

// TestSubUserLoginRateKeyIgnoresUsernameVariants 子用户密码登录：桶键只按 IP。
// 刻意不配置任何子用户：未命中账号的失败同样要落进同一个 IP 桶。
func TestSubUserLoginRateKeyIgnoresUsernameVariants(t *testing.T) {
	previous := config.GetTestConfig()
	t.Cleanup(func() { config.RestoreTestConfig(previous) })
	config.SetTestConfig(&config.EyvescloudConfig{JWTSecret: "ratelimit-hardening-secret"})

	assertSharedBucket(t, HandleSubUserLogin, "/api/sub-user/login", "10.77.2.2", "10.77.2.2|subuser-login",
		[]string{"alice", "ALICE", "Alice", "alice@example.com", "aLiCe", "alice "})
}

// TestAccessCodeLoginRateKeyIgnoresGuessedCode 访问码登录：桶键只按 IP。
// 这是最要命的那个——每个"猜测值"如果各自成桶，爆破就没有上限。
func TestAccessCodeLoginRateKeyIgnoresGuessedCode(t *testing.T) {
	previous := config.GetTestConfig()
	t.Cleanup(func() { config.RestoreTestConfig(previous) })
	config.SetTestConfig(&config.EyvescloudConfig{JWTSecret: "ratelimit-hardening-secret"})

	assertSharedBucket(t, HandleSubUserAccessCode, "/api/sub-user/access", "10.77.3.3", "10.77.3.3|subuser-accesscode",
		[]string{"000000", "111111", "222222", "333333", "444444", "555555"})
}

// TestV2AuthRequiresSameOriginForCookieWrites v2 写操作补齐 CSRF 纵深防御：
// 只有「Cookie 认证」的浏览器才受约束；Bearer/API Key 调用方不受影响。
func TestV2AuthRequiresSameOriginForCookieWrites(t *testing.T) {
	token := withV2TestConfig(t)
	mux := newV2TestMux()

	call := func(origin, referer string, useCookie bool) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/instances/7/power",
			strings.NewReader(`{"action":"start"}`))
		req.Host = "panel.example.com"
		if useCookie {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call("http://evil.example.com", "", true); code != http.StatusForbidden {
		t.Fatalf("跨站 Origin + Cookie 的 v2 写操作应 403，得到 %d", code)
	}
	if code := call("", "http://evil.example.com/attack", true); code != http.StatusForbidden {
		t.Fatalf("跨站 Referer + Cookie 的 v2 写操作应 403，得到 %d", code)
	}
	if code := call("http://panel.example.com", "", true); code == http.StatusForbidden {
		t.Fatal("同源 Cookie 写操作被 CSRF 守卫误拦")
	}
	if code := call("http://evil.example.com", "", false); code == http.StatusForbidden {
		t.Fatal("Bearer 认证不带 Cookie，无 CSRF 语义，不该被拦截")
	}
}

// TestNodeHeartbeatRejectsMismatchedToken 节点心跳：错误 token 必须 401。
func TestNodeHeartbeatRejectsMismatchedToken(t *testing.T) {
	previous := config.GetTestConfig()
	t.Cleanup(func() { config.RestoreTestConfig(previous) })
	config.SetTestConfig(&config.EyvescloudConfig{
		JWTSecret: "ratelimit-hardening-secret",
		Nodes:     []config.Node{{ID: "node-1", Token: "correct-node-token"}},
	})

	call := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/heartbeat",
			strings.NewReader(`{"version":"2.2.41"}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handleNodeHeartbeat(rec, req, "node-1")
		return rec.Code
	}

	if code := call("wrong-node-token"); code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，得到 %d", code)
	}
	if code := call(""); code != http.StatusUnauthorized {
		t.Fatalf("空 token 应 401，得到 %d", code)
	}
	if code := call("correct-node-token"); code == http.StatusUnauthorized {
		t.Fatalf("正确 token 不该被判 401，得到 %d", code)
	}
}
