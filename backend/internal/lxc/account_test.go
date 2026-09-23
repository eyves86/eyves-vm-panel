package lxc

import "testing"

func TestNormalizeReinstallMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"system", ReinstallModeSystem},
		{"SYSTEM", ReinstallModeSystem},
		{" full ", ReinstallModeFull},
		{"Full", ReinstallModeFull},
		{"", ""},
		{"bogus", ""},
	}
	for _, tc := range cases {
		if got := NormalizeReinstallMode(tc.in); got != tc.want {
			t.Fatalf("NormalizeReinstallMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateAccountUsername(t *testing.T) {
	valid := []string{"alice", "ops_user", "team-1", "_svc"}
	for _, name := range valid {
		if err := ValidateAccountUsername(name); err != nil {
			t.Fatalf("ValidateAccountUsername(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "Alice", "1user", "-bad", "bad name", "user;rm", "a/b", "verylongusernamethatexceedsthirtytwo"}
	for _, name := range invalid {
		if err := ValidateAccountUsername(name); err == nil {
			t.Fatalf("ValidateAccountUsername(%q) should have failed", name)
		}
	}
}

func TestValidateAccountPassword(t *testing.T) {
	if err := ValidateAccountPassword("S3cure-pass!"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := ValidateAccountPassword(""); err == nil {
		t.Fatalf("empty password should be rejected")
	}
	if err := ValidateAccountPassword("line1\nline2"); err == nil {
		t.Fatalf("password containing newline should be rejected")
	}
}