package agent

import (
	"testing"

	"eyvescloud/internal/config"
)

// TestInitSecureTransportRequiresHTTPS 保障节点主控通道默认要求 https，
// 杜绝节点 token 明文传输；仅显式 --allow-insecure-http 时放行 http。
func TestInitSecureTransportRequiresHTTPS(t *testing.T) {
	if err := initSecureTransport("https://master.example:18999", false); err != nil {
		t.Fatalf("https should be allowed without opt-in: %v", err)
	}
	if err := initSecureTransport("HTTPS://master.example:18999", false); err != nil {
		t.Fatalf("https (uppercase) should be allowed: %v", err)
	}
	// 明文 http 且未获豁免 → 必须拒绝。
	if err := initSecureTransport("http://master.example:18999", false); err == nil {
		t.Fatal("plaintext http without opt-in must be rejected")
	}
	// 显式豁免 → 允许（并仅在此场景）。
	if err := initSecureTransport("http://master.example:18999", true); err != nil {
		t.Fatalf("plaintext http with explicit opt-in should be allowed: %v", err)
	}
	// 非法 scheme → 拒绝。
	if err := initSecureTransport("ftp://master.example:18999", true); err == nil {
		t.Fatal("non-http(s) scheme must be rejected")
	}
}

// TestNormalizeSelfAddressByPanelScheme 回归 F9：--addr 无 scheme 时按本机
// 面板真实协议补全（http 面板必须显式声明 http://，否则主控默认 https 会断链）。
func TestNormalizeSelfAddressByPanelScheme(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })

	// 面板未启用 SSL → 无 scheme 地址补 http://
	config.AppConfig = &config.EyvescloudConfig{}
	if got := normalizeSelfAddress("10.0.0.2:8999"); got != "http://10.0.0.2:8999" {
		t.Fatalf("http panel: normalizeSelfAddress = %q, want http://10.0.0.2:8999", got)
	}
	// 已带 scheme → 原样保留（含显式 https://）
	if got := normalizeSelfAddress("http://10.0.0.2:8999"); got != "http://10.0.0.2:8999" {
		t.Fatalf("explicit http: got %q", got)
	}
	if got := normalizeSelfAddress("https://10.0.0.2:8999"); got != "https://10.0.0.2:8999" {
		t.Fatalf("explicit https: got %q", got)
	}

	// 面板启用 SSL → 无 scheme 地址补 https://
	config.AppConfig = &config.EyvescloudConfig{SSL: config.SSLConfig{Enabled: true}}
	if got := normalizeSelfAddress("10.0.0.2:8999"); got != "https://10.0.0.2:8999" {
		t.Fatalf("https panel: normalizeSelfAddress = %q, want https://10.0.0.2:8999", got)
	}
	if selfPanelScheme() != "https" {
		t.Fatalf("selfPanelScheme = %q, want https", selfPanelScheme())
	}
}