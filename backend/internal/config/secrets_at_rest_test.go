package config

import (
	"strings"
	"testing"
)

// TestSecretAtRestRoundTrip 验证凭据静态加密的基本往返与空值语义。
func TestSecretAtRestRoundTrip(t *testing.T) {
	const plain = "Sup3r-Secret-Passw0rd!"
	enc := EncryptSecretAtRest(plain)
	if enc == "" {
		t.Fatal("encrypted value must not be empty")
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("ciphertext leaks plaintext: %q", enc)
	}
	if !strings.HasPrefix(enc, nodeTokenEncPrefix) {
		t.Fatalf("ciphertext must carry the enc:v1: prefix, got %q", enc)
	}
	if got := DecryptSecretAtRest(enc); got != plain {
		t.Fatalf("DecryptSecretAtRest = %q, want %q", got, plain)
	}
	if got := EncryptSecretAtRest(""); got != "" {
		t.Fatalf("empty value must stay empty, got %q", got)
	}
	if got := DecryptSecretAtRest(""); got != "" {
		t.Fatalf("empty ciphertext must stay empty, got %q", got)
	}
	// 存量明文（无前缀）必须原样通过，保证升级路径透明。
	if got := DecryptSecretAtRest("legacy-plaintext"); got != "legacy-plaintext" {
		t.Fatalf("legacy plaintext must pass through, got %q", got)
	}
	// 损坏的密文一律返回空串，绝不能把密文当明文用。
	if got := DecryptSecretAtRest(nodeTokenEncPrefix + "!!!not-base64!!!"); got != "" {
		t.Fatalf("corrupted ciphertext must decrypt to empty, got %q", got)
	}
	// 两次加密同一明文必须得到不同密文（随机 nonce）。
	if EncryptSecretAtRest(plain) == enc {
		t.Fatal("nonce reuse detected: identical ciphertext for the same plaintext")
	}
}

// TestExportImportSecretsRoundTrip 验证配置备份/迁移的导出加密与导入解密闭环：
// 导出副本中不得出现明文凭据，导入后必须还原为明文。
func TestExportImportSecretsRoundTrip(t *testing.T) {
	original := EyvescloudConfig{
		TurnstileSecretKey: "turnstile-secret",
		AgentPairingKey:    "pairing-key-value",
		Containers: []Container{
			{ID: 1, Name: "ct-1", SSHPassword: "container-ssh-pw"},
		},
		Nodes: []Node{
			{ID: "n1", Name: "node-1", Token: "node-token-value", InstallKey: "install-key-value"},
		},
		SubUsers: []SubUser{
			{ID: "su1", Username: "alice", Password: "sub-user-pw", AccessCode: "access-code"},
		},
		Tasks: []SavedTask{
			{ID: "t1", Type: "create", Config: `{"name":"ct-9","ssh_password":"task-ssh-pw","ram_mb":1024}`},
		},
	}

	imported := original
	// 深拷贝切片（模拟 JSON 往返），避免就地加密污染"原始内存态"。
	imported.Containers = append([]Container(nil), original.Containers...)
	imported.Nodes = append([]Node(nil), original.Nodes...)
	imported.SubUsers = append([]SubUser(nil), original.SubUsers...)
	imported.Tasks = append([]SavedTask(nil), original.Tasks...)

	EncryptSecretsForExport(&imported)

	// 导出副本中不得出现任何明文凭据。
	for _, secret := range []string{
		"container-ssh-pw", "node-token-value", "install-key-value",
		"turnstile-secret", "pairing-key-value", "sub-user-pw", "access-code", "task-ssh-pw",
	} {
		if containsSecret(&imported, secret) {
			t.Fatalf("exported copy still contains plaintext secret %q", secret)
		}
	}

	// 导入解密后必须与原始明文一致。
	if failed := DecryptSecretsAfterImport(&imported); len(failed) != 0 {
		t.Fatalf("unexpected undecryptable fields: %v", failed)
	}
	if imported.Containers[0].SSHPassword != "container-ssh-pw" {
		t.Fatalf("container ssh password not restored: %q", imported.Containers[0].SSHPassword)
	}
	if imported.Nodes[0].Token != "node-token-value" || imported.Nodes[0].InstallKey != "install-key-value" {
		t.Fatalf("node credentials not restored: %+v", imported.Nodes[0])
	}
	if imported.TurnstileSecretKey != "turnstile-secret" || imported.AgentPairingKey != "pairing-key-value" {
		t.Fatalf("panel secrets not restored")
	}
	if imported.SubUsers[0].Password != "sub-user-pw" || imported.SubUsers[0].AccessCode != "access-code" {
		t.Fatalf("sub-user credentials not restored: %+v", imported.SubUsers[0])
	}
	if !strings.Contains(imported.Tasks[0].Config, "task-ssh-pw") {
		t.Fatalf("task ssh password not restored: %s", imported.Tasks[0].Config)
	}
	// 任务负载的其它字段必须原样保留。
	if !strings.Contains(imported.Tasks[0].Config, `"ram_mb":1024`) {
		t.Fatalf("task config payload was corrupted: %s", imported.Tasks[0].Config)
	}
}

// TestDecryptSecretsAfterImportReportsUndecryptable 验证跨密钥导入时字段被置空
// 并回报字段名（而不是把密文当明文写回）。
func TestDecryptSecretsAfterImportReportsUndecryptable(t *testing.T) {
	cfg := EyvescloudConfig{
		// 合法格式但密钥不同 → 解密失败（GCM 认证失败）。
		TurnstileSecretKey: "enc:v1:" + strings.Repeat("A", 44),
		Containers:         []Container{{ID: 2, Name: "ct-2", SSHPassword: "enc:v1:" + strings.Repeat("B", 44)}},
	}
	failed := DecryptSecretsAfterImport(&cfg)
	if len(failed) != 2 {
		t.Fatalf("expected 2 undecryptable fields, got %v", failed)
	}
	if cfg.TurnstileSecretKey != "" {
		t.Fatalf("undecryptable secret must be cleared, got %q", cfg.TurnstileSecretKey)
	}
	if cfg.Containers[0].SSHPassword != "" {
		t.Fatalf("undecryptable container password must be cleared, got %q", cfg.Containers[0].SSHPassword)
	}
}

func containsSecret(cfg *EyvescloudConfig, secret string) bool {
	if secret == "" {
		return false
	}
	if strings.Contains(cfg.TurnstileSecretKey, secret) || strings.Contains(cfg.AgentPairingKey, secret) {
		return true
	}
	for _, c := range cfg.Containers {
		if strings.Contains(c.SSHPassword, secret) {
			return true
		}
	}
	for _, n := range cfg.Nodes {
		if strings.Contains(n.Token, secret) || strings.Contains(n.InstallKey, secret) {
			return true
		}
	}
	for _, su := range cfg.SubUsers {
		if strings.Contains(su.Password, secret) || strings.Contains(su.AccessCode, secret) {
			return true
		}
	}
	for _, task := range cfg.Tasks {
		if strings.Contains(task.Config, secret) {
			return true
		}
	}
	return false
}
