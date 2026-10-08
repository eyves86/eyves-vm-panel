package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eyvescloud/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

// TestSubUserTokenRevocationViaIndex 在子用户下标缓存（FindSubUserIndexByNameUnlocked）
// 生效的前提下钉死 claimsFromToken 的吊销语义：下标命中校验若做错行，
// 这里最先炸——要么放行已吊销令牌，要么吊销别人的会话。
func TestSubUserTokenRevocationViaIndex(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		SubUsers: []config.SubUser{
			{Username: "su-a", TokenVersion: 1},
			{Username: "su-b", TokenVersion: 7},
			{Username: "su-zero", TokenVersion: 0},
		},
	}

	exp := time.Now().Add(time.Hour)
	tok := newSubUserToken("su-b", nil, exp, 7)
	if _, ok := claimsFromToken(tok); !ok {
		t.Fatal("版本匹配的令牌必须有效")
	}

	// 版本不匹配 / 旧式无版本令牌（stored>0 时一律拒绝）
	if _, ok := claimsFromToken(newSubUserToken("su-b", nil, exp, 6)); ok {
		t.Fatal("版本过期（6 != 7）的令牌必须失效")
	}
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub_user": "su-b", "iss": jwtIssuer, "aud": jwtAudience, "exp": exp.Unix(),
	})
	legacyStr, _ := legacy.SignedString([]byte(config.AppConfig.JWTSecret))
	if _, ok := claimsFromToken(legacyStr); ok {
		t.Fatal("stored>0 时缺 token_version 的旧式令牌必须失效")
	}

	// 轮换 TokenVersion（改密即吊销）→ 已签发令牌立刻失效
	config.AppConfigMu.Lock()
	for i := range config.AppConfig.SubUsers {
		if config.AppConfig.SubUsers[i].Username == "su-b" {
			config.AppConfig.SubUsers[i].TokenVersion++
		}
	}
	config.AppConfigMu.Unlock()
	if _, ok := claimsFromToken(tok); ok {
		t.Fatal("TokenVersion 轮换后旧令牌必须失效")
	}

	// 删除账号 → 令牌失效（删行前移后下标必须自愈，不得复活）
	config.AppConfigMu.Lock()
	rest := config.AppConfig.SubUsers[:0]
	for _, su := range config.AppConfig.SubUsers {
		if su.Username != "su-b" {
			rest = append(rest, su)
		}
	}
	config.AppConfig.SubUsers = rest
	config.AppConfigMu.Unlock()
	if _, ok := claimsFromToken(tok); ok {
		t.Fatal("账号删除后令牌必须失效")
	}

	// stored=0（从未轮换）的新建账号：旧式无版本令牌仍有效——向后兼容契约。
	if _, ok := claimsFromToken(legacyStr); ok {
		t.Fatal("legacy 令牌属于 su-b，su-b 已删除，不得因 su-zero 存在而复活")
	}
	tokZero := newSubUserToken("su-zero", nil, exp, 0)
	if _, ok := claimsFromToken(tokZero); !ok {
		t.Fatal("stored=0 的账号版本匹配令牌必须有效")
	}

	// 重建同名账号（新 TokenVersion）→ 新令牌有效，且不串到其它行的版本。
	config.AppConfigMu.Lock()
	config.AppConfig.SubUsers = append(config.AppConfig.SubUsers, config.SubUser{Username: "su-b", TokenVersion: 8})
	config.AppConfigMu.Unlock()
	if _, ok := claimsFromToken(tok); ok {
		t.Fatal("重建后的 su-b TokenVersion=8，旧版本 7 的令牌必须仍失效")
	}
	if _, ok := claimsFromToken(newSubUserToken("su-b", nil, exp, 8)); !ok {
		t.Fatal("重建后的 su-b 版本匹配令牌必须有效")
	}
}

// TestClaimsCachedAcrossMiddleware 钉死切片 3 契约：中间件验签一次后 claims
// 经 context 复用（handler 内 claimsFromRequest 不再重复 HMAC 验签）；
// 未挂中间件的裸请求仍走 token 解析兜底；无令牌一律 !ok。
func TestClaimsCachedAcrossMiddleware(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{
		JWTSecret: "test-secret",
		SubUsers:  []config.SubUser{{Username: "su-a", TokenVersion: 1}},
	}
	tok := newSubUserToken("su-a", nil, time.Now().Add(time.Hour), 1)

	assertSubUser := func(what string, r *http.Request) {
		t.Helper()
		claims, ok := claimsFromRequest(r)
		if !ok || claims["sub_user"] != "su-a" {
			t.Fatalf("%s: claimsFromRequest 必须返回 su-a 的 claims", what)
		}
		if !isSubUserRequest(r) {
			t.Fatalf("%s: isSubUserRequest 必须为真", what)
		}
	}

	// 挂 AuthMiddleware 的路由：handler 拿到的 claims 来自 context 缓存。
	mwReq := httptest.NewRequest("GET", "/x", http.NoBody)
	mwReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	mwRec := httptest.NewRecorder()
	AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		assertSubUser("middleware 路由", r)
		w.WriteHeader(http.StatusOK)
	}).ServeHTTP(mwRec, mwReq)
	if mwRec.Code != http.StatusOK {
		t.Fatalf("middleware 路由未放行（%d），断言空洞", mwRec.Code)
	}

	// 挂 OptionalAuthMiddleware 的路由：同样注入缓存。
	opReq := httptest.NewRequest("GET", "/x", http.NoBody)
	opReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	opRec := httptest.NewRecorder()
	OptionalAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		assertSubUser("optional 路由", r)
		w.WriteHeader(http.StatusOK)
	}).ServeHTTP(opRec, opReq)
	if opRec.Code != http.StatusOK {
		t.Fatalf("optional 路由未放行（%d），断言空洞", opRec.Code)
	}

	// 裸请求（无中间件）：兜底路径仍解析 token。
	req := httptest.NewRequest("GET", "/x", http.NoBody)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	assertSubUser("裸请求兜底", req)

	// 无令牌：claimsFromRequest 必须 !ok，isSubUserRequest 必须 false。
	bare := httptest.NewRequest("GET", "/x", http.NoBody)
	if _, ok := claimsFromRequest(bare); ok {
		t.Fatal("无令牌请求不得返回 claims")
	}
	if isSubUserRequest(bare) {
		t.Fatal("无令牌请求不得被判定为子用户")
	}
}

// BenchmarkClaimsFromTokenSubUser 量化鉴权热路径：10w 子用户目录下单次
// 令牌校验（JWT 验签 + TokenVersion 吊销查表）。索引化前这里是
// O(全部子用户) 线性扫，且每请求可能 parse 多次（requestActor 等）。
func BenchmarkClaimsFromTokenSubUser(b *testing.B) {
	const n = 100000
	sus := make([]config.SubUser, n)
	for i := range sus {
		sus[i] = config.SubUser{Username: fmt.Sprintf("su-%06d", i), TokenVersion: 3}
	}
	previous := config.AppConfig
	b.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{JWTSecret: "bench-secret", SubUsers: sus}

	exp := time.Now().Add(time.Hour)
	tok := newSubUserToken("su-099999", nil, exp, 3)
	if _, ok := claimsFromToken(tok); !ok {
		b.Fatal("bench token must validate")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := claimsFromToken(tok); !ok {
			b.Fatal("token must validate in loop")
		}
	}
}
