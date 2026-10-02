package api

// ssl_test.go —— TLS 证书生成 / 判定 / 模式解析回归测试。
//
// 背景：面板自带完整 TLS 能力（自签 ECDSA、Let's Encrypt、上传证书，含自动
// 续期监控），但生产默认未启用。启用 TLS 属于"上线商业化"前的阻断项，所以
// 先把证书逻辑锁死——避免真正去启用时才发现证书生成或目标匹配是坏的。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// withSSLTestConfig 准备一个隔离的配置库（数据目录重定向到临时目录）。
// 必须在测试里真正初始化配置库，因为 SSL 更新路径会调用 SaveConfig()；
// 不隔离会让测试写到真实的数据目录（生产 $HOME/.eyvescloud）。
func withSSLTestConfig(t *testing.T) *config.EyvescloudConfig {
	t.Helper()
	dir := t.TempDir()
	previous := config.GetTestConfig()
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	os.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.RestoreTestConfig(previous)
		config.SetConfigPath("")
		os.Unsetenv("EYVESCLOUD_DATA_DIR")
	})

	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	return cfg
}

// TestGenerateSelfSignedCertificateForIP IP 目标的自签证书：生成、权限、可用性判定。
func TestGenerateSelfSignedCertificateForIP(t *testing.T) {
	withSSLTestConfig(t)

	const ip = "203.0.113.10"
	certPath, keyPath, err := generateSelfSignedCertificate(ip)
	if err != nil {
		t.Fatalf("生成自签证书失败: %v", err)
	}
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("证书文件缺失 %s: %v", p, err)
		}
	}
	if info, err := os.Stat(keyPath); err == nil && info.Mode().Perm() != 0o600 {
		t.Fatalf("私钥权限应为 0600，实际 %v", info.Mode().Perm())
	}

	if !certificateUsable(certPath, keyPath, ip) {
		t.Fatal("刚生成的自签证书应判定为可用")
	}
	if certificateUsable(certPath, keyPath, "198.51.100.7") {
		t.Fatal("目标不匹配的证书不该判定为可用")
	}
	if certificateNeedsRenewal(certPath, keyPath, ip, 24*time.Hour) {
		t.Fatal("刚签发（有效期 1 年）的证书不该判定为需续期")
	}
}

// TestGenerateSelfSignedCertificateForDNS 域名目标：应写入 DNSNames 且能通过 VerifyHostname。
func TestGenerateSelfSignedCertificateForDNS(t *testing.T) {
	withSSLTestConfig(t)

	const host = "panel.example.com"
	certPath, keyPath, err := generateSelfSignedCertificate(host)
	if err != nil {
		t.Fatalf("生成自签证书失败: %v", err)
	}
	if !certificateUsable(certPath, keyPath, host) {
		t.Fatal("域名自签证书应判定为可用")
	}
	if certificateUsable(certPath, keyPath, "other.example.com") {
		t.Fatal("域名不匹配的证书不该判定为可用")
	}

	cert, err := readLeafCertificate(certPath)
	if err != nil {
		t.Fatalf("读取证书失败: %v", err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != host {
		t.Fatalf("DNSNames 应为 [%s]，实际 %v", host, cert.DNSNames)
	}
	if len(cert.IPAddresses) != 0 {
		t.Fatalf("域名证书不该带 IP SAN，实际 %v", cert.IPAddresses)
	}
}

// TestSSLErrorHandlingWhenCertificateMissing 证书路径存在但文件缺失时必须判为不可用，
// 而不是 panic 或误判可用（否则会以"启用 TLS"状态起服却加载不了证书）。
func TestSSLErrorHandlingWhenCertificateMissing(t *testing.T) {
	withSSLTestConfig(t)

	if certificateUsable("/nonexistent/cert.pem", "/nonexistent/key.pem", "example.com") {
		t.Fatal("不存在的证书文件必须判定为不可用")
	}
	if !certificateNeedsRenewal("/nonexistent/cert.pem", "/nonexistent/key.pem", "example.com", time.Hour) {
		t.Fatal("证书文件缺失时必须判定为需要（重新）签发")
	}
	if err := validateCertificatePair("/nonexistent/cert.pem", "/nonexistent/key.pem"); err == nil {
		t.Fatal("缺失的证书对应返回错误")
	}
}

// TestSelfSignedModeReusesValidCertificate self_signed 模式：已有可用证书时不应重复签发。
func TestSelfSignedModeReusesValidCertificate(t *testing.T) {
	cfg := withSSLTestConfig(t)
	// 排除前序测试残留的证书槽位：本轮数据目录是临时的，残留路径指向已删除的
	// 文件，会让"证书可用性"判定失败而重新签发，测试就测不到复用逻辑了。
	cfg.SSLCertificates = nil

	const ip = "203.0.113.20"
	first, err := resolveSSLModeCertificate(config.SSLModeSelfSigned, ip, "", "", "")
	if err != nil {
		t.Fatalf("首次签发失败: %v", err)
	}
	if first.CertPath == "" || first.KeyPath == "" {
		t.Fatal("首次签发应返回证书路径")
	}
	// 生产路径（updateSSLSettings）会把结果落到槽位；不落槽时下次解析拿不到
	// 既有证书路径，会重复签发——这里按生产语义落槽。
	saveSSLSlot(first)
	stat1, err := os.Stat(first.CertPath)
	if err != nil {
		t.Fatalf("证书文件缺失: %v", err)
	}

	second, err := resolveSSLModeCertificate(config.SSLModeSelfSigned, ip, "", "", "")
	if err != nil {
		t.Fatalf("二次解析失败: %v", err)
	}
	stat2, err := os.Stat(second.CertPath)
	if err != nil {
		t.Fatalf("证书文件缺失: %v", err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Fatal("已有可用证书时不应重新签发（文件 mtime 变化了）")
	}
}

// TestNormalizeSSLCertificateTargetRejectsPathTraversal 目标里的路径字符必须被拒，
// 否则证书文件名会被拼接成目录穿越。
func TestNormalizeSSLCertificateTargetRejectsPathTraversal(t *testing.T) {
	bad := []string{"", "../../etc/passwd", "a/b", `a\b`, "..", "this is not a host"}
	for _, target := range bad {
		if _, err := config.NormalizeSSLCertificateTarget(target); err == nil {
			t.Fatalf("非法目标 %q 应被拒绝", target)
		}
	}
	good := map[string]string{
		"203.0.113.10":      "203.0.113.10",
		"[2001:db8::1]":     "2001:db8::1",
		"Panel.Example.COM": "panel.example.com",
	}
	for in, want := range good {
		got, err := config.NormalizeSSLCertificateTarget(in)
		if err != nil {
			t.Fatalf("合法目标 %q 被拒: %v", in, err)
		}
		if got != want {
			t.Fatalf("目标 %q 归一化应为 %q，实际 %q", in, want, got)
		}
	}
}
