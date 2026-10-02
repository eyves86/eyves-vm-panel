package server

// tls_redirect_test.go —— HTTP → HTTPS 跳转回归测试。
//
// 背景：面板是单端口服务，启用 TLS 后原 HTTP 入口直接消失，用户的既有书签、
// 监控探针、计费系统回调会立刻连接失败（表现为"配了证书反而打不开"）。
// 跳转监听把这次断裂变成透明升级，这里锁定其语义。

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPToHTTPSRedirectHandler(t *testing.T) {
	const httpsPort = 8999
	h := httpToHTTPSRedirectHandler(httpsPort)

	cases := []struct {
		name string
		host string
		uri  string
		want string
	}{
		{
			name: "主机头带跳转端口，应替换为 HTTPS 端口",
			host: "154.16.173.136:8998",
			uri:  "/admin-abc/containers",
			want: "https://154.16.173.136:8999/admin-abc/containers",
		},
		{
			name: "主机头不带端口，应补上 HTTPS 端口",
			host: "panel.example.com",
			uri:  "/login?next=%2Fuser",
			want: "https://panel.example.com:8999/login?next=%2Fuser",
		},
		{
			name: "根路径",
			host: "10.0.0.1:8998",
			uri:  "/",
			want: "https://10.0.0.1:8999/",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.uri, nil)
			req.Host = c.host
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("应返回 301，实际 %d", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != c.want {
				t.Fatalf("跳转目标应为 %q，实际 %q", c.want, got)
			}
		})
	}
}

// TestHTTPToHTTPSRedirectHandlerKeepsHTTPPort 反向保障：跳转目标必须带上面板
// HTTPS 端口。少写端口会让浏览器回落到 443，跳到一个没人监听的地址。
func TestHTTPToHTTPSRedirectHandlerKeepsHTTPPort(t *testing.T) {
	h := httpToHTTPSRedirectHandler(9443)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Location"); got != "https://example.com:9443/x" {
		t.Fatalf("跳转目标必须显式带 HTTPS 端口，实际 %q", got)
	}
}
