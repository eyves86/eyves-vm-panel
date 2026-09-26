package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// F7/P2-11：节点 Token AES-GCM 静态加密回归。
func TestNodeTokenRoundTrip(t *testing.T) {
	plain := "node-secret-token-abc123"
	enc, err := EncryptNodeToken(plain)
	if err != nil {
		t.Fatalf("EncryptNodeToken: %v", err)
	}
	if !strings.HasPrefix(enc, "enc:v1:") {
		t.Fatalf("密文缺少前缀: %q", enc)
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("密文包含明文: %q", enc)
	}
	got, err := DecryptNodeToken(enc)
	if err != nil {
		t.Fatalf("DecryptNodeToken: %v", err)
	}
	if got != plain {
		t.Fatalf("解密后不匹配: got %q want %q", got, plain)
	}
	// 幂等：已加密值再次加密应原样返回。
	again, err := EncryptNodeToken(enc)
	if err != nil || again != enc {
		t.Fatalf("重复加密不幂等: %q vs %q (err=%v)", again, enc, err)
	}
}

func TestNodeTokenLegacyPlaintextPassthrough(t *testing.T) {
	// 存量明文（无前缀）应原样返回，保证升级兼容。
	got, err := DecryptNodeToken("legacy-plain-token")
	if err != nil {
		t.Fatalf("DecryptNodeToken(legacy): %v", err)
	}
	if got != "legacy-plain-token" {
		t.Fatalf("存量明文被改动: %q", got)
	}
	// 空串保持空串。
	if v, _ := EncryptNodeToken(""); v != "" {
		t.Fatalf("空串加密应返回空串, got %q", v)
	}
	if v, _ := DecryptNodeToken(""); v != "" {
		t.Fatalf("空串解密应返回空串, got %q", v)
	}
}

func TestNodeTokenTamperRejected(t *testing.T) {
	enc, err := EncryptNodeToken("secret-value")
	if err != nil {
		t.Fatalf("EncryptNodeToken: %v", err)
	}
	// 篡改密文尾部 → GCM 认证失败。
	tampered := enc[:len(enc)-2] + "aa"
	if _, err := DecryptNodeToken(tampered); err == nil {
		t.Fatal("篡改后的密文应解密失败")
	}
}

// 密钥文件自动生成：首次生成 0600 hex 文件，二次读取复用同一密钥。
func TestNodeTokenKeyFilePersist(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("EYVESCLOUD_NODE_TOKEN_KEY")

	oldPath := getConfigPath()
	defer SetConfigPath(oldPath)
	SetConfigPath(filepath.Join(dir, "eyvescloud.json"))

	// 重置进程级懒加载缓存，模拟冷启动。
	nodeTokenKeyOnce = sync.Once{}
	nodeTokenKeyVal, nodeTokenKeyErr = nil, nil
	defer func() {
		nodeTokenKeyOnce = sync.Once{}
		nodeTokenKeyVal, nodeTokenKeyErr = nil, nil
	}()

	key1, err := nodeTokenKey()
	if err != nil {
		t.Fatalf("首次生成密钥: %v", err)
	}
	// 再次加载（模拟重启）应读到同一密钥文件。
	nodeTokenKeyOnce = sync.Once{}
	nodeTokenKeyVal, nodeTokenKeyErr = nil, nil
	key2, err := nodeTokenKey()
	if err != nil {
		t.Fatalf("二次读取密钥: %v", err)
	}
	if string(key1) != string(key2) {
		t.Fatal("密钥文件重启后不一致")
	}
	// 密钥文件权限 0600。
	info, err := os.Stat(getDBPath() + ".tokenkey")
	if err != nil {
		t.Fatalf("密钥文件不存在: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("密钥文件权限 %v, want 0600", info.Mode().Perm())
	}
}
