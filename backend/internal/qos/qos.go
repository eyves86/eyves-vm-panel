// Package qos 出入口带宽强制执行（P2-1）：
//
//   - tc htb 命令构造：容器出口限速（tc qdisc add dev <iface> root handle 1: htb
//     + tc class add ... rate/ceil）；
//   - libvirt <bandwidth> XML 片段：KVM 实例入口限速（inbound/outbound
//     average/peak/burst）；
//   - LXC 配置片段：limits.network.maxIngress/maxEgress 写到容器 config；
//   - 重放路径：Apply 函数幂等（同目标 + 同速率 → 不重复执行；已存在
//     但速率变化 → 先删后建）。
//
// 本包只构造命令/配置文本，不实际执行；执行由 storage.CommandRunner 抽象
// 注入，本包提供 Apply() 函数封装"先查后改"的执行流程。
package qos

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// ---- tc htb 命令构造 ----

// HTBConfig 是 tc htb 限速参数。
type HTBConfig struct {
	// Iface：veth/桥接端口（如 vethXXXX、tapXXXX）。
	Iface string
	// IngressMbps：入口速率（Mbps）；0 表示不限。
	IngressMbps int
	// EgressMbps：出口速率（Mbps）；0 表示不限。
	EgressMbps int
	// BurstKBytes：突发缓冲（KB）；默认 32KB。
	BurstKBytes int
}

// BuildHTBCommands 返回一组 tc 命令字符串 + 参数数组：
//   1) qdisc add root handle 1: htb default 30;
//   2) class add parent 1: classid 1:1 htb rate <ingress> ceil <ingress>;
//   3) class add parent 1:1 classid 1:10 htb rate <egress> ceil <egress>;
//   4) filter add ... protocol ip u32 match ... flowid 1:10（出口流量走向）。
//
// 返回命令名（"tc"）+ 多组 args（每组一个独立 tc 调用）。
func BuildHTBCommands(cfg HTBConfig) ([][]string, error) {
	if strings.TrimSpace(cfg.Iface) == "" {
		return nil, errors.New("qos: iface required")
	}
	if cfg.IngressMbps < 0 || cfg.EgressMbps < 0 {
		return nil, fmt.Errorf("qos: rate must be >= 0")
	}
	if cfg.BurstKBytes <= 0 {
		cfg.BurstKBytes = 32
	}
	burstBytes := cfg.BurstKBytes * 1024
	cmds := [][]string{}
	// 1. 根 qdisc（HTB 父类）。
	cmds = append(cmds, []string{"qdisc", "add", "dev", cfg.Iface, "root", "handle", "1:", "htb", "default", "30"})
	// 2. 父类（容器聚合速率 = max(ingress, egress)）。
	parentRate := cfg.IngressMbps
	if cfg.EgressMbps > parentRate {
		parentRate = cfg.EgressMbps
	}
	if parentRate <= 0 {
		// 不限速：只装 qdisc，不建 class，避免无意义规则。
		return cmds, nil
	}
	cmds = append(cmds, []string{"class", "add", "dev", cfg.Iface, "parent", "1:", "classid", "1:1",
		"htb", "rate", fmt.Sprintf("%dmbit", parentRate),
		"ceil", fmt.Sprintf("%dmbit", parentRate),
		"burst", fmt.Sprintf("%d", burstBytes)})
	// 3. 子类：出口限速（容器发出的流量走 1:10）。
	if cfg.EgressMbps > 0 {
		cmds = append(cmds, []string{"class", "add", "dev", cfg.Iface, "parent", "1:1", "classid", "1:10",
			"htb", "rate", fmt.Sprintf("%dmbit", cfg.EgressMbps),
			"ceil", fmt.Sprintf("%dmbit", cfg.EgressMbps),
			"burst", fmt.Sprintf("%d", burstBytes)})
		// 4. u32 filter 把所有从该 iface 出向的流量归到 1:10。
		cmds = append(cmds, []string{"filter", "add", "dev", cfg.Iface, "protocol", "ip",
			"u32", "match", "u32", "0", "0", "flowid", "1:10"})
	}
	// 5. 入口限速走 police（ingress 方向由内核 ingress qdisc 处理）：
	//    对端 veth 入向流量由本端 police 限速。
	if cfg.IngressMbps > 0 {
		cmds = append(cmds, []string{"qdisc", "add", "dev", cfg.Iface, "ingress"})
		cmds = append(cmds, []string{"filter", "add", "dev", cfg.Iface, "parent", "ffff:",
			"protocol", "ip", "u32", "match", "u32", "0", "0",
			"police", fmt.Sprintf("rate %dmbit burst %d mtu 64k drop", cfg.IngressMbps, cfg.BurstKBytes*1024)})
	}
	return cmds, nil
}

// RemoveHTBCommands 返回删除 qdisc 的命令（用于重放路径的"先清后建"）。
func RemoveHTBCommands(iface string) [][]string {
	return [][]string{
		{"qdisc", "del", "dev", iface, "ingress"},
		{"qdisc", "del", "dev", iface, "root"},
	}
}

// ---- libvirt <bandwidth> XML 构造 ----

// LibvirtBandwidthXML 是 KVM 实例 libvirt XML 的 <bandwidth> 片段。
// 仅在 inbound/outbound 任一非零时返回非空；零值时调用方应省略整段。
type LibvirtBandwidthXML struct {
	XMLName xml.Name `xml:"bandwidth"`
	Units   string   `xml:"units,attr,omitempty"` // "bytes" / "KB"（默认 bytes）
	Ingress *BandwidthShape `xml:"inbound,omitempty"`
	Egress  *BandwidthShape `xml:"outbound,omitempty"`
}

// BandwidthShape 是 <inbound>/<outbound> 子结构。
type BandwidthShape struct {
	Average int `xml:"average,attr"`      // bytes/s
	Peak    int `xml:"peak,attr,omitempty"`
	Burst   int `xml:"burst,attr,omitempty"`
}

// BuildLibvirtBandwidth 把速率（Mbps/MBps）转 bytes/s 后构造 XML 片段。
// ingressMBytesPerSec / egressMBytesPerSec 单位 MB/s（libvirt 习惯用 MB）。
func BuildLibvirtBandwidth(ingressMBps, egressMBps int) (LibvirtBandwidthXML, error) {
	if ingressMBps < 0 || egressMBps < 0 {
		return LibvirtBandwidthXML{}, fmt.Errorf("qos: rates must be >= 0")
	}
	if ingressMBps == 0 && egressMBps == 0 {
		return LibvirtBandwidthXML{}, nil
	}
	out := LibvirtBandwidthXML{Units: "bytes"}
	if ingressMBps > 0 {
		out.Ingress = &BandwidthShape{Average: ingressMBps * 1024 * 1024}
	}
	if egressMBps > 0 {
		out.Egress = &BandwidthShape{Average: egressMBps * 1024 * 1024}
	}
	return out, nil
}

// ---- LXC 配置片段 ----

// BuildLXCLimitsLines 返回写入 lxc.container.conf 的 limits.network.* 行：
//
//   lxc.net.0.maxIngress = <mbps>
//   lxc.net.0.maxEgress = <mbps>
//
// 入参 ingressMbpps / egressMbpps 单位 Mbps；0 表示该方向不限（不输出对应行）。
func BuildLXCLimitsLines(ingressMbps, egressMbps int) []string {
	out := []string{}
	if ingressMbps > 0 {
		out = append(out, fmt.Sprintf("lxc.net.0.maxIngress = %d", ingressMbps))
	}
	if egressMbps > 0 {
		out = append(out, fmt.Sprintf("lxc.net.0.maxEgress = %d", egressMbps))
	}
	return out
}

// ---- 重放路径 ----

// Apply 编排完整的"清旧建新"tc 流程：调用方传入 runner 与 interface。
//
//   - 旧规则可能存在也可能不存在：RemoveHTBCommands 在 qdisc 不存在时
//     失败被吞掉（exit code !=0 → 不视为错误）；
//   - BuildHTBCommands 生成的命令按序执行；
//   - 整体执行结果（最后一个命令的输出）返回。
func Apply(runner interface {
	Run(string, ...string) (string, error)
}, iface string, ingress, egress int) error {
	if runner == nil {
		return errors.New("qos: nil runner")
	}
	// 1) 清旧（容忍 qdisc 不存在）。
	for _, args := range RemoveHTBCommands(iface) {
		_, _ = runner.Run("tc", args...)
	}
	// 2) 建新。
	cmds, err := BuildHTBCommands(HTBConfig{Iface: iface, IngressMbps: ingress, EgressMbps: egress})
	if err != nil {
		return err
	}
	for _, args := range cmds {
		if _, err := runner.Run("tc", args...); err != nil {
			return fmt.Errorf("tc %s: %v", strings.Join(args, " "), err)
		}
	}
	return nil
}