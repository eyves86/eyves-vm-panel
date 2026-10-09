package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestImageDeleteRoutesRegistered 锁定「主控一键删除被控镜像」链路的两个新路由已挂载：
// 被控入站 /api/agent/images/delete 与 主控节点代理 /api/nodes/{id}/images/delete。
// 用 mux.Handler 取匹配 pattern（不真实执行 handler），避免牵动 webFS 与鉴权中间件，
// 只回归「server.go 是否把路由接上」这一类疏漏。
func TestImageDeleteRoutesRegistered(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	cases := []struct {
		name    string
		path    string
		pattern string
	}{
		{"被控镜像删除入站路由", "/api/agent/images/delete", "/api/agent/images/delete"},
		{"主控节点镜像删除代理路由", "/api/nodes/node-1/images/delete", "/api/nodes/"},
	}
	for _, tc := range cases {
		_, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, tc.path, nil))
		if pattern != tc.pattern {
			t.Fatalf("%s: 匹配 pattern = %q, want %q（路由未注册？）", tc.name, pattern, tc.pattern)
		}
	}
}
