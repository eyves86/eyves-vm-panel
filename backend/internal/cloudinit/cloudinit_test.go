package cloudinit

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

const fakeSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrmxwSwMQmc1nhUUYOj0B9rMFs1yQc+eR0vj fake@key"

func validInputs() Inputs {
	return Inputs{
		Hostname: "ct-1",
		Timezone: "Asia/Shanghai",
		User:     "admin",
		SSHKeys:  []string{fakeSSHKey},
	}
}

func TestValidateHostname(t *testing.T) {
	cases := map[string]bool{
		"ct-1":            true,
		"a":               true,
		"a1":              true,
		"a-b-c":           true,
		"":                false,
		"CT-1":            false, // 大写不允许
		"-ct":             false, // 首字符不能 -
		"ct-":             false, // 尾字符不能 -
		"ct..1":           false, // ..
		"a!b":             false, // 特殊字符
		strings.Repeat("a", 64): false, // 超长
		strings.Repeat("a", 63): true,
	}
	for h, ok := range cases {
		if err := ValidateHostname(h); (err == nil) != ok {
			t.Fatalf("ValidateHostname(%q): ok=%v err=%v", h, ok, err)
		}
	}
}

func TestValidateUserDataRequiresCloudConfig(t *testing.T) {
	if err := ValidateUserData("foo: bar"); err == nil {
		t.Fatal("non-cloud-config must error")
	}
	if err := ValidateUserData("#cloud-config\nfoo: bar"); err != nil {
		t.Fatalf("valid user-data: %v", err)
	}
}

func TestValidateUserDataRejectsShellInjection(t *testing.T) {
	bad := []string{
		"#cloud-config\nruncmd:\n  - $(whoami)",
		"#cloud-config\nruncmd:\n  - ${PATH}",
		"#cloud-config\nruncmd:\n  - `id`",
		"#cloud-config\nruncmd:\n  - evil|sh",
		"#cloud-config\nruncmd:\n  - evil|bash",
	}
	for _, s := range bad {
		if err := ValidateUserData(s); err == nil {
			t.Fatalf("must reject: %s", s)
		}
	}
}

func TestInputsValidate(t *testing.T) {
	if err := validInputs().Validate(); err != nil {
		t.Fatalf("valid inputs: %v", err)
	}
	bad := validInputs()
	bad.SSHKeys = []string{"not-a-key"}
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid SSH key must error")
	}
}

func TestRenderUserData(t *testing.T) {
	i := validInputs()
	s, err := RenderUserData(i)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#cloud-config",
		"hostname: ct-1",
		"ssh_authorized_keys:",
		fakeSSHKey,
		"timezone: Asia/Shanghai",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("user-data missing %q: %s", want, s)
		}
	}
}

func TestRenderUserDataDefaults(t *testing.T) {
	i := validInputs()
	i.Timezone = ""
	i.User = ""
	s, err := RenderUserData(i)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "timezone: UTC") {
		t.Fatal("default timezone must be UTC")
	}
	if !strings.Contains(s, "name: admin") {
		t.Fatal("default user must be admin")
	}
}

func TestRenderMetaDataIncludesHostname(t *testing.T) {
	s, err := RenderMetaData(validInputs())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "local-hostname: ct-1") {
		t.Fatalf("metadata = %q", s)
	}
	instanceIDRE := regexp.MustCompile(`instance-id: iid-[0-9a-f]{16}`)
	if !instanceIDRE.MatchString(s) {
		t.Fatalf("metadata missing instance-id: %s", s)
	}
}

func TestRenderNetworkConfigDHCP(t *testing.T) {
	s := RenderNetworkConfig()
	if !strings.Contains(s, "dhcp4: true") {
		t.Fatalf("network-config missing dhcp4: %s", s)
	}
}

func TestEncodeSeedToBase64(t *testing.T) {
	encoded := EncodeSeedToBase64("hello")
	if encoded == "" {
		t.Fatal("encoded empty")
	}
	decoded, err := decodeB64(encoded)
	if err != nil || decoded != "hello" {
		t.Fatalf("roundtrip: %q err=%v", decoded, err)
	}
}

func decodeB64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestRenderNoCloudSeedAllParts(t *testing.T) {
	meta, user, net, err := RenderNoCloudSeed(validInputs())
	if err != nil {
		t.Fatal(err)
	}
	if meta == "" || user == "" || net == "" {
		t.Fatal("seed parts must all be non-empty")
	}
}

func TestCustomUserDataAppended(t *testing.T) {
	i := validInputs()
	i.UserData = "#cloud-config\nruncmd:\n  - echo hi\n"
	s, err := RenderUserData(i)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "runcmd:") {
		t.Fatal("custom user-data section must be present")
	}
}

func TestRenderLXCUserDataBase64(t *testing.T) {
	s, err := RenderLXCUserData(validInputs())
	if err != nil {
		t.Fatal(err)
	}
	// base64 must decode back to user-data
	decoded, err := decodeB64(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded, "#cloud-config") {
		t.Fatal("decoded LXC user-data must contain cloud-config header")
	}
}