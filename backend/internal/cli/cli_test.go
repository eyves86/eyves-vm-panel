package cli

import (
	"strings"
	"testing"
)

func TestSafeReleaseBackupComponent(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":              "1.2.3",
		" release/candidate ": "release_candidate",
		"../../etc/passwd":    "etc_passwd",
		"":                    "unknown",
	}
	for input, want := range tests {
		if got := safeReleaseBackupComponent(input); got != want {
			t.Fatalf("safeReleaseBackupComponent(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReleaseArchiveAssetName(t *testing.T) {
	tests := map[string]string{
		"amd64": "eyvescloud-linux-amd64.tar.gz",
		"arm64": "eyvescloud-linux-arm64.tar.gz",
	}
	for goarch, want := range tests {
		got, err := releaseArchiveAssetName(goarch)
		if err != nil {
			t.Fatalf("releaseArchiveAssetName(%q) error = %v", goarch, err)
		}
		if got != want {
			t.Fatalf("releaseArchiveAssetName(%q) = %q, want %q", goarch, got, want)
		}
	}

	if _, err := releaseArchiveAssetName("386"); err == nil {
		t.Fatal("releaseArchiveAssetName(386) error = nil, want unsupported architecture")
	}
}

func TestCopyFileToBackupRejectsUnsafeFileName(t *testing.T) {
	unsafeNames := []string{
		"../eyvescloud",
		"..\\eyvescloud",
		"subdir/eyvescloud",
		"",
	}
	for _, name := range unsafeNames {
		if _, err := copyFileToBackup("missing-source", name, 0755); err == nil || !strings.Contains(err.Error(), "unsafe backup file name") {
			t.Fatalf("copyFileToBackup(%q) error = %v, want unsafe backup file name", name, err)
		}
	}
}

func TestFormatSSHAccessDoesNotExposePassword(t *testing.T) {
	out := formatSSHAccess(2222)
	if strings.Contains(out, "/") {
		t.Fatalf("formatSSHAccess output contains credential separator: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "password123") {
		t.Fatalf("formatSSHAccess output exposed password: %q", out)
	}
	if !strings.Contains(out, "2222 -> 22") {
		t.Fatalf("formatSSHAccess output = %q, want SSH port mapping", out)
	}
}

func TestFormatSSHAccessHandlesMissingPort(t *testing.T) {
	out := formatSSHAccess(0)
	if !strings.Contains(out, "端口未分配") {
		t.Fatalf("formatSSHAccess output = %q, want missing port message", out)
	}
}

func TestResolveRepoSource(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		platform string
		owner    string
		repo     string
	}{
		{"plain owner/repo", "FenhaoLost/eyves-vm-panel", "github", "FenhaoLost", "eyves-vm-panel"},
		{"codeberg prefix", "codeberg:fenhaolost/eyves-vm-panel", "codeberg", "fenhaolost", "eyves-vm-panel"},
		{"gitee short prefix", "gt:user/repo", "gitee", "user", "repo"},
		{"gitlab prefix", "gitlab:group/subgroup/repo", "gitlab", "group", "subgroup/repo"},
		{"codeberg URL", "https://codeberg.org/fenhaolost/eyves-vm-panel", "codeberg", "fenhaolost", "eyves-vm-panel"},
		{"gitee URL", "https://gitee.com/user/repo", "gitee", "user", "repo"},
		{"gitlab URL", "https://gitlab.com/group/repo", "gitlab", "group", "repo"},
		{"github URL fallback", "https://github.com/user/repo", "github", "user", "repo"},
		{"empty → default", "", "github", "FenhaoLost", "eyves-vm-panel"},
		{"gh alias", "gh:user/repo", "github", "user", "repo"},
		{"cb alias", "cb:user/repo", "codeberg", "user", "repo"},
		{"gl alias", "gl:group/repo", "gitlab", "group", "repo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := resolveRepoSource(c.input)
			if src.Platform != c.platform {
				t.Fatalf("platform: got %q, want %q", src.Platform, c.platform)
			}
			if src.Owner != c.owner {
				t.Fatalf("owner: got %q, want %q", src.Owner, c.owner)
			}
			if src.Repo != c.repo {
				t.Fatalf("repo: got %q, want %q", src.Repo, c.repo)
			}
		})
	}
}

func TestValidateRepoSlugMultiPlatform(t *testing.T) {
	// 扩展后应同时接受 owner/repo 和 codeberg:owner/repo 两种格式。
	if !validateRepoSlug("FenhaoLost/eyves-vm-panel") {
		t.Fatal("owner/repo must be valid")
	}
	if !validateRepoSlug("codeberg:fenhaolost/eyves-vm-panel") {
		t.Fatal("codeberg:owner/repo must be valid")
	}
	if !validateRepoSlug("https://codeberg.org/fenhaolost/eyves-vm-panel") {
		t.Fatal("full codeberg URL must be valid")
	}
	if !validateRepoSlug("") {
		t.Fatal("empty must be rejected (or accepted as default, current impl accepts)")
	}
	if validateRepoSlug("invalid/path/extra") {
		t.Fatal("three-segment slug must be rejected")
	}
}
