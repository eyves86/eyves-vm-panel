package lxc

import (
	"testing"

	"eyvescloud/internal/config"
)

// TestLXCNATNetworkWiring 验证自愈模块使用的 NAT 网段计算，确保
// 网关、DHCP 范围、子网与 install.sh 写入 /etc/default/lxc-net 的值一致。
func TestLXCNATNetworkWiring(t *testing.T) {
	cfg := config.LXCNATNetwork()
	if cfg.Subnet != "10.0.3.0/24" {
		t.Fatalf("unexpected LXC subnet: %s", cfg.Subnet)
	}
	if cfg.Gateway != "10.0.3.1" {
		t.Fatalf("unexpected LXC gateway: %s", cfg.Gateway)
	}
	if cfg.DHCPStart != "10.0.3.2" {
		t.Fatalf("unexpected DHCP start: %s", cfg.DHCPStart)
	}
	if cfg.DHCPEnd != "10.0.3.254" {
		t.Fatalf("unexpected DHCP end: %s", cfg.DHCPEnd)
	}
	// 网关必须落在子网内（自愈配置才有效）。
	if !subnetContains(cfg.Subnet, cfg.Gateway) {
		t.Fatalf("gateway %s not inside subnet %s", cfg.Gateway, cfg.Subnet)
	}
}

// TestParseBridgeIPv4 覆盖网桥 IPv4 地址解析逻辑（10.0.3.1/24 形式）。
func TestParseBridgeIPv4(t *testing.T) {
	tests := []struct {
		ip     string
		gateway string
		want   bool
	}{
		{"10.0.3.1/24", "10.0.3.1", true},
		{"10.0.3.5/24", "10.0.3.1", false},
		{"169.254.74.19/16", "10.0.3.1", false},
		{"", "10.0.3.1", false},
	}
	for _, tc := range tests {
		if got := (bridgeIPv4AddressCIDR(tc.ip) == tc.gateway); got != tc.want {
			t.Errorf("bridgeIPv4AddressCIDR(%q)==%q got %v want %v", tc.ip, tc.gateway, got, tc.want)
		}
	}
}

// TestEnsureViaLXCNetServiceDetection 避免误判 lxc-net 服务可用性。
func TestEnsureViaLXCNetServiceDetection(t *testing.T) {
	// 不执行实际自愈（需要 root + 真实服务），仅验证查询不会 panic。
	_ = bridgeExists("__does_not_exist__")
	_ = dnsmasqActiveFor("__does_not_exist__")
	_ = isRoot()
}