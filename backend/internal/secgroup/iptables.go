package secgroup

// iptables.go —— 安全组的真实执行层（补齐 P2-3 缺失的下发环节）。
//
// 背景：本包原本只有 Compile()，能把规则编译成 nftables 文本，但编译产物
// （CompileResult.NftText）在**全库没有任何消费方**——即界面上配好的安全组
// 规则从未下发到任何地方，容器实际上是裸奔的。`Container.SecGroupIDs` 数据
// 模型是完整的，缺的只是执行。
//
// 为什么用 iptables 而不是 nft：
//   - 目标宿主机（Debian/Ubuntu）普遍预装 iptables 且内核模块齐备；
//     生产实测主控机上**没有 nft 命令**，nftables 方案会静默失效。
//   - 本实现只操作**独立链**（EYVES-SG-IN/OUT-<tenant>），不修改、不清空
//     任何既有规则；卸载时按链名精确删除。
//
// 安全约束：
//   - 命令逐参数传递，绝不拼接 shell 字符串（与包内其余部分一致）；
//   - 所有标识符经 sanitize，链名只含 [a-z0-9_-]；
//   - 应用失败向上返回错误，由调用方决定是否阻塞（默认不阻塞容器启动）。

import (
	"fmt"
	"sort"
	"strings"
)

// Runner 执行外部命令。与 storage.CommandRunner 同形，避免包间依赖。
type Runner interface {
	// Run 执行命令并返回合并输出；参数必须逐个传递，禁止 shell 拼接。
	Run(name string, args ...string) (string, error)
}

// ChainName 返回某租户在指定方向上的链名。
func chainName(tenantID string, ingress bool) string {
	dir := "OUT"
	if ingress {
		dir = "IN"
	}
	name := "EYVES-SG-" + dir + "-" + sanitize(tenantID)
	// iptables 链名上限 28 字符；超长时截断（sanitize 后仍可能超）。
	if len(name) > 28 {
		name = name[:28]
	}
	return name
}

// ChainNames 返回该编译结果涉及的两条链名。
func ChainNames(res CompileResult) (ingress, egress string) {
	return chainName(res.TenantID, true), chainName(res.TenantID, false)
}

// iptablesRuleArgs 把一条 secgroup.Rule 渲染成 iptables 参数（不含 -A 链名）。
func iptablesRuleArgs(r Rule) ([]string, error) {
	args := []string{}
	switch r.Protocol {
	case ProtoTCP:
		args = append(args, "-p", "tcp")
	case ProtoUDP:
		args = append(args, "-p", "udp")
	case ProtoICMP:
		args = append(args, "-p", "icmp")
	case ProtoAny:
		// 不指定协议匹配器
	default:
		return nil, fmt.Errorf("secgroup: 未知协议 %q", r.Protocol)
	}
	if strings.TrimSpace(r.SrcMask) != "" {
		args = append(args, "-s", strings.TrimSpace(r.SrcMask))
	}
	if strings.TrimSpace(r.DstMask) != "" {
		args = append(args, "-d", strings.TrimSpace(r.DstMask))
	}
	// --dport/--sport 只在指定了 tcp/udp 时才合法。
	if r.Protocol == ProtoTCP || r.Protocol == ProtoUDP {
		if r.DstPort > 0 {
			args = append(args, "--dport", fmt.Sprintf("%d", r.DstPort))
		}
		if r.SrcPort > 0 {
			args = append(args, "--sport", fmt.Sprintf("%d", r.SrcPort))
		}
	}
	switch r.Action {
	case ActionDrop:
		args = append(args, "-j", "DROP")
	case ActionReject:
		args = append(args, "-j", "REJECT")
	case ActionAccept, "":
		args = append(args, "-j", "ACCEPT")
	default:
		return nil, fmt.Errorf("secgroup: 未知动作 %q", r.Action)
	}
	return args, nil
}

// BuildIptablesCommands 生成把安全组规则同步到独立链所需的完整命令序列。
//
// containerIPs 是绑定该租户安全组的容器 IP 列表——规则按容器地址生效，
// 这是把"租户级规则集"落到"具体容器"的关键一步（原实现缺这一环）。
//
// 生成的序列是**幂等式**的：先删旧链、再建新链、最后挂到 FORWARD。
// 但为避免误删其它流量，本函数**不返回任何 flush/delete 整表的命令**。
func BuildIptablesCommands(res CompileResult, containerIPs []string) ([][]string, error) {
	if strings.TrimSpace(res.TenantID) == "" {
		return nil, fmt.Errorf("secgroup: 缺少租户 ID")
	}
	if len(containerIPs) == 0 {
		// 没有任何容器绑定：只清理链条，不建立新规则。
		return RemoveIptablesCommands(res.TenantID), nil
	}

	inChain, outChain := ChainNames(res)
	var cmds [][]string

	// 1) 确保链存在（重复创建会被忽略，见 Apply 的错误容忍）。
	cmds = append(cmds, []string{"iptables", "-N", inChain})
	cmds = append(cmds, []string{"iptables", "-N", outChain})

	// 2) 清空旧规则（只清自己的链）。
	cmds = append(cmds, []string{"iptables", "-F", inChain})
	cmds = append(cmds, []string{"iptables", "-F", outChain})

	// 3) 有状态放行：已建立/相关的连接不重复检查（与 nft 版本语义一致）。
	cmds = append(cmds, []string{"iptables", "-A", inChain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"})
	cmds = append(cmds, []string{"iptables", "-A", outChain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"})

	// 4) 按优先级升序写入规则（Priority 小的先匹配）。
	rules := append([]Rule(nil), res.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	for _, r := range rules {
		args, err := iptablesRuleArgs(r)
		if err != nil {
			return nil, err
		}
		chain := outChain
		if r.Direction == DirIngress {
			chain = inChain
		}
		cmds = append(cmds, append([]string{"iptables", "-A", chain}, args...))
	}

	// 5) 默认策略：未命中规则时的处置（取该租户 Groups 的默认动作，
	//    只要有一个组声明 drop 就按 drop 处理——与 Compile 的语义一致）。
	defaultAction := ActionAccept
	for _, g := range res.Groups {
		if g.DefaultAction == ActionDrop {
			defaultAction = ActionDrop
			break
		}
	}
	verb := "ACCEPT"
	if defaultAction == ActionDrop {
		verb = "DROP"
	}
	cmds = append(cmds, []string{"iptables", "-A", inChain, "-j", verb})
	cmds = append(cmds, []string{"iptables", "-A", outChain, "-j", verb})

	// 6) 把链挂到 FORWARD：仅对绑定容器地址的流量生效，不影响其它转发。
	for _, ip := range containerIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		// 避免重复挂载（-C 检查失败才 -I）。
		cmds = append(cmds, []string{"iptables", "-C", "FORWARD", "-d", ip, "-j", inChain})
		cmds = append(cmds, []string{"iptables", "-I", "FORWARD", "1", "-d", ip, "-j", inChain})
		cmds = append(cmds, []string{"iptables", "-C", "FORWARD", "-s", ip, "-j", outChain})
		cmds = append(cmds, []string{"iptables", "-I", "FORWARD", "1", "-s", ip, "-j", outChain})
	}
	return cmds, nil
}

// RemoveIptablesCommands 生成卸载某租户安全组所需的命令序列。
func RemoveIptablesCommands(tenantID string) [][]string {
	inChain, outChain := ChainNames(CompileResult{TenantID: tenantID})
	var cmds [][]string
	for _, chain := range []string{inChain, outChain} {
		// 从 FORWARD 上摘除所有对本链的跳转（可能有多条）。
		cmds = append(cmds, []string{"iptables", "-D", "FORWARD", "-j", chain})
		cmds = append(cmds, []string{"iptables", "-F", chain})
		cmds = append(cmds, []string{"iptables", "-X", chain})
	}
	return cmds
}

// Apply 依次执行命令序列。
//
// 容错策略：链已存在 / 规则不存在 这类"目标状态已达成"的错误被忽略
// （iptables 对这些情况返回非零但状态是对的），其余错误向上返回。
func Apply(runner Runner, cmds [][]string) error {
	if runner == nil {
		return fmt.Errorf("secgroup: runner 为空")
	}
	for _, c := range cmds {
		if len(c) == 0 {
			continue
		}
		out, err := runner.Run(c[0], c[1:]...)
		if err != nil && !isBenignIptablesError(out) {
			return fmt.Errorf("secgroup: %s 失败: %v (%s)", strings.Join(c, " "), err, strings.TrimSpace(out))
		}
	}
	return nil
}

// isBenignIptablesError 判断是否是"目标状态已达成"导致的非零退出。
func isBenignIptablesError(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"already exists",     // 链已存在
		"does not exist",     // 待删的链/规则不存在
		"no chain/target",    // 同上
		"bad rule (does a matching rule exist in that chain?)",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
