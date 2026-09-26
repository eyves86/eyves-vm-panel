package config

import "testing"

func TestNormalizeAdminRoleFailsClosed(t *testing.T) {
	cases := map[string]string{
		"admin":    AdminRoleAdmin,
		"ADMIN":    AdminRoleAdmin,
		"operator": AdminRoleOperator,
		"readonly": AdminRoleReadonly,
		"viewer":   AdminRoleReadonly,
		"read":     AdminRoleReadonly,
		// 未知取值一律降级只读，绝不默认放行
		"superuser": AdminRoleReadonly,
		"":          AdminRoleReadonly,
	}
	for in, want := range cases {
		if got := NormalizeAdminRole(in); got != want {
			t.Fatalf("NormalizeAdminRole(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdminAccountLookupAndUniqueness(t *testing.T) {
	previous := AppConfig
	t.Cleanup(func() { AppConfig = previous })

	AppConfig = &EyvescloudConfig{
		AdminUser: "root-admin",
		Admins: []AdminAccount{
			{ID: "a1", Username: "alice", Role: AdminRoleOperator},
			{ID: "a2", Username: "bob", Role: AdminRoleReadonly},
		},
	}

	if _, ok := FindAdminAccount("alice"); !ok {
		t.Fatal("alice must be found")
	}
	// 大小写不敏感
	if _, ok := FindAdminAccount("ALICE"); !ok {
		t.Fatal("lookup must be case-insensitive")
	}
	if _, ok := FindAdminAccount("nobody"); ok {
		t.Fatal("unknown admin must not be found")
	}
	if a, ok := FindAdminAccountByID("a2"); !ok || a.Username != "bob" {
		t.Fatal("lookup by id failed")
	}

	// 用户名占用：主管理员 / 其它管理员 / 自身
	if !AdminUsernameTaken("root-admin", "") {
		t.Fatal("primary admin username must be taken")
	}
	if !AdminUsernameTaken("alice", "") {
		t.Fatal("existing admin username must be taken")
	}
	if AdminUsernameTaken("alice", "a1") {
		t.Fatal("own username must be allowed when excluding self")
	}
	if AdminUsernameTaken("carol", "") {
		t.Fatal("unused username must be free")
	}
	if !AdminUsernameTaken("", "") {
		t.Fatal("empty username must be treated as taken (invalid)")
	}
}

func TestNormalizeAdminPath(t *testing.T) {
	valid := map[string]string{
		"":                DefaultAdminPath,
		"/":               DefaultAdminPath,
		"admin":           "/admin",
		"/admin":          "/admin",
		"/admin/":         "/admin",
		"/my-panel_9.x~y": "/my-panel_9.x~y",
		"/a/b":            "/a/b",
	}
	for in, want := range valid {
		got, ok := NormalizeAdminPath(in)
		if !ok || got != want {
			t.Fatalf("NormalizeAdminPath(%q) = (%q,%v), want (%q,true)", in, got, ok, want)
		}
	}

	// 非法值必须被拒绝：保留前缀、路径穿越、空白/特殊字符、非 ASCII
	invalid := []string{
		"/api", "/api/v1", "/user", "/user/login", "/assets", "/assets/index.js",
		"/favicon", "/favicon.svg", "/index.html",
		"/a/../b", "/..", "/a//b", "/a b", "/a?b", "/a#b", "/a%2f", "/管理", "/a\\b",
	}
	for _, in := range invalid {
		if got, ok := NormalizeAdminPath(in); ok {
			t.Fatalf("NormalizeAdminPath(%q) must be rejected, got (%q,true)", in, got)
		}
	}
}

// TestAdminPathForRequest 锁定「按请求路径注入管理员入口」的行为：
// 只有命中管理路径的请求才会拿到管理端路由，其它路径一律注入空串。
func TestAdminPathForRequest(t *testing.T) {
	previous := AppConfig
	t.Cleanup(func() { AppConfig = previous })

	// 默认路径 "/"：任何请求都注入 "/"，保持历史行为
	AppConfig = &EyvescloudConfig{AdminPath: DefaultAdminPath}
	for _, p := range []string{"/", "/login", "/containers", "/whatever"} {
		if got := AdminPathForRequest(p); got != "/" {
			t.Fatalf("default path: AdminPathForRequest(%q) = %q, want /", p, got)
		}
	}

	// 自定义路径：只有命中才注入真实路径，其它注入空串
	AppConfig = &EyvescloudConfig{AdminPath: "/secret-panel"}
	cases := map[string]string{
		"/secret-panel":       "/secret-panel",
		"/secret-panel/login": "/secret-panel",
		"/secret-panel/a/b":   "/secret-panel",
		"/":                   "",
		"/login":              "",
		"/admin":              "",
		"/user":               "",
		"/secret-panelX":      "",
	}
	for reqPath, want := range cases {
		if got := AdminPathForRequest(reqPath); got != want {
			t.Fatalf("AdminPathForRequest(%q) = %q, want %q", reqPath, got, want)
		}
	}
}
