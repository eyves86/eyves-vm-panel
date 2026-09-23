package rbac

import "testing"

func TestBuiltinRolesCoverBaselinePermissions(t *testing.T) {
	for _, id := range []string{"admin", "owner", "operator", "readonly"} {
		if _, ok := NewEngine().GetRole(id); !ok {
			t.Fatalf("role %q missing", id)
		}
	}
}

func TestAdminHasWildcard(t *testing.T) {
	r, _ := NewEngine().GetRole("admin")
	for _, p := range AllPermissions() {
		if !r.HasPermission(p) {
			t.Fatalf("admin must have %s via wildcard", p)
		}
	}
}

func TestReadonlyCannotWrite(t *testing.T) {
	r, _ := NewEngine().GetRole("readonly")
	writes := []Permission{
		PermContainerPower, PermContainerCreate, PermContainerDelete,
		PermSnapshotCreate, PermSnapshotDelete,
		PermAPIKeyCreate, PermAPIKeyDelete,
		PermSystemSettings,
	}
	for _, p := range writes {
		if r.HasPermission(p) {
			t.Fatalf("readonly must NOT have %s", p)
		}
	}
}

func TestReadonlyCanRead(t *testing.T) {
	r, _ := NewEngine().GetRole("readonly")
	reads := []Permission{
		PermContainerRead, PermImageRead, PermSnapshotRead,
		PermRoutingRead, PermHostRead, PermTenantRead,
	}
	for _, p := range reads {
		if !r.HasPermission(p) {
			t.Fatalf("readonly must have %s", p)
		}
	}
}

func TestOwnerExcludesAdminOnlyScopes(t *testing.T) {
	r, _ := NewEngine().GetRole("owner")
	if r.HasPermission(PermAdminAccess) {
		t.Fatal("owner must NOT have admin:access")
	}
	if r.HasPermission(PermSystemSettings) {
		t.Fatal("owner must NOT have system:settings")
	}
	if r.HasPermission(PermAuditSettings) {
		t.Fatal("owner must NOT have audit:settings")
	}
}

func TestOperatorExcludesSensitiveWrites(t *testing.T) {
	r, _ := NewEngine().GetRole("operator")
	if r.HasPermission(PermAPIKeyCreate) {
		t.Fatal("operator must NOT create API keys")
	}
	if r.HasPermission(PermContainerDelete) {
		t.Fatal("operator must NOT delete containers (revocable)")
	}
}

func TestAuthorizeByRole(t *testing.T) {
	e := NewEngine()
	d := e.Authorize("u1", "operator", "container", "c-1", PermContainerPower)
	if !d.Allow {
		t.Fatal("operator should allow container:power")
	}
	if d.Source != "role" {
		t.Fatalf("source = %s, want role", d.Source)
	}
	d = e.Authorize("u1", "readonly", "container", "c-1", PermContainerPower)
	if d.Allow {
		t.Fatal("readonly must deny container:power")
	}
}

func TestAuthorizeByResourceGrant(t *testing.T) {
	e := NewEngine()
	if err := e.AddGrant(ResourceGrant{
		Subject: "u1", ResourceType: "container", ResourceID: "c-special",
		Permissions: []Permission{PermContainerPower},
	}); err != nil {
		t.Fatal(err)
	}
	// readonly 用户对 c-special 拿到额外授权
	d := e.Authorize("u1", "readonly", "container", "c-special", PermContainerPower)
	if !d.Allow {
		t.Fatal("grant must override default-deny")
	}
	if d.Source != "grant" {
		t.Fatalf("source = %s, want grant", d.Source)
	}
	// 但对其它容器仍 deny
	d = e.Authorize("u1", "readonly", "container", "c-other", PermContainerPower)
	if d.Allow {
		t.Fatal("grant must be resource-specific")
	}
}

func TestResourceGrantWildcardID(t *testing.T) {
	e := NewEngine()
	_ = e.AddGrant(ResourceGrant{
		Subject: "u1", ResourceType: "container",
		Permissions: []Permission{PermSnapshotCreate},
	})
	d := e.Authorize("u1", "readonly", "container", "any", PermSnapshotCreate)
	if !d.Allow {
		t.Fatal("wildcard resource ID grant must apply to all containers")
	}
}

func TestAddGrantValidates(t *testing.T) {
	e := NewEngine()
	if err := e.AddGrant(ResourceGrant{}); err == nil {
		t.Fatal("empty subject must error")
	}
	if err := e.AddGrant(ResourceGrant{Subject: "u"}); err == nil {
		t.Fatal("empty resource type must error")
	}
}

func TestUpsertRole(t *testing.T) {
	e := NewEngine()
	if err := e.UpsertRole(Role{ID: "custom", Permissions: []Permission{PermHostRead}}); err != nil {
		t.Fatal(err)
	}
	r, ok := e.GetRole("custom")
	if !ok {
		t.Fatal("custom role not found")
	}
	if !r.HasPermission(PermHostRead) {
		t.Fatal("custom role must have host:read")
	}
	if err := e.UpsertRole(Role{}); err == nil {
		t.Fatal("empty id must error")
	}
}

func TestMigrateLegacyRole(t *testing.T) {
	cases := map[string]string{
		"admin":    "admin",
		"":         "admin",
		"operator": "operator",
		"viewer":   "readonly",
		"readonly": "readonly",
		"read":     "readonly",
		"unknown":  "readonly",
		// owner 必须保持 owner，绝不能升级为 admin（越权回归测试）
		"owner": "owner",
		"OWNER": "owner",
	}
	for in, want := range cases {
		if got := MigrateLegacyRole(in); got != want {
			t.Fatalf("MigrateLegacyRole(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestMigrateLegacyOwnerDoesNotEscalate 锁定越权修复：租户 owner 迁移后
// 不得持有 admin:access / system:settings 等平台管理权限。
func TestMigrateLegacyOwnerDoesNotEscalate(t *testing.T) {
	e := NewEngine()
	roleID := MigrateLegacyRole("owner")
	r, ok := e.GetRole(roleID)
	if !ok {
		t.Fatalf("migrated role %q must exist", roleID)
	}
	if r.HasPermission(PermAdminAccess) {
		t.Fatal("migrated owner must NOT have admin:access")
	}
	if r.HasPermission(PermSystemSettings) {
		t.Fatal("migrated owner must NOT have system:settings")
	}
	// 但租户内应有基本读写能力
	if !r.HasPermission(PermContainerRead) {
		t.Fatal("migrated owner should keep container:read")
	}
}

func TestHasPermissionPrefixWildcard(t *testing.T) {
	r := Role{ID: "x", Permissions: []Permission{"apikey:*"}}
	if !r.HasPermission(PermAPIKeyRead) {
		t.Fatal("apikey:* must grant apikey:read")
	}
	if !r.HasPermission(PermAPIKeyDelete) {
		t.Fatal("apikey:* must grant apikey:delete")
	}
	if r.HasPermission(PermContainerRead) {
		t.Fatal("apikey:* must NOT grant container:read")
	}
}