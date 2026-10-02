package secgroup

// iptables_test.go —— 安全组执行层（下发）回归测试。
//
// 背景：本包原本只有 Compile()，编译产物没有任何消费方——界面上配好的安全组
// 从未真正生效。这些测试锁定"能生成正确的下发命令、且只动自己的链"。

import (
	"fmt"
	"strings"
	"testing"
)

// mockRunner 记录执行过的命令；可按命令片段注入错误。
type mockRunner struct {
	cmds    [][]string
	failOn  map[string]string // 命令包含该子串时，返回此错误信息
	runCall int
}

func (m *mockRunner) Run(name string, args ...string) (string, error) {
	full := append([]string{name}, args...)
	m.cmds = append(m.cmds, full)
	m.runCall++
	joined := strings.Join(full, " ")
	for marker, msg := range m.failOn {
		if strings.Contains(joined, marker) {
			return msg, fmt.Errorf("exit status 1")
		}
	}
	return "", nil
}

func (m *mockRunner) joined() string {
	parts := make([]string, 0, len(m.cmds))
	for _, c := range m.cmds {
		parts = append(parts, strings.Join(c, " "))
	}
	return strings.Join(parts, "\n")
}

func testCompileResult(t *testing.T) CompileResult {
	t.Helper()
	groups := []Group{{ID: "g1", TenantID: "acme", Name: "web", DefaultAction: ActionDrop}}
	rules := []Rule{
		{ID: "r1", GroupID: "g1", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 22, Action: ActionAccept, Priority: 10},
		{ID: "r2", GroupID: "g1", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 443, Action: ActionAccept, Priority: 20},
		// egress 规则带源网段，便于与"默认策略 -j ACCEPT"区分开。
		{ID: "r3", GroupID: "g1", Direction: DirEgress, Protocol: ProtoAny, SrcMask: "10.0.3.0/24", Action: ActionAccept, Priority: 5},
	}
	res, err := Compile("acme", groups, rules)
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	return res
}

// TestBuildIptablesCommandsCoversRulesAndBinding 命令序列必须包含
// 建链、清链、每条规则、默认策略，以及把链挂到容器地址上的跳转。
func TestBuildIptablesCommandsCoversRulesAndBinding(t *testing.T) {
	res := testCompileResult(t)
	cmds, err := BuildIptablesCommands(res, []string{"10.0.3.2"})
	if err != nil {
		t.Fatalf("生成命令失败: %v", err)
	}

	var sb strings.Builder
	for _, c := range cmds {
		sb.WriteString(strings.Join(c, " "))
		sb.WriteString("\n")
	}
	all := sb.String()

	// 链名来自租户 ID 且被 sanitize。
	inChain, outChain := ChainNames(res)
	if !strings.Contains(all, "-N "+inChain) || !strings.Contains(all, "-N "+outChain) {
		t.Fatalf("应创建两侧独立链，实际:\n%s", all)
	}
	// 有状态放行。
	if !strings.Contains(all, "conntrack --ctstate ESTABLISHED,RELATED") {
		t.Fatalf("应包含有状态放行规则，实际:\n%s", all)
	}
	// 三条业务规则。
	for _, port := range []string{"--dport 22", "--dport 443"} {
		if !strings.Contains(all, port) {
			t.Fatalf("缺少规则 %s，实际:\n%s", port, all)
		}
	}
	// 默认策略取组里的 drop。
	if !strings.Contains(all, "-A "+inChain+" -j DROP") {
		t.Fatalf("ingress 默认策略应为 DROP，实际:\n%s", all)
	}
	// 挂到容器地址（这是"租户规则落到具体容器"的关键一步）。
	if !strings.Contains(all, "-I FORWARD 1 -d 10.0.3.2 -j "+inChain) {
		t.Fatalf("应把 ingress 链挂到容器地址，实际:\n%s", all)
	}
	if !strings.Contains(all, "-I FORWARD 1 -s 10.0.3.2 -j "+outChain) {
		t.Fatalf("应把 egress 链挂到容器地址，实际:\n%s", all)
	}
}

// TestBuildIptablesCommandsRespectsDirection 每条规则必须落到与其方向对应的链。
func TestBuildIptablesCommandsRespectsDirection(t *testing.T) {
	res := testCompileResult(t)
	cmds, err := BuildIptablesCommands(res, []string{"10.0.3.5"})
	if err != nil {
		t.Fatalf("生成命令失败: %v", err)
	}
	inChain, outChain := ChainNames(res)

	for _, r := range res.Rules {
		wantArgs, err := iptablesRuleArgs(r)
		if err != nil {
			t.Fatalf("渲染规则 %s 失败: %v", r.ID, err)
		}
		wantChain, wrongChain := outChain, inChain
		if r.Direction == DirIngress {
			wantChain, wrongChain = inChain, outChain
		}
		want := strings.Join(wantArgs, " ")

		foundRight, foundWrong := false, false
		for _, c := range cmds {
			if len(c) < 4 || c[0] != "iptables" || c[1] != "-A" {
				continue
			}
			if strings.Join(c[3:], " ") != want {
				continue
			}
			if c[2] == wantChain {
				foundRight = true
			}
			if c[2] == wrongChain {
				foundWrong = true
			}
		}
		if !foundRight {
			t.Fatalf("规则 %s(%s) 应写入 %s", r.ID, r.Direction, wantChain)
		}
		if foundWrong {
			t.Fatalf("规则 %s(%s) 不应写入 %s", r.ID, r.Direction, wrongChain)
		}
	}
}

// TestBuildIptablesCommandsNoBindingCleansUp 没有容器绑定该安全组时，
// 只应清理链条，不得留下放行/丢弃规则。
func TestBuildIptablesCommandsNoBindingCleansUp(t *testing.T) {
	res := testCompileResult(t)
	cmds, err := BuildIptablesCommands(res, nil)
	if err != nil {
		t.Fatalf("生成命令失败: %v", err)
	}
	joined := ""
	for _, c := range cmds {
		joined += strings.Join(c, " ") + "\n"
	}
	if strings.Contains(joined, "-A ") {
		t.Fatalf("无绑定不应写入任何规则，实际:\n%s", joined)
	}
	if !strings.Contains(joined, "-X ") {
		t.Fatalf("无绑定应删除链条，实际:\n%s", joined)
	}
}

// TestChainNameSanitizedAndBounded 链名必须净化且有长度上限（iptables 限制 28）。
func TestChainNameSanitizedAndBounded(t *testing.T) {
	weird := "acme/../../etc;rm -rf"
	name := chainName(weird, true)
	if strings.ContainsAny(name, "/.; ") {
		t.Fatalf("链名含非法字符: %q", name)
	}
	if len(name) > 28 {
		t.Fatalf("链名超长（%d）: %q", len(name), name)
	}

	long := strings.Repeat("a", 100)
	if got := chainName(long, false); len(got) > 28 {
		t.Fatalf("超长租户 ID 的链名应被截断，实际 %d 字符", len(got))
	}
}

// TestIptablesRuleArgsRejectsPortWithoutProtocol dport/sport 只在 tcp/udp 下合法，
// 否则 iptables 会直接报错——宁可在生成阶段就拒掉。
func TestIptablesRuleArgsRejectsPortWithoutProtocol(t *testing.T) {
	// 带端口的 tcp 规则：正常。
	args, err := iptablesRuleArgs(Rule{Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept})
	if err != nil {
		t.Fatalf("tcp 规则不该失败: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "--dport 80") {
		t.Fatalf("应包含 dport，实际 %v", args)
	}
	// any + 端口：端口被忽略而不是产生非法命令。
	args, err = iptablesRuleArgs(Rule{Protocol: ProtoAny, DstPort: 80, Action: ActionDrop})
	if err != nil {
		t.Fatalf("any 规则不该失败: %v", err)
	}
	if strings.Contains(strings.Join(args, " "), "--dport") {
		t.Fatalf("any 协议下不应出现 --dport，实际 %v", args)
	}
	if strings.Join(args, " ") != "-j DROP" {
		t.Fatalf("any + drop 应只渲染为 -j DROP，实际 %v", args)
	}
	// 未知协议必须报错，不能静默产生一条匹配所有流量的规则。
	if _, err := iptablesRuleArgs(Rule{Protocol: Protocol("sctp"), Action: ActionAccept}); err == nil {
		t.Fatal("未知协议必须报错")
	}
}

// TestApplyToleratesBenignErrors 链已存在 / 规则不存在 属"目标状态已达成"，
// 不该让整次下发失败。
func TestApplyToleratesBenignErrors(t *testing.T) {
	m := &mockRunner{failOn: map[string]string{"-N ": "iptables: Chain already exists."}}
	cmds := [][]string{
		{"iptables", "-N", "EYVES-SG-IN-acme"},
		{"iptables", "-F", "EYVES-SG-IN-acme"},
	}
	if err := Apply(m, cmds); err != nil {
		t.Fatalf("链已存在不应视为失败: %v", err)
	}
	if m.runCall != 2 {
		t.Fatalf("应继续执行后续命令，实际只跑了 %d 条", m.runCall)
	}
}

// TestApplyPropagatesRealErrors 真正的失败必须向上返回。
func TestApplyPropagatesRealErrors(t *testing.T) {
	m := &mockRunner{failOn: map[string]string{"-F ": "iptables: Permission denied (you must be root)"}}
	cmds := [][]string{{"iptables", "-F", "EYVES-SG-IN-acme"}}
	if err := Apply(m, cmds); err == nil {
		t.Fatal("权限错误必须向上返回")
	}
}

// TestRemoveIptablesCommandsUnhooksForward 卸载必须先摘 FORWARD 跳转再删链。
func TestRemoveIptablesCommandsUnhooksForward(t *testing.T) {
	cmds := RemoveIptablesCommands("acme")
	inChain, outChain := ChainNames(CompileResult{TenantID: "acme"})
	joined := ""
	for _, c := range cmds {
		joined += strings.Join(c, " ") + "\n"
	}
	for _, chain := range []string{inChain, outChain} {
		if !strings.Contains(joined, "-D FORWARD -j "+chain) {
			t.Fatalf("应从 FORWARD 摘除 %s，实际:\n%s", chain, joined)
		}
		if !strings.Contains(joined, "-X "+chain) {
			t.Fatalf("应删除链 %s，实际:\n%s", chain, joined)
		}
	}
}
