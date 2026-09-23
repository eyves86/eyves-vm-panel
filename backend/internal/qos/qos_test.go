package qos

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestBuildHTBCommandsIngressEgressBoth(t *testing.T) {
	cmds, err := BuildHTBCommands(HTBConfig{Iface: "vethABC", IngressMbps: 100, EgressMbps: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) < 5 {
		t.Fatalf("expected >=5 tc commands, got %d: %v", len(cmds), cmds)
	}
	// qdisc root 一定是第一条
	if cmds[0][0] != "qdisc" || !contains(cmds[0], "root") {
		t.Fatalf("first cmd must be root qdisc, got %v", cmds[0])
	}
	// parent class rate=100mbit（max(ingress, egress)）
	if !contains(cmds[1], "100mbit") {
		t.Fatalf("parent class rate must be 100mbit, got %v", cmds[1])
	}
	// 子类 rate=50mbit
	if !contains(cmds[2], "50mbit") {
		t.Fatalf("egress child class rate must be 50mbit, got %v", cmds[2])
	}
	// ingress 方向存在
	hasIngress := false
	for _, c := range cmds {
		if contains(c, "ingress") {
			hasIngress = true
		}
	}
	if !hasIngress {
		t.Fatal("ingress qdisc missing")
	}
}

func TestBuildHTBCommandsNoLimit(t *testing.T) {
	cmds, err := BuildHTBCommands(HTBConfig{Iface: "vethABC"})
	if err != nil {
		t.Fatal(err)
	}
	// 仅根 qdisc，无 class。
	if len(cmds) != 1 {
		t.Fatalf("no-limit expected 1 cmd, got %d", len(cmds))
	}
}

func TestBuildHTBCommandsEgressOnly(t *testing.T) {
	cmds, err := BuildHTBCommands(HTBConfig{Iface: "vethABC", EgressMbps: 80})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAny(cmds, "100mbit") && !containsAny(cmds, "80mbit") {
		t.Fatal("expected 80mbit parent class")
	}
}

func TestBuildHTBCommandsRequiresIface(t *testing.T) {
	if _, err := BuildHTBCommands(HTBConfig{IngressMbps: 10}); err == nil {
		t.Fatal("empty iface must error")
	}
}

func TestBuildHTBCommandsRejectsNegative(t *testing.T) {
	if _, err := BuildHTBCommands(HTBConfig{Iface: "x", IngressMbps: -1}); err == nil {
		t.Fatal("negative ingress must error")
	}
}

func TestRemoveHTBCommands(t *testing.T) {
	cmds := RemoveHTBCommands("vethABC")
	if len(cmds) != 2 {
		t.Fatalf("expected 2 remove commands, got %d", len(cmds))
	}
	if cmds[0][0] != "qdisc" || cmds[0][1] != "del" {
		t.Fatalf("remove cmd = %v", cmds[0])
	}
}

func TestBuildLibvirtBandwidthBoth(t *testing.T) {
	bw, err := BuildLibvirtBandwidth(100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if bw.Ingress == nil || bw.Egress == nil {
		t.Fatalf("expected both ingress+egress, got %+v", bw)
	}
	if bw.Units != "bytes" {
		t.Fatalf("units = %q, want bytes", bw.Units)
	}
	if bw.Ingress.Average != 100*1024*1024 {
		t.Fatalf("ingress average = %d, want %d", bw.Ingress.Average, 100*1024*1024)
	}
	if bw.Egress.Average != 50*1024*1024 {
		t.Fatalf("egress average = %d", bw.Egress.Average)
	}
	// XML 序列化必须含 <inbound>/<outbound> 标签。
	data, err := xml.Marshal(bw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "<inbound") || !strings.Contains(s, "<outbound") {
		t.Fatalf("XML missing inbound/outbound: %s", s)
	}
}

func TestBuildLibvirtBandwidthZeroOmits(t *testing.T) {
	bw, err := BuildLibvirtBandwidth(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bw.Ingress != nil || bw.Egress != nil {
		t.Fatalf("zero rates must omit, got %+v", bw)
	}
}

func TestBuildLibvirtBandwidthNegative(t *testing.T) {
	if _, err := BuildLibvirtBandwidth(-1, 10); err == nil {
		t.Fatal("negative ingress must error")
	}
}

func TestBuildLXCLimitsLines(t *testing.T) {
	lines := BuildLXCLimitsLines(100, 50)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %v", lines)
	}
	if !strings.Contains(lines[0], "maxIngress = 100") {
		t.Fatalf("ingress line wrong: %v", lines)
	}
	if !strings.Contains(lines[1], "maxEgress = 50") {
		t.Fatalf("egress line wrong: %v", lines)
	}
}

func TestBuildLXCLimitsLinesZeroOmits(t *testing.T) {
	if lines := BuildLXCLimitsLines(0, 100); len(lines) != 1 {
		t.Fatalf("ingress=0 must omit, got %v", lines)
	}
	if lines := BuildLXCLimitsLines(0, 0); len(lines) != 0 {
		t.Fatalf("all-zero must omit, got %v", lines)
	}
}

// TestApplyClearAndBuild 验证 Apply 顺序：先 RemoveHTBCommands 再 BuildHTBCommands。
func TestApplyClearAndBuild(t *testing.T) {
	r := &cmdRecorder{}
	if err := Apply(r, "vethT", 100, 50); err != nil {
		t.Fatal(err)
	}
	if !r.hasName("tc") {
		t.Fatal("expected tc commands")
	}
	// 第一条必须是 del（清旧）。
	if r.calls[0][1] != "del" && r.calls[0][2] != "del" {
		t.Fatalf("first call must be del, got %v", r.calls[0])
	}
	// 至少有一条 qdisc root（建新）。
	found := false
	for _, c := range r.calls {
		if contains(c, "root") && contains(c, "add") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no root qdisc add after clear, calls = %v", r.calls)
	}
}

func TestApplyNilRunnerError(t *testing.T) {
	if err := Apply(nil, "v", 100, 100); err == nil {
		t.Fatal("nil runner must error")
	}
}

func TestApplyBuildErrorPropagates(t *testing.T) {
	r := &cmdRecorder{}
	err := Apply(r, "", 100, 100)
	if err == nil {
		t.Fatal("empty iface must error")
	}
}

// ---- helpers ----

func contains(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

func containsAny(cmds [][]string, needle string) bool {
	for _, c := range cmds {
		if contains(c, needle) {
			return true
		}
	}
	return false
}

type cmdRecorder struct {
	calls [][]string
	byName map[string]int
}

func (r *cmdRecorder) Run(name string, args ...string) (string, error) {
	full := append([]string{name}, args...)
	r.calls = append(r.calls, full)
	return "", nil
}

func (r *cmdRecorder) hasName(name string) bool {
	for _, c := range r.calls {
		if len(c) > 0 && c[0] == name {
			return true
		}
	}
	return false
}