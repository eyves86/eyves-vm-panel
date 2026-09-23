package api

import (
	"net"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"eyvescloud/internal/config"
)

// IP 防伪（anti-spoof）执行层：把「平台分配的 IPv4」与其绑定的 MAC 绑定，
// 使容器无法盗用其它 IP（同源 IP 但非绑定 MAC 的转发报文被丢弃）。
//
// 安全设计：
//   - 只操作本项目自有的专用链（iptables EYVESCLOUD_ANTISPOOF / arptables EYVESCLOUD_ANTISPOOF），
//     不触碰其它链或规则，关闭功能时可整体删除、完全可逆；
//   - 仅当设置项开启时生效，默认关闭，避免影响既有网络；
//   - 仅接受合法 IPv4 地址，避免把 0.0.0.0/0 之类过宽网段写入规则；
//   - 依赖工具缺失时安全跳过（best-effort），不影响主流程；
//   - 规则集用指纹去重，仅在期望规则变化时才重建，避免周期性抖动。

const (
	antiSpoofChain = "EYVESCLOUD_ANTISPOOF"
	// iptables 的 IPv4 过滤在 FORWARD 链执行（拦截容器转发报文）；
	// arptables 的 filter 表只有 INPUT/OUTPUT（nft 版没有 FORWARD 链），
	// 因此 ARP 层规则挂在 INPUT 上，拦截伪造源 IP 的 ARP 帧进入本机邻居表。
	antiSpoofIPParent  = "FORWARD"
	antiSpoofARPParent = "INPUT"
)

var (
	antiSpoofMu          sync.Mutex
	antiSpoofFingerprint string
)

type antiSpoofTarget struct {
	address string
	iface   string
	mac     string
}

// reconcileIPAntiSpoof 按当前设置维护 IPv4 防伪规则。
// 开启时：为所有运行中容器的公网 IPv4 建立 MAC 绑定规则；
// 关闭时：删除专用链，恢复原状。
func reconcileIPAntiSpoof() {
	antiSpoofMu.Lock()
	defer antiSpoofMu.Unlock()

	enabled := false
	config.AppConfigMu.RLock()
	if config.AppConfig != nil {
		enabled = config.AppConfig.IPAntiSpoofEnabled
	}
	config.AppConfigMu.RUnlock()

	if !enabled {
		if antiSpoofFingerprint != "" {
			removeAntiSpoofChain("iptables", antiSpoofIPParent)
			removeAntiSpoofChain("arptables", antiSpoofARPParent)
			antiSpoofFingerprint = ""
		}
		return
	}

	targets, fingerprint := desiredAntiSpoofRules()
	// 期望规则未变化时直接跳过，避免每轮 flush/重建造成的规则抖动与短暂空窗。
	if fingerprint == antiSpoofFingerprint {
		return
	}

	if !ensureAntiSpoofChain("iptables", antiSpoofIPParent) {
		return
	}
	_ = exec.Command("iptables", "-F", antiSpoofChain).Run()

	// arptables 为可选增强（ARP 层防伪），工具缺失或链不可用时自动跳过。
	arpReady := ensureAntiSpoofChain("arptables", antiSpoofARPParent)
	if arpReady {
		_ = exec.Command("arptables", "-F", antiSpoofChain).Run()
	}

	for _, target := range targets {
		for _, rule := range antiSpoofIPRules(target.address, target.iface, target.mac) {
			appendChainRule("iptables", rule)
		}
		if arpReady {
			appendChainRule("arptables", antiSpoofARPRules(target.address, target.iface, target.mac))
		}
	}
	antiSpoofFingerprint = fingerprint
}

// desiredAntiSpoofRules 汇总当前应为哪些容器建立防伪规则，并给出用于去重的指纹。
func desiredAntiSpoofRules() ([]antiSpoofTarget, string) {
	targets := make([]antiSpoofTarget, 0)
	for _, c := range config.GetContainers() {
		if c.Status != "running" {
			continue
		}
		mac := strings.ToLower(strings.TrimSpace(c.MACAddress))
		if mac == "" {
			continue
		}
		for _, ip := range c.PublicIPv4s {
			address := strings.TrimSpace(ip.Address)
			parsed := net.ParseIP(address)
			// 必须是合法 IPv4，避免把 0.0.0.0/0 之类过宽网段写进规则造成过度拦截。
			if parsed == nil || parsed.To4() == nil {
				continue
			}
			targets = append(targets, antiSpoofTarget{
				address: address,
				iface:   strings.TrimSpace(ip.Interface),
				mac:     mac,
			})
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].address != targets[j].address {
			return targets[i].address < targets[j].address
		}
		return targets[i].mac < targets[j].mac
	})

	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, target.address+"|"+target.iface+"|"+target.mac)
	}
	return targets, strings.Join(parts, ",")
}

// antiSpoofIPRules 返回 IPv4 防伪规则：接受绑定 MAC，丢弃其它 MAC 冒用该 IP。
func antiSpoofIPRules(address, iface, mac string) [][]string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	base := []string{antiSpoofChain}
	if iface != "" {
		base = append(base, "-i", iface)
	}
	accept := append(append([]string{}, base...), "-s", address+"/32", "-m", "mac", "--mac-source", mac, "-j", "ACCEPT")
	drop := append(append([]string{}, base...), "-s", address+"/32", "-j", "DROP")
	return [][]string{accept, drop}
}

// antiSpoofARPRules 返回 ARP 防伪规则：拒绝非绑定 MAC 声称持有该 IP。
func antiSpoofARPRules(address, iface, mac string) []string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	rule := []string{antiSpoofChain}
	if iface != "" {
		rule = append(rule, "-i", iface)
	}
	// arptables 的 --source-mac 属于核心规则项（不是 -m mac 扩展），取反需写成
	// "! --source-mac"：丢弃源 IP 为 address、但源 MAC 非绑定 MAC 的 ARP 帧。
	return append(rule, "-s", address, "!", "--source-mac", mac, "-j", "DROP")
}

// ensureAntiSpoofChain 创建专用链并确保父链跳转到它；返回该工具是否可用。
func ensureAntiSpoofChain(tool, parent string) bool {
	if _, err := exec.LookPath(tool); err != nil {
		return false
	}
	// 创建链；已存在则忽略错误。
	_ = exec.Command(tool, "-N", antiSpoofChain).Run()
	if exec.Command(tool, "-L", antiSpoofChain, "-n").Run() != nil {
		return false
	}

	jump := []string{parent, "-j", antiSpoofChain}
	if exec.Command(tool, append([]string{"-C"}, jump...)...).Run() != nil {
		// arptables 老版本不支持 -C；失败时直接尝试插入。
		_ = exec.Command(tool, append([]string{"-I", parent, "1"}, jump...)...).Run()
	}
	return true
}

// appendChainRule 向专用链追加一条规则。
func appendChainRule(tool string, rule []string) {
	if len(rule) == 0 {
		return
	}
	_ = exec.Command(tool, append([]string{"-A"}, rule...)...).Run()
}

// removeAntiSpoofChain 从父链解绑并删除专用链，恢复原状。
func removeAntiSpoofChain(tool, parent string) {
	if _, err := exec.LookPath(tool); err != nil {
		return
	}
	if exec.Command(tool, "-L", antiSpoofChain, "-n").Run() != nil {
		return
	}
	jump := []string{parent, "-j", antiSpoofChain}
	for i := 0; i < 8; i++ {
		if exec.Command(tool, append([]string{"-C"}, jump...)...).Run() != nil {
			break
		}
		if exec.Command(tool, append([]string{"-D"}, jump...)...).Run() != nil {
			break
		}
	}
	_ = exec.Command(tool, "-F", antiSpoofChain).Run()
	_ = exec.Command(tool, "-X", antiSpoofChain).Run()
}
