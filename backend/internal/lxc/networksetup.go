package lxc

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"eyvescloud/internal/config"
)

// lxcNetworkEnsureMu 串行化 lxcbr0 网络自愈，避免并发创建/重启竞态。
var lxcNetworkEnsureMu sync.Mutex

// EnsureLXCBridgeNetwork 以「幂等 + 自愈」方式保证 lxcbr0 可用：
//  1. 检测 lxcbr0 是否存在，缺失则利用系统 lxc 自带的 lxc-net 脚本/服务重建；
//  2. 检测 lxcbr0 是否已分配本项目 LXC NAT 网段的网关 IP（默认 10.0.3.1/24）；
//     缺失则通过 lxc-net 服务补配（该服务会同时用 dnsmasq 提供 DHCP）；
//  3. 兜底：若无法借助 lxc-net（例如系统未安装 lxc 包），直接创建网桥 +
//     网关地址 + 用 dnsmasq 提供 DHCP + MASQUERADE 与 FORWARD 放行。
//
// 整个过程是幂等的：重复调用不会产生重复规则或重复地址。
// 它只作为“网络自愈”，不会主动销毁已有运行中容器或覆盖用户自定义配置。
func EnsureLXCBridgeNetwork() error {
	lxcNetworkEnsureMu.Lock()
	defer lxcNetworkEnsureMu.Unlock()

	if !isRoot() {
		// 非 root 下（如被控节点以受限用户运行时）保持可观测但不 panic。
		fmt.Println("Warning: skipped LXC bridge self-healing (requires root)")
		return nil
	}

	netCfg := config.LXCNATNetwork()
	gateway := netCfg.Gateway
	bridge := "lxcbr0"

	// 1) 若 lxcbr0 已存在且已分配目标网关 IP，且 dnsmasq DHCP 可达 → 直接认为就绪。
	if bridgeExists(bridge) {
		if ip := bridgeIPv4Address(bridge); ip == gateway {
			if dnsmasqActiveFor(bridge) || !commandExists("dnsmasq") {
				return nil
			}
		}
	}

	// 2) 借助系统 lxc-net（最优先，最贴合发行版规范）。
	if err := ensureViaLXCNet(bridge, gateway, netCfg); err == nil {
		applyLXCNATFirewall(bridge, netCfg.Subnet)
		return nil
	} else {
		fmt.Printf("lxc-net self-heal failed (%v), falling back to direct bridge setup\n", err)
	}

	// 3) 兜底：直接手工创建网桥 + dnsmasq。
	return ensureBridgeDirect(bridge, gateway, netCfg)
}

// bridgeExists 判断名字为 name 的 Linux bridge 是否存在。
func bridgeExists(name string) bool {
	out, err := exec.Command("ip", "link", "show", "dev", name).CombinedOutput()
	return err == nil && strings.Contains(string(out), "state UP")
}

// bridgeIPv4Address 返回网桥上第一个全局 IPv4 地址（CIDR 形式，用于与网关比较）。
func bridgeIPv4Address(name string) string {
	out, err := exec.Command("ip", "-4", "addr", "show", "dev", name).CombinedOutput()
	if err != nil {
		return ""
	}
	return bridgeIPv4AddressCIDRFromOutput(string(out))
}

// bridgeIPv4AddressCIDR 从单个 "10.0.3.1/24" 字符串解析地址部分，便于测试与复用。
func bridgeIPv4AddressCIDR(raw string) string {
	return addrPartFromCIDR(raw)
}

// bridgeIPv4AddressCIDRFromOutput 从 `ip -4 addr show` 输出中提取第一个 inet 地址。
func bridgeIPv4AddressCIDRFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1] // "10.0.3.1/24"
			}
		}
	}
	return ""
}

// addrPartFromCIDR 从 "10.0.3.1/24" 提取纯地址 "10.0.3.1"。
func addrPartFromCIDR(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "/"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// subnetContains 判断 addr 是否落在 cidr 子网内（IPv4）。
func subnetContains(cidr, addr string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	parsed, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	return prefix.Contains(parsed)
}

// dnsmasqActiveFor 粗略判断 dnsmasq 是否正在为指定网桥监听（存在监听进程即视为可用）。
func dnsmasqActiveFor(bridge string) bool {
	out, err := exec.Command("pgrep", "-a", "dnsmasq").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), bridge)
}

// ensureViaLXCNet 通过系统 lxc-net 自愈 lxcbr0。
// 策略：
//   - 已配置 lxc-net 服务（systemd/openrc/BSD rc）→ 直接 enable+restart；
//   - 未安装 lxc 包 → 返回错误交给直接创建兜底。
func ensureViaLXCNet(bridge, gateway string, cfg config.NATNetwork) error {
	// 检测是否有 lxc-net 初始化脚本或 systemd 单元。
	lxcNetUnit := ""
	if commandExists("systemctl") {
		out, err := exec.Command("systemctl", "list-unit-files", "lxc-net.service").CombinedOutput()
		if err == nil && strings.Contains(string(out), "lxc-net") {
			lxcNetUnit = "lxc-net.service"
		}
	}
	if lxcNetUnit == "" {
		if _, err := os.Stat("/etc/default/lxc-net"); err != nil {
			return fmt.Errorf("lxc-net not found (no systemd unit and no /etc/default/lxc-net)")
		}
		if commandExists("systemctl") {
			lxcNetUnit = "lxc-net.service"
		}
	}
	if lxcNetUnit == "" {
		return fmt.Errorf("no lxc-net service available")
	}

	// 若网桥不存在，lxc-net 的 up 脚本本应创建它；先尝试启动/重启服务。
	if !bridgeExists(bridge) {
		if commandExists("systemctl") {
			_ = exec.Command("systemctl", "enable", "--now", "lxc-net.service").Run()
		} else {
			_ = exec.Command("rc-service", "lxc-net", "start").Run()
		}
	}
	// 服务启动后再次确认。
	if !bridgeExists(bridge) {
		return fmt.Errorf("lxc-net did not create %s", bridge)
	}
	if bridgeIPv4Address(bridge) == gateway {
		return nil
	}
	// IP 仍未就位 → 重启服务以应用 /etc/default/lxc-net。
	if commandExists("systemctl") {
		_ = exec.Command("systemctl", "restart", "lxc-net.service").Run()
	} else {
		_ = exec.Command("rc-service", "lxc-net", "restart").Run()
	}
	if bridgeIPv4Address(bridge) != gateway {
		return fmt.Errorf("lxc-net still missing gateway %s", gateway)
	}
	return nil
}

// ensureBridgeDirect 兜底：直接创建网桥并手工配置 IP、dnsmasq、路由。
func ensureBridgeDirect(bridge, gateway string, cfg config.NATNetwork) error {
	if err := exec.Command("command", "-v", "brctl").Run(); err != nil {
		return fmt.Errorf("neither lxc-net nor brctl is available for LXC bridge setup")
	}
	if !bridgeExists(bridge) {
		if out, err := exec.Command("ip", "link", "add", bridge, "type", "bridge").CombinedOutput(); err != nil {
			return fmt.Errorf("failed to create bridge %s: %v, output: %s", bridge, err, string(out))
		}
		exec.Command("ip", "link", "set", bridge, "up").Run()
	}

	// 分配网关地址（若缺失）。
	if bridgeIPv4Address(bridge) != gateway {
		prefix, err := netip.ParsePrefix(cfg.Subnet)
		if err != nil {
			prefix = netip.MustParsePrefix("10.0.3.0/24")
		}
		exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%d", gateway, prefix.Bits()), "dev", bridge).Run()
		exec.Command("ip", "link", "set", bridge, "up").Run()
	}

	// 使用 dnsmasq 提供 DHCP。
	if err := ensureBridgeDNSMASQ(bridge, gateway, cfg); err != nil {
		return err
	}

	applyLXCNATFirewall(bridge, cfg.Subnet)
	return nil
}

// ensureBridgeDNSMASQ 为网桥启动/复用 dnsmasq，提供 DHCP 与 DNS。
func ensureBridgeDNSMASQ(bridge, gateway string, cfg config.NATNetwork) error {
	if !commandExists("dnsmasq") {
		return fmt.Errorf("dnsmasq is required for LXC DHCP")
	}
	if dnsmasqActiveFor(bridge) {
		return nil
	}
	// 使用 --interface 限定到网桥，避免影响宿主其他端口；--bind-interfaces 防止跨接口冒用。
	args := []string{
		"--interface=" + bridge,
		"--bind-interfaces",
		"--listen-address=" + gateway,
		"--dhcp-range=" + cfg.DHCPStart + "," + cfg.DHCPEnd + ",12h",
		"--dhcp-option=option:router," + gateway,
		"--dhcp-option=option:dns-server," + gateway + ",8.8.8.8",
		"--no-resolv",
		"--no-hosts",
		"--pid-file=" + filepath.Join("/run", "eyvescloud-dnsmasq-"+bridge+".pid"),
	}
	if out, err := exec.Command("dnsmasq", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start dnsmasq for %s: %v, output: %s", bridge, err, string(out))
	}
	return nil
}

// applyLXCNATFirewall 幂等配置 ip_forward、MASQUERADE 与 FORWARD/INPUT 放行。
func applyLXCNATFirewall(bridge, subnet string) {
	// 启用内核转发（systemd 下用 sysctl.d 持久化）。
	if commandExists("sysctl") {
		_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	}
	if commandExists("sysctl") {
		_ = exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").Run()
	}
	if _, err := os.Stat("/etc/sysctl.d/99-eyvescloud.conf"); os.IsNotExist(err) {
		_ = os.MkdirAll("/etc/sysctl.d", 0755)
		writeSysctlDefaults()
	}

	// MASQUERADE（网段等级，幂等）。
	if commandExists("iptables") {
		ensureNATRule("POSTROUTING", []string{"-s", subnet, "-o", "eth+", "-j", "MASQUERADE"})
	}
	// FORWARD / INPUT 放行。
	EnsureForwardRules(bridge)
	// 让流量能穿越 bridge（部分内核需要）。
	runQuiet("iptables", "-I", "FORWARD", "1", "-i", bridge, "-j", "ACCEPT")
	runQuiet("iptables", "-I", "FORWARD", "1", "-o", bridge, "-j", "ACCEPT")
}

// writeSysctlDefaults 写入持久的转发配置，幂等。
func writeSysctlDefaults() {
	content := "net.ipv4.ip_forward = 1\n" +
		"net.ipv6.conf.all.forwarding = 1\n" +
		"net.bridge.bridge-nf-call-iptables = 0\n" +
		"net.bridge.bridge-nf-call-ip6tables = 0\n"
	_ = os.WriteFile("/etc/sysctl.d/99-eyvescloud.conf", []byte(content), 0644)
	if commandExists("sysctl") {
		_ = exec.Command("sysctl", "--system").Run()
	}
}

// isRoot 判断当前进程是否以 root 运行。
func isRoot() bool {
	return os.Geteuid() == 0
}