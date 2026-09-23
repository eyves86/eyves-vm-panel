package api

import "testing"

func TestCanonicalAdminPath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/containers": "/api/containers",
		"/api/containers":    "/api/containers",
		"/api/v1/admins/abc": "/api/admins/abc",
		"/api/2fa/setup":     "/api/2fa/setup",
	}
	for in, want := range cases {
		if got := canonicalAdminPath(in); got != want {
			t.Fatalf("canonicalAdminPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAdminRoleAllowedMatrix 锁定管理端角色权限矩阵（基于 rbac 权限点，fail closed）。
func TestAdminRoleAllowedMatrix(t *testing.T) {
	cases := []struct {
		role, method, path string
		want               bool
	}{
		// 全权 admin：一切放行
		{"admin", "GET", "/api/v1/containers", true},
		{"admin", "DELETE", "/api/v1/admins/abc", true},
		{"admin", "POST", "/api/v1/api-keys", true},
		{"admin", "POST", "/api/v1/unknown-thing", true},

		// readonly：仅读；且没有 apikey:read 等平台读权限
		{"readonly", "GET", "/api/v1/containers", true},
		{"readonly", "GET", "/api/v1/snapshots", true},
		{"readonly", "POST", "/api/v1/containers", false},
		{"readonly", "DELETE", "/api/v1/snapshots/x", false},
		{"readonly", "GET", "/api/v1/api-keys", false},
		{"readonly", "POST", "/api/v1/admins", false},

		// operator：运维可写，平台级禁
		{"operator", "POST", "/api/v1/containers", true},
		{"operator", "POST", "/api/v1/snapshots", true},
		{"operator", "POST", "/api/v1/routing", true},
		{"operator", "POST", "/api/v1/admins", false},
		{"operator", "POST", "/api/v1/api-keys", false},
		{"operator", "PUT", "/api/v1/policies", false},
		{"operator", "POST", "/api/v1/tenants", false},
		{"operator", "PUT", "/api/v1/access-policy", false},
		{"operator", "POST", "/api/v1/unknown-thing", false}, // 未映射写操作 fail closed

		// 自助路径：所有管理员角色可用（2FA/语言/鉴权探测）
		{"readonly", "GET", "/api/check-auth", true},
		{"readonly", "POST", "/api/2fa/setup", true},
		{"operator", "POST", "/api/2fa/enable", true},

		// 未知角色 fail closed
		{"bogus", "GET", "/api/v1/containers", false},
		{"", "GET", "/api/v1/containers", true}, // 空角色按 admin 兼容旧令牌
	}
	for _, c := range cases {
		if got := adminRoleAllowed(c.role, c.method, c.path); got != c.want {
			t.Errorf("adminRoleAllowed(%q, %s, %s) = %v, want %v", c.role, c.method, c.path, got, c.want)
		}
	}
}
