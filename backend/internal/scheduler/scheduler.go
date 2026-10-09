// Package scheduler 多节点放置调度器（P1-2）。
//
// 流水线：过滤器（filter）→ 评分器（score）→ 决策（decide）→ 留痕（record）。
// 所有节点来自 config.AppConfig.Nodes；容量数据沿用 Node 现有字段
// （RAMTotalMB/RAMUsedMB/DiskTotalGB/DiskUsedGB/ContainerCount）。
//
// 单实例请求入口：
//
//   req := scheduler.Request{ RAMMB: 512, DiskGB: 10, StorageBackend: "dir", ImageID: "..." }
//   chosen, diagnostics, err := scheduler.Place(req, scheduler.DefaultPolicy())
//
// 直发路径：调用方传 NodeID 时跳过调度（管理员直发），本包不参与。
package scheduler

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/node"
)

// Request 是单次调度请求的输入。
type Request struct {
	// RAMMB / DiskGB / ContainerCountDDelta：要预留的容量增量。
	RAMMB            int64
	DiskGB           float64
	ContainerCountDelta int
	// StorageBackend：所需存储后端（dir/zfs/lvm/rbd/cephfs/nfs）。dir 视为任意节点支持。
	StorageBackend string
	// VirtType：所需虚拟化类型 "lxc" 或 "kvm"；空时默认任意。
	VirtType string
	// ImageID：模板 ID（保留字段，本 P1-2 暂不参与过滤；P5-1 后按镜像能力扩展）。
	ImageID string
	// RequestID：调用方生成的去重 ID；空时由 Decide 填 UUID 替代占位。
	RequestID string
	// TenantID：可选亲和（同租户分散度评分时使用）。
	TenantID string
	// RegionID / NodeGroupID / ClusterID：将调度范围收敛到指定地域 / 分组 / 集群。
	// 空 = 不限。三者可叠加使用（如某集群内的某分组 + 指定 Region 做子域收敛）。
	RegionID    string
	NodeGroupID string
	ClusterID   string
}

// Diagnostics 是调度失败或选择的诊断信息（top-N 候选及理由）。
type Diagnostics struct {
	// RequestID 与 Request.RequestID 对应。
	RequestID string
	// Chosen：选中的节点 ID（成功时非空）。
	Chosen string
	// Reason：成功/失败的简短原因（"score:highest", "all nodes offline" 等）。
	Reason string
	// Candidates：top-N 候选（按 Score 降序），含每个候选的过滤与评分原因。
	Candidates []CandidateScore
}

// CandidateScore 是一个候选节点的过滤+评分结果。
type CandidateScore struct {
	NodeID        string
	PassedFilter  bool
	FilterReasons []string
	Score         float64
	ScoreReasons  []string
}

// FilterFn 决定节点是否符合请求。返回空切片表示通过；非空切片给出拒绝理由（留痕）。
type FilterFn func(n config.Node, req Request) []string

// ScoreFn 给节点打分；分数高者优先。返回 (分数, 评分理由)。
type ScoreFn func(n config.Node, req Request) (float64, []string)

// Policy 是过滤 + 评分的有序集合（按顺序应用过滤器，依次打分）。
type Policy struct {
	Name   string
	Filter FilterFn
	Score  ScoreFn
}

// DefaultPolicy 是出厂策略：
//   - Filter：节点在线、容量足够、StorageBackend 匹配；
//   - Score：剩余 RAM 比例（60%）+ 剩余 Disk 比例（40%）；
//             同租户分散：与该租户已用容器数（节点粒度）成反比。
func DefaultPolicy() Policy {
	return Policy{
		Name:  "default",
		Filter: defaultFilter,
		Score:  defaultScore,
	}
}

// defaultFilter 检查节点是否满足硬性条件：状态、容量、存储后端、维护模式、
// 虚拟化类型、地域 / 分组 / 集群归属。
func defaultFilter(n config.Node, req Request) []string {
	reason := []string{}
	if n.MaintenanceMode {
		reason = append(reason, "node is in maintenance mode")
	}
	if !node.AllowsContainerOps(node.Status(n.Status)) {
		reason = append(reason, fmt.Sprintf("status %q disallows container ops", n.Status))
	}
	if n.RAMTotalMB > 0 && req.RAMMB > 0 && n.RAMTotalMB-n.RAMUsedMB < req.RAMMB {
		reason = append(reason, fmt.Sprintf("insufficient RAM: need %dMB, have %dMB", req.RAMMB, n.RAMTotalMB-n.RAMUsedMB))
	}
	if n.DiskTotalGB > 0 && req.DiskGB > 0 && n.DiskTotalGB-n.DiskUsedGB < req.DiskGB {
		reason = append(reason, fmt.Sprintf("insufficient disk: need %.1fGB, have %.1fGB", req.DiskGB, n.DiskTotalGB-n.DiskUsedGB))
	}
	if req.ContainerCountDelta > 0 && n.ContainerCount+req.ContainerCountDelta > maxContainersPerNode(n) {
		reason = append(reason, fmt.Sprintf("container count limit exceeded"))
	}
	if !backendMatches(n, req.StorageBackend) {
		reason = append(reason, fmt.Sprintf("backend %q not supported by node", req.StorageBackend))
	}
	// VirtType 过滤：节点不支持请求的虚拟化类型则拒绝。
	if req.VirtType != "" {
		if !config.NodeSupportsVirt(n, req.VirtType) {
			reason = append(reason, fmt.Sprintf("virt_type %q not supported by node (supports %v)", req.VirtType, n.VirtTypes))
		}
	}
	// 归属过滤：Region / NodeGroup / Cluster 任一不匹配即拒绝。
	if req.RegionID != "" && n.RegionID != req.RegionID {
		reason = append(reason, fmt.Sprintf("region %q does not match request", n.RegionID))
	}
	if req.NodeGroupID != "" && n.NodeGroupID != req.NodeGroupID {
		reason = append(reason, fmt.Sprintf("node_group %q does not match request", n.NodeGroupID))
	}
	if req.ClusterID != "" {
		if n.ClusterID != req.ClusterID {
			// Cluster 同时覆盖 Region：如果 Cluster 配了 RegionIDs，则该 Region 下的节点也视为属于 Cluster。
			scopeMatches := false
			if cl, ok := config.FindCluster(req.ClusterID); ok {
				for _, rid := range cl.RegionIDs {
					if n.RegionID == rid {
						scopeMatches = true
						break
					}
				}
			}
			if !scopeMatches {
				reason = append(reason, fmt.Sprintf("cluster %q does not cover node (node.cluster=%q)", req.ClusterID, n.ClusterID))
			}
		}
	}
	return reason
}

// maxContainersPerNode 单节点最大实例数（保守值，按节点 RAM 缩放）。
// 避免依赖 Node 字段扩展——若未设，回归保守默认 200。
func maxContainersPerNode(n config.Node) int {
	if n.RAMTotalMB > 0 {
		// 经验值：每 256MB 一个容器上限。
		return int(n.RAMTotalMB / 256)
	}
	return 200
}

// backendMatches 节点是否支持请求的存储后端。
//
// 当前没有"节点能力"字段（Node.StorageBackends），采用保守策略：
//   - dir：所有节点支持；
//   - 其他（P0-2+ 后端）：必须与节点 CapacityTags 或 Region 配置匹配，缺失时一律拒绝。
//   - 默认（空字符串）：视作 dir。
func backendMatches(n config.Node, backend string) (ok bool) {
	if backend == "" {
		backend = "dir"
	}
	if backend == "dir" {
		return true
	}
	// 缺能力注册表：当前没有任何字段声明节点驱动能力；为安全起见拒绝非 dir 请求。
	// P0-2/P1-3 后接入 Agent 注册的 drivers 列表（Node.Drivers []string）后改为白名单匹配。
	return false
}

// defaultScore 综合剩余 RAM/DISK 比例，同租户分散度降低被多次选中的节点分数。
func defaultScore(n config.Node, req Request) (float64, []string) {
	score := 0.0
	reasons := []string{}
	if n.RAMTotalMB > 0 {
		free := float64(n.RAMTotalMB-n.RAMUsedMB) / float64(n.RAMTotalMB)
		score += 0.6 * free
		reasons = append(reasons, fmt.Sprintf("ram_free=%.2f", free))
	} else {
		score += 0.3
		reasons = append(reasons, "ram_unknown=0.3")
	}
	if n.DiskTotalGB > 0 {
		free := (n.DiskTotalGB - n.DiskUsedGB) / n.DiskTotalGB
		score += 0.4 * free
		reasons = append(reasons, fmt.Sprintf("disk_free=%.2f", free))
	} else {
		score += 0.2
		reasons = append(reasons, "disk_unknown=0.2")
	}
	// 同租户分散度：req.TenantID 已知时，节点上同租户实例数越多评分越低。
	// 当前 Node 字段无"按租户分桶"，用 ContainerCount 做近似。
	if req.TenantID != "" && n.ContainerCount > 0 {
		penalty := 0.05 * float64(n.ContainerCount)
		if penalty > 0.3 {
			penalty = 0.3
		}
		score -= penalty
		reasons = append(reasons, fmt.Sprintf("tenant_spread_penalty=-%.2f", penalty))
	}
	return score, reasons
}

// Place 执行单次调度：返回选中的节点 ID + 诊断；全节点失败时 err 非空。
//
// 默认 top-N=3。失败时 Diagnostics.Candidates 仍含 top-N（按 Score 降序），便于调用方解释。
func Place(req Request, policy Policy, topN int) (Diagnostics, error) {
	if topN <= 0 {
		topN = 3
	}
	nodes := snapshotNodes()
	candidates := make([]CandidateScore, 0, len(nodes))
	for _, n := range nodes {
		cs := CandidateScore{NodeID: n.ID}
		cs.FilterReasons = policy.Filter(n, req)
		cs.PassedFilter = len(cs.FilterReasons) == 0
		if cs.PassedFilter {
			cs.Score, cs.ScoreReasons = policy.Score(n, req)
		} else {
			cs.Score = -1
		}
		candidates = append(candidates, cs)
	}
	// 按 Score 降序。
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	// 留痕：topN。
	kept := candidates
	if len(kept) > topN {
		kept = kept[:topN]
	}
	diag := Diagnostics{
		RequestID:  req.RequestID,
		Candidates: kept,
	}
	if len(candidates) > 0 && candidates[0].PassedFilter {
		diag.Chosen = candidates[0].NodeID
		diag.Reason = "highest score among passing candidates"
	} else {
		diag.Reason = summarizeRejection(candidates)
	}
	if diag.Chosen != "" {
		recordDecision(diag)
		return diag, nil
	}
	recordDecision(diag)
	return diag, fmt.Errorf("scheduler: no eligible node (reason: %s)", diag.Reason)
}

// summarizeRejection 把全节点失败的原因聚合成一句话（用于上层错误响应）。
func summarizeRejection(cands []CandidateScore) string {
	count := 0
	offline := 0
	capacity := 0
	backend := 0
	for _, c := range cands {
		if c.PassedFilter {
			continue
		}
		count++
		for _, r := range c.FilterReasons {
			if containsAny(r, []string{"status", "maintenance", "draining", "offline"}) {
				offline++
			} else if containsAny(r, []string{"RAM", "disk", "container count"}) {
				capacity++
			} else if containsAny(r, []string{"backend"}) {
				backend++
			}
		}
	}
	if count == 0 {
		return "no nodes registered"
	}
	return fmt.Sprintf("rejected %d/%d nodes (offline/state=%d, capacity=%d, backend=%d)", count, len(cands), offline, capacity, backend)
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// snapshotNodes 拷贝当前节点快照，避免持锁期间调用上层。
func snapshotNodes() []config.Node {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	out := make([]config.Node, len(config.AppConfig.Nodes))
	copy(out, config.AppConfig.Nodes)
	return out
}

// ---- Decision 记录（P1-2 验收"留痕"） ----

// Decision 是单次调度的留痕记录（也写入配置库 scheduling_decisions）。
type Decision struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id"`
	NodeID     string    `json:"chosen_node_id,omitempty"`
	Reason     string    `json:"reason"`
	Candidates []CandidateScore `json:"candidates"`
	At         time.Time `json:"at"`
}

// recordDecision 全局留痕（仅写内存 ring buffer；P7-1 events 表前不持久化到配置库，
// 减少 P1-2 范围；记录仍可用于 /api/scheduler/decisions 调试端点）。
func recordDecision(diag Diagnostics) {
	decisionsMu.Lock()
	defer decisionsMu.Unlock()
	decisions = append(decisions, Decision{
		ID:         fmt.Sprintf("dec-%d", time.Now().UnixNano()),
		RequestID:  diag.RequestID,
		NodeID:     diag.Chosen,
		Reason:     diag.Reason,
		Candidates: diag.Candidates,
		At:         time.Now(),
	})
	if len(decisions) > 1024 {
		decisions = decisions[len(decisions)-1024:]
	}
}

var (
	decisionsMu sync.Mutex
	decisions   []Decision
)

// Decisions 返回近期留痕（深拷贝，调试/审计用）。
func Decisions() []Decision {
	decisionsMu.Lock()
	defer decisionsMu.Unlock()
	out := make([]Decision, len(decisions))
	copy(out, decisions)
	return out
}

// EncodeDecision 将决策序列化为 JSON（API 输出用）。
func EncodeDecision(d Decision) ([]byte, error) {
	return json.Marshal(d)
}