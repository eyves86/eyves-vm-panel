package agent

import "testing"

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