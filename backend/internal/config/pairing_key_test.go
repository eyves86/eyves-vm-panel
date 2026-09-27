package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFirstBootGeneratesAgentPairingKey 验证全新安装首启即自带节点对接密钥：
// 内存与落库值一致、24h 有效、并写入首启凭据文件（安装脚本据此展示对接信息）。
func TestFirstBootGeneratesAgentPairingKey(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	dir := t.TempDir()
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	SetConfigPath(filepath.Join(dir, "config.json"))

	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AgentPairingKey) != 64 {
		t.Fatalf("expected 64-char hex pairing key, got %d chars: %q", len(cfg.AgentPairingKey), cfg.AgentPairingKey)
	}
	expiry, err := time.Parse(time.RFC3339, cfg.AgentPairingKeyExpiry)
	if err != nil {
		t.Fatalf("pairing key expiry not RFC3339: %v", err)
	}
	if d := time.Until(expiry); d <= 0 || d > 25*time.Hour {
		t.Fatalf("pairing key expiry out of 24h window: %v", d)
	}

	// 首启凭据文件应包含密钥与有效期（运维/安装脚本可见）。
	creds, err := os.ReadFile(filepath.Join(cfg.DataDir, FirstBootCredsFile))
	if err != nil {
		t.Fatalf("first boot credentials file missing: %v", err)
	}
	if !strings.Contains(string(creds), cfg.AgentPairingKey) {
		t.Fatalf("credentials file does not contain the pairing key")
	}
	if !strings.Contains(string(creds), cfg.AgentPairingKeyExpiry) {
		t.Fatalf("credentials file does not contain the pairing key expiry")
	}

	// 重启（重新加载）后密钥仍应存在且不变。
	resetConfigStoreForTest(t)
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	SetConfigPath(filepath.Join(dir, "config.json"))
	cfg2, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.AgentPairingKey != cfg.AgentPairingKey {
		t.Fatalf("pairing key not persisted across reload: %q != %q", cfg2.AgentPairingKey, cfg.AgentPairingKey)
	}
}
