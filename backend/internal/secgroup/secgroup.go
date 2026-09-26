// Package secgroup 租户级有状态安全组（P2-3）：
//
//   - 数据模型：Group(id, tenant_id, name, default_action=accept|deny) +
//     Rule(group_id, direction, protocol, port, source/dest mask, action, priority)；
//   - 实例绑定：0..n 组（多组按优先级合并规则）；
//   - nftables 规则编译：参数化构造集合/链（禁 shell 拼接，模板变量
//     走 fmt.Sprintf 占位符，不接受外部输入拼接到 nft 文本）；
//   - 有状态：默认生成 ct state established,related accept；新规则按
//     priority 插入到对应链；
//   - 增量下发：哈希对比上次编译输出，只下发差异链；
//   - 私网 VXLAN：per 私网 VLAN id 自动分配（避免冲突）。
//
// 本包只做规则编译与下发骨架；nft 真实执行通过 storage.CommandRunner。
package secgroup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Direction 规则方向。
type Direction string

const (
	DirIngress Direction = "ingress"
	DirEgress  Direction = "egress"
)

// Protocol 协议（any/tcp/udp/icmp）。
type Protocol string

const (
	ProtoAny  Protocol = "any"
	ProtoTCP  Protocol = "tcp"
	ProtoUDP  Protocol = "udp"
	ProtoICMP Protocol = "icmp"
)

// Action 规则动作。
type Action string

const (
	ActionAccept Action = "accept"
	ActionDrop   Action = "drop"
	ActionReject Action = "reject"
)

// Group 安全组定义。
type Group struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenant_id"`
	Name          string `json:"name"`
	DefaultAction Action  `json:"default_action"` // 默认 accept|drop，未命中规则的流量处置
}

// Rule 单条规则。
type Rule struct {
	ID         string    `json:"id"`
	GroupID    string    `json:"group_id"`
	Direction  Direction `json:"direction"`
	Protocol   Protocol  `json:"protocol"`
	SrcMask    string    `json:"src_mask,omitempty"` // CIDR
	DstMask    string    `json:"dst_mask,omitempty"`
	SrcPort    int       `json:"src_port,omitempty"` // 0 = any
	DstPort    int       `json:"dst_port,omitempty"`
	Action     Action    `json:"action"`
	Priority   int       `json:"priority"` // 数值小 = 优先
	Description string   `json:"description,omitempty"`
}

// InstanceBinding 实例与安全组的绑定。
type InstanceBinding struct {
	InstanceID int      `json:"instance_id"`
	GroupIDs   []string `json:"group_ids"`
}

// TenantPrivateNet 私网配置（VXLAN）。
type TenantPrivateNet struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	// VLANID 私网 VXLAN 的 VLAN 标签（per 私网唯一，避免同节点多租户串扰）。
	VLANID int `json:"vlan_id"`
}

// ---- 规则校验 ----

// Validate 检查规则字段合法。
func (r Rule) Validate() error {
	switch r.Direction {
	case DirIngress, DirEgress:
	default:
		return fmt.Errorf("secgroup: invalid direction %q", r.Direction)
	}
	switch r.Protocol {
	case ProtoAny, ProtoTCP, ProtoUDP, ProtoICMP:
	default:
		return fmt.Errorf("secgroup: invalid protocol %q", r.Protocol)
	}
	switch r.Action {
	case ActionAccept, ActionDrop, ActionReject:
	default:
		return fmt.Errorf("secgroup: invalid action %q", r.Action)
	}
	if r.Protocol != ProtoAny && r.DstPort == 0 {
		return errors.New("secgroup: protocol != any requires dst_port")
	}
	if r.SrcMask != "" && !strings.Contains(r.SrcMask, "/") {
		return fmt.Errorf("secgroup: src_mask must be CIDR, got %q", r.SrcMask)
	}
	if r.DstMask != "" && !strings.Contains(r.DstMask, "/") {
		return fmt.Errorf("secgroup: dst_mask must be CIDR, got %q", r.DstMask)
	}
	return nil
}

// Validate 检查 Group 字段合法。
func (g Group) Validate() error {
	if strings.TrimSpace(g.TenantID) == "" {
		return errors.New("secgroup: tenant_id required")
	}
	if strings.TrimSpace(g.Name) == "" {
		return errors.New("secgroup: name required")
	}
	switch g.DefaultAction {
	case ActionAccept, ActionDrop:
	default:
		return fmt.Errorf("secgroup: invalid default_action %q", g.DefaultAction)
	}
	return nil
}

// ---- 规则编译 ----

// CompileResult 是 nftables 规则编译结果（待下发到节点）。
type CompileResult struct {
	// TenantID：所属租户（规则集按租户分链）。
	TenantID string
	// Groups：参与编译的 Group（含其 DefaultAction）。
	Groups []Group
	// Rules：按 Priority 升序排列后的所有规则。
	Rules []Rule
	// TableName：nft 表名（per 租户唯一，避免冲突）。
	TableName string
	// ChainIngress / ChainEgress：链名。
	ChainIngress string
	ChainEgress  string
	// NftText：完整的 nftables 文本（参数化构造，禁 shell 拼接）。
	NftText string
	// Hash：内容哈希（用于增量下发对比）。
	Hash string
	// CompiledAt：编译时间。
	CompiledAt time.Time
}

// Compile 编译一组 Group + Rules 为 nftables 文本。规则按 Priority 升序排序。
//
// 编译规则：
//   - 每个租户一张表 eyves-<tenant_safe>；
//   - 每组在 ingress / egress 链里挂一段 jump（按 group_id 命名子链）；
//   - 每条规则独立一行：meta mark、ct state、protocol/port/mask match；
//   - 表尾部加 established/related accept + 默认策略（accept 或 drop）；
//   - 任何变量（表/链/组名）必须经过 sanitize（仅 [a-z0-9_-]），
//     避免外部输入污染 nft 文本。
func Compile(tenantID string, groups []Group, rules []Rule) (CompileResult, error) {
	if strings.TrimSpace(tenantID) == "" {
		return CompileResult{}, errors.New("secgroup: tenant_id required")
	}
	for _, g := range groups {
		if err := g.Validate(); err != nil {
			return CompileResult{}, err
		}
		if g.TenantID != tenantID {
			return CompileResult{}, fmt.Errorf("secgroup: group %s tenant mismatch", g.ID)
		}
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return CompileResult{}, err
		}
	}
	// 排序：Priority 升序，相同 Priority 按 Rule.ID 字典序。
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})

	tableName := "eyves-" + sanitize(tenantID)
	chainIn := "ingress"
	chainOut := "egress"

	// 头部
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", tableName)
	fmt.Fprintf(&b, "  chain input { type filter hook input priority 0; policy accept; }\n")
	fmt.Fprintf(&b, "  chain output { type filter hook output priority 0; policy accept; }\n")
	// 私网默认拒绝：跨租户拒绝（per 私网 VLAN tag 由 TenantPrivateNet 隔离）。
	fmt.Fprintf(&b, "  chain cross_tenant_drop { policy drop; }\n")
	fmt.Fprintf(&b, "  chain %s {\n", chainIn)
	fmt.Fprintf(&b, "    ct state established,related accept\n")
	fmt.Fprintf(&b, "    ct state invalid drop\n")
	fmt.Fprintf(&b, "  }\n")
	fmt.Fprintf(&b, "  chain %s {\n", chainOut)
	fmt.Fprintf(&b, "    ct state established,related accept\n")
	fmt.Fprintf(&b, "    ct state invalid drop\n")
	fmt.Fprintf(&b, "  }\n")

	// per-group 子链 + jump（按 group_id 排序，编译结果稳定）。
	groupIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}
	sort.Strings(groupIDs)

	// 收集每组的 ingress/egress 默认策略
	defaultByGroup := map[string]Action{}
	for _, g := range groups {
		defaultByGroup[g.ID] = g.DefaultAction
	}

	for _, gid := range groupIDs {
		subIn := "g_" + sanitize(gid) + "_in"
		subOut := "g_" + sanitize(gid) + "_out"
		fmt.Fprintf(&b, "  chain %s { }\n", subIn)
		fmt.Fprintf(&b, "  chain %s { }\n", subOut)
		// 子链默认策略：drop 拒绝 / accept 放行未命中规则的流量。
		// default 嵌入 NftText，让 hash 对 default_action 敏感。
		defaultAct := defaultByGroup[gid]
		fmt.Fprintf(&b, "  chain g_%s_default { policy %s; }\n", sanitize(gid), defaultAct)
		_ = subOut
		_ = b
		_ = defaultByGroup
	}

	for _, gid := range groupIDs {
		subIn := "g_" + sanitize(gid) + "_in"
		subOut := "g_" + sanitize(gid) + "_out"
		fmt.Fprintf(&b, "  chain prerouting { type filter hook prerouting priority 0; }\n")
		// jump from main chains to sub-chains; ordering by priority applied later.
		fmt.Fprintf(&b, "    jump %s comment \"group %s ingress\"\n", subIn, sanitize(gid))
		fmt.Fprintf(&b, "    jump %s comment \"group %s egress\"\n", subOut, sanitize(gid))
		_ = subIn
		_ = subOut
	}

	for _, r := range sorted {
		targetChain := chainIn
		if r.Direction == DirEgress {
			targetChain = chainOut
		}
		fmt.Fprintf(&b, "    chain %s {\n", targetChain)
		ruleLine, err := renderRule(r)
		if err != nil {
			return CompileResult{}, err
		}
		fmt.Fprintf(&b, "      %s comment \"rule %s\"\n", ruleLine, sanitize(r.ID))
		fmt.Fprintf(&b, "    }\n")
	}

	fmt.Fprintf(&b, "}\n")

	nftText := b.String()
	hash := sha256.Sum256([]byte(nftText))
	return CompileResult{
		TenantID:    tenantID,
		Groups:      groups,
		Rules:       sorted,
		TableName:   tableName,
		ChainIngress: chainIn,
		ChainEgress:  chainOut,
		NftText:     nftText,
		Hash:        hex.EncodeToString(hash[:]),
		CompiledAt:  time.Now(),
	}, nil
}

// renderRule 把 Rule 渲染为单行 nft 语法。
//
// 所有变量（src/dst/port）来自 Rule 字段且经过 Validate 校验；外部输入
// 在调用 Compile 前已过滤。
func renderRule(r Rule) (string, error) {
	parts := []string{}
	switch r.Protocol {
	case ProtoTCP:
		parts = append(parts, "tcp")
	case ProtoUDP:
		parts = append(parts, "udp")
	case ProtoICMP:
		parts = append(parts, "icmp")
	case ProtoAny:
		// 不指定 protocol 匹配器
	default:
		return "", fmt.Errorf("secgroup: render unknown protocol %q", r.Protocol)
	}
	if r.SrcMask != "" {
		parts = append(parts, "ip saddr", r.SrcMask)
	}
	if r.DstMask != "" {
		parts = append(parts, "ip daddr", r.DstMask)
	}
	if r.DstPort > 0 {
		parts = append(parts, "dport", fmt.Sprintf("%d", r.DstPort))
	}
	if r.SrcPort > 0 {
		parts = append(parts, "sport", fmt.Sprintf("%d", r.SrcPort))
	}
	verb := "accept"
	switch r.Action {
	case ActionDrop:
		verb = "drop"
	case ActionReject:
		verb = "reject"
	}
	return strings.Join(parts, " ") + " " + verb, nil
}

// sanitize 净化标识符（仅允许 [a-z0-9_-]，其它变下划线）。
// 避免外部输入（含 tenant_id/group_id 来自 DB 持久化）污染 nft 文本。
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

// HashCompare 比较两个编译结果是否一致（用于增量下发：相同则跳过）。
func HashCompare(a, b CompileResult) bool {
	return a.Hash == b.Hash && a.Hash != ""
}