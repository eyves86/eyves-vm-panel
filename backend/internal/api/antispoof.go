package api

import (
	"os/exec"
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
//   - 依赖工具缺失时安全跳过（best-effort），不影响主流程。

const (
	antiSpoofChain        = "EYVESCLOUD_ANTISPOOF"
	antiSpoofTableComment = "eyvescloud anti-spoof"
)

var antiSpoofMu sync.Mutex

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
		removeAntiSpoofChain("iptables")
		removeAntiSpoofChain("arptables")
		return
	}

	if !ensureAntiSpoofChain("iptables") {
		return
	}
	// 每轮先清空自有链再重建，保证规则与当前容器集合一致（自愈、无残留）。
	_ = exec.Command("iptables", "-F", antiSpoofChain).Run()

	// arptables 为可选增强（ARP 层防伪），工具缺失或链不可用时自动跳过。
	arpReady := ensureAntiSpoofChain("arptables")
	if arpReady {
		_ = exec.Command("arptables", "-F", antiSpoofChain).Run()
	}

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
			if address == "" {
				continue
			}
			iface := strings.TrimSpace(ip.Interface)

			// IP 层：只允许绑定 MAC 使用该 IP 转发。
			for _, rule := range antiSpoofIPRules(address, iface, mac) {
				appendChainRule("iptables", rule)
			}

			// ARP 层：拒绝其它 MAC 冒充该 IP 的 ARP 报文。
			if arpReady {
				appendChainRule("arptables", antiSpoofARPRules(address, iface, mac))
			}
		}
	}
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
	// 注意：不同 arptables 实现的 MAC 匹配参数略有差异，此处使用广泛支持的 --source-mac。
	return append(rule, "-s", address, "-m", "mac", "--source-mac", "!", mac, "-j", "DROP")
}

// ensureAntiSpoofChain 创建专用链并确保父链跳转到它；返回该工具是否可用。
func ensureAntiSpoofChain(tool string) bool {
	if _, err := exec.LookPath(tool); err != nil {
		return false
	}
	// 创建链；已存在则忽略错误。
	_ = exec.Command(tool, "-N", antiSpoofChain).Run()
	if exec.Command(tool, "-L", antiSpoofChain, "-n").Run() != nil {
		return false
	}

	parent := "FORWARD"
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
func removeAntiSpoofChain(tool string) {
	if _, err := exec.LookPath(tool); err != nil {
		return
	}
	if exec.Command(tool, "-L", antiSpoofChain, "-n").Run() != nil {
		return
	}
	jump := []string{"FORWARD", "-j", antiSpoofChain}
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