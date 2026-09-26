package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"eyvescloud/internal/config"
)

// TestRootPathServesSPAIndexWithAdminPathInjection 验证自定义管理员入口路径下，
// 根路径 "/" 返回的 index.html 必须经过 serveSPAIndex（按请求路径注入管理路径）。
//
// 回归背景：静态处理器先 webFS.Open(path)，仅当 Open 失败才走 serveSPAIndex。
// 而根路径 "/"（目录）Open 成功，由 http.FileServer 直接吐出原始 index.html，
// 占位符 __EYVES_ADMIN_PATH_VALUE__ 未被替换 —— 前端 panelPath.ts 把占位符
// 视为“开发模式/默认”，按根路径挂载管理端路由，导致 / 直接暴露管理员登录。
func TestRootPathServesSPAIndexWithAdminPathInjection(t *testing.T) {
	indexHTML := `<!DOCTYPE html><html><head>
<script nonce="__EYVES_ADMIN_NONCE__">window.__EYVES_ADMIN_PATH__ = "__EYVES_ADMIN_PATH_VALUE__";</script>
</head><body><div id="root"></div></body></html>`
	webFS = http.FS(fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte(indexHTML)},
		"favicon.svg": &fstest.MapFile{Data: []byte("<svg/>")},
	})

	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{AdminPath: "/admin-x"}

	mux := http.NewServeMux()
	setupRoutes(mux)

	// 自定义管理路径：注入真实路径 + nonce
	wAdmin := httptest.NewRecorder()
	mux.ServeHTTP(wAdmin, httptest.NewRequest(http.MethodGet, "/admin-x", nil))
	if !strings.Contains(wAdmin.Body.String(), `window.__EYVES_ADMIN_PATH__ = "/admin-x"`) {
		t.Fatalf("admin path page should inject real admin path, got: %s", wAdmin.Body.String())
	}
	assertNonceInjected(t, wAdmin)

	// 根路径 "/":不得返回未替换占位符的原始 index.html
	wRoot := httptest.NewRecorder()
	mux.ServeHTTP(wRoot, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(wRoot.Body.String(), adminPathPlaceholder) {
		t.Fatalf("root path served raw index.html with unreplaced placeholder: %s", wRoot.Body.String())
	}
	if !strings.Contains(wRoot.Body.String(), `window.__EYVES_ADMIN_PATH__ = ""`) {
		t.Fatalf("root path should inject empty admin path, got: %s", wRoot.Body.String())
	}
	assertNonceInjected(t, wRoot)

	// /user：注入空串（现有正确行为）
	wUser := httptest.NewRecorder()
	mux.ServeHTTP(wUser, httptest.NewRequest(http.MethodGet, "/user", nil))
	if !strings.Contains(wUser.Body.String(), `window.__EYVES_ADMIN_PATH__ = ""`) {
		t.Fatalf("user path page should inject empty admin path, got: %s", wUser.Body.String())
	}

	// /login：注入空串（现有正确行为，枚举 /login 不应命中管理路由）
	wLogin := httptest.NewRecorder()
	mux.ServeHTTP(wLogin, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(wLogin.Body.String(), `window.__EYVES_ADMIN_PATH__ = ""`) {
		t.Fatalf("/login page should inject empty admin path, got: %s", wLogin.Body.String())
	}

	// 静态资源（文件，非目录）仍由 FileServer 原样返回
	wAsset := httptest.NewRecorder()
	mux.ServeHTTP(wAsset, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if wAsset.Code != http.StatusOK || wAsset.Body.String() != "<svg/>" {
		t.Fatalf("static asset should be served raw, got status=%d body=%s", wAsset.Code, wAsset.Body.String())
	}
}

// assertNonceInjected 校验：nonce 占位符已被替换为一次性值，且 CSP 的
// script-src 已同步放行该 nonce —— 否则浏览器会拦截内联脚本，
// window.__EYVES_ADMIN_PATH__ 为 undefined，前端按根路径挂载管理端路由。
func assertNonceInjected(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), adminNoncePlaceholder) {
		t.Fatalf("nonce placeholder not replaced: %s", w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") {
		t.Fatalf("CSP script-src must include per-response nonce, CSP: %s", csp)
	}
	// body 与 CSP 的 nonce 必须一致
	open := strings.Index(w.Body.String(), `nonce="`)
	if open < 0 {
		t.Fatalf("inline script missing nonce attribute: %s", w.Body.String())
	}
	nonceVal := w.Body.String()[open+len(`nonce="`):]
	nonceVal = nonceVal[:strings.Index(nonceVal, `"`)]
	if !strings.Contains(csp, "'nonce-"+nonceVal+"'") {
		t.Fatalf("CSP nonce does not match script nonce (script=%s)", nonceVal)
	}
}
