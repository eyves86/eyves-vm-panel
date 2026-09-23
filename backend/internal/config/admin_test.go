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
