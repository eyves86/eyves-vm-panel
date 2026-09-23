package secgroup

import (
	"strings"
	"testing"
)

func TestRuleValidate(t *testing.T) {
	// OK
	good := Rule{ID: "r1", GroupID: "g1", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept}
	if err := good.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	// protocol=any 不要求 port
	anyProto := Rule{ID: "r2", GroupID: "g1", Direction: DirIngress, Protocol: ProtoAny, Action: ActionAccept}
	if err := anyProto.Validate(); err != nil {
		t.Fatalf("any protocol must pass, got %v", err)
	}
	// protocol=tcp 但 dst_port=0
	tcpNoPort := Rule{ID: "r3", GroupID: "g1", Direction: DirIngress, Protocol: ProtoTCP, Action: ActionAccept}
	if err := tcpNoPort.Validate(); err == nil {
		t.Fatal("tcp without port must error")
	}
	// 非 CIDR
	badMask := Rule{ID: "r4", GroupID: "g1", Direction: DirIngress, Protocol: ProtoAny, SrcMask: "1.2.3.4", Action: ActionAccept}
	if err := badMask.Validate(); err == nil {
		t.Fatal("non-CIDR src_mask must error")
	}
	// 无效 direction
	badDir := Rule{ID: "r5", GroupID: "g1", Direction: "sideways", Protocol: ProtoAny, Action: ActionAccept}
	if err := badDir.Validate(); err == nil {
		t.Fatal("invalid direction must error")
	}
}

func TestGroupValidate(t *testing.T) {
	g := Group{ID: "g1", TenantID: "t1", Name: "web", DefaultAction: ActionAccept}
	if err := g.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	noTenant := Group{ID: "g2", Name: "x", DefaultAction: ActionAccept}
	if err := noTenant.Validate(); err == nil {
		t.Fatal("missing tenant must error")
	}
	badDefault := Group{ID: "g3", TenantID: "t1", Name: "x", DefaultAction: "shrug"}
	if err := badDefault.Validate(); err == nil {
		t.Fatal("invalid default_action must error")
	}
}

func TestSanitizeStripsUnsafe(t *testing.T) {
	cases := map[string]string{
		"abc-def_123":   "abc-def_123",
		"ABC":            "abc",
		"ten.ant;rm -rf": "ten_ant_rm_-rf",
		"":               "default",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Fatalf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompileMinimal(t *testing.T) {
	groups := []Group{{
		ID: "g1", TenantID: "t1", Name: "web", DefaultAction: ActionAccept,
	}}
	rules := []Rule{{
		ID: "r1", GroupID: "g1", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept,
	}}
	res, err := Compile("t1", groups, rules)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hash == "" {
		t.Fatal("hash must be non-empty")
	}
	// 必需关键字
	for _, kw := range []string{"table inet eyves-t1", "ct state established,related accept", "tcp dport 80 accept"} {
		if !strings.Contains(res.NftText, kw) {
			t.Fatalf("NftText missing %q: %s", kw, res.NftText)
		}
	}
}

func TestCompileRequiresTenant(t *testing.T) {
	if _, err := Compile("", nil, nil); err == nil {
		t.Fatal("empty tenant must error")
	}
}

func TestCompileTenantMismatch(t *testing.T) {
	groups := []Group{{ID: "g1", TenantID: "other", Name: "x", DefaultAction: ActionAccept}}
	if _, err := Compile("t1", groups, nil); err == nil {
		t.Fatal("tenant mismatch must error")
	}
}

func TestCompileSortedByPriority(t *testing.T) {
	rules := []Rule{
		{ID: "r-high", GroupID: "g", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 22, Action: ActionAccept, Priority: 10},
		{ID: "r-low", GroupID: "g", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept, Priority: 100},
		{ID: "r-mid", GroupID: "g", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 443, Action: ActionAccept, Priority: 50},
	}
	res, err := Compile("t", []Group{{ID: "g", TenantID: "t", Name: "x", DefaultAction: ActionAccept}}, rules)
	if err != nil {
		t.Fatal(err)
	}
	// 找到每个规则在编译文本中的出现位置。
	positions := []int{}
	for _, rid := range []string{"r-high", "r-mid", "r-low"} {
		i := strings.Index(res.NftText, "rule "+rid)
		if i < 0 {
			t.Fatalf("rule %s not in NftText", rid)
		}
		positions = append(positions, i)
	}
	if !(positions[0] < positions[1] && positions[1] < positions[2]) {
		t.Fatalf("priority order wrong: %v", positions)
	}
}

func TestCompileDeterministic(t *testing.T) {
	groups := []Group{
		{ID: "g-a", TenantID: "t", Name: "a", DefaultAction: ActionAccept},
		{ID: "g-b", TenantID: "t", Name: "b", DefaultAction: ActionDrop},
	}
	rules := []Rule{
		{ID: "r1", GroupID: "g-a", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept},
		{ID: "r2", GroupID: "g-b", Direction: DirEgress, Protocol: ProtoAny, Action: ActionDrop},
	}
	a, _ := Compile("t", groups, rules)
	b, _ := Compile("t", groups, rules)
	if a.Hash != b.Hash {
		t.Fatalf("hash not stable across compiles: %s vs %s", a.Hash, b.Hash)
	}
}

func TestCompileHashDiffersForDiffGroups(t *testing.T) {
	a, _ := Compile("t",
		[]Group{{ID: "g", TenantID: "t", Name: "x", DefaultAction: ActionAccept}},
		[]Rule{{ID: "r1", GroupID: "g", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept}})
	b, _ := Compile("t",
		[]Group{{ID: "g", TenantID: "t", Name: "x", DefaultAction: ActionDrop}}, // default 不同
		[]Rule{{ID: "r1", GroupID: "g", Direction: DirIngress, Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept}})
	if a.Hash == b.Hash {
		t.Fatal("hash must differ when default_action differs")
	}
}

func TestCompileRejectsBadRule(t *testing.T) {
	r := Rule{ID: "r", GroupID: "g", Direction: "wrong", Protocol: ProtoAny, Action: ActionAccept}
	if _, err := Compile("t", []Group{{ID: "g", TenantID: "t", Name: "x", DefaultAction: ActionAccept}}, []Rule{r}); err == nil {
		t.Fatal("bad rule must error")
	}
}

func TestRenderRuleOutputs(t *testing.T) {
	cases := []struct {
		rule    Rule
		wantSub []string
	}{
		{Rule{Protocol: ProtoTCP, DstPort: 80, Action: ActionAccept}, []string{"tcp", "dport 80", "accept"}},
		{Rule{Protocol: ProtoUDP, DstPort: 53, Action: ActionDrop}, []string{"udp", "dport 53", "drop"}},
		{Rule{Protocol: ProtoAny, Action: ActionReject}, []string{"reject"}},
		{Rule{Protocol: ProtoAny, SrcMask: "10.0.0.0/8", Action: ActionAccept}, []string{"ip saddr 10.0.0.0/8"}},
	}
	for _, c := range cases {
		got, err := renderRule(c.rule)
		if err != nil {
			t.Fatalf("renderRule: %v", err)
		}
		for _, sub := range c.wantSub {
			if !strings.Contains(got, sub) {
				t.Fatalf("renderRule = %q, missing %q", got, sub)
			}
		}
	}
}

func TestHashCompareEqualAndNotEqual(t *testing.T) {
	a := CompileResult{Hash: "abc"}
	b := CompileResult{Hash: "abc"}
	c := CompileResult{Hash: "def"}
	if !HashCompare(a, b) {
		t.Fatal("same hash must compare equal")
	}
	if HashCompare(a, c) {
		t.Fatal("different hash must compare not equal")
	}
	if HashCompare(a, CompileResult{}) {
		t.Fatal("empty hash must compare not equal")
	}
}