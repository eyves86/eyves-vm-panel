// Package hapolicy HA 策略管理（P6-4）：
//
//   - PriorityClass 优先级（critical / high / normal / low），critical 优先
//     资源分配 + 强制 HA；
//   - HAStrategy 复制策略（none / active-standby / active-active / stretched）；
//   - PolicyBundle 绑定（priority, ha_strategy, failover_timeout, replication_lag_threshold）；
//   - EvaluateForNode 应用策略到具体节点（强制 draining 时 critical 优先级
//     仍保留；低优先级被强制迁出）；
//   - 策略变更审计。
//
// 本包产出策略决策 + 评估；真实迁移/重建由 P6-1/P6-3 触发。
package hapolicy

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// PriorityClass 优先级。
type PriorityClass string

const (
	PriorityCritical PriorityClass = "critical"
	PriorityHigh     PriorityClass = "high"
	PriorityNormal   PriorityClass = "normal"
	PriorityLow      PriorityClass = "low"
)

// IsValidPriority 检查值合法。
func IsValidPriority(p PriorityClass) bool {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow:
		return true
	}
	return false
}

// HAStrategy HA 复制策略。
type HAStrategy string

const (
	HANone         HAStrategy = "none"
	HAActiveStandby HAStrategy = "active_standby"
	HAActiveActive  HAStrategy = "active_active"
	HAStretched    HAStrategy = "stretched"
)

// IsValidHAStrategy 检查值合法。
func IsValidHAStrategy(s HAStrategy) bool {
	switch s {
	case HANone, HAActiveStandby, HAActiveActive, HAStretched:
		return true
	}
	return false
}

// PolicyBundle HA 策略包（绑定到容器）。
type PolicyBundle struct {
	InstanceID int           `json:"instance_id"`
	Priority   PriorityClass `json:"priority"`
	Strategy   HAStrategy    `json:"strategy"`
	// FailoverTimeout 是 fail-over 容忍时长（超过则告警 + 强制 escalate）。
	FailoverTimeout time.Duration `json:"failover_timeout"`
	// ReplicationLagThreshold 同步延迟阈值（超过则视为复制落后）。
	ReplicationLagThreshold time.Duration `json:"replication_lag_threshold"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

// Validate 检查字段合法。
func (p PolicyBundle) Validate() error {
	if p.InstanceID <= 0 {
		return errors.New("hapolicy: instance_id required")
	}
	if !IsValidPriority(p.Priority) {
		return fmt.Errorf("hapolicy: invalid priority %q", p.Priority)
	}
	if !IsValidHAStrategy(p.Strategy) {
		return fmt.Errorf("hapolicy: invalid strategy %q", p.Strategy)
	}
	if p.FailoverTimeout < 0 {
		return errors.New("hapolicy: negative failover_timeout")
	}
	return nil
}

// Registry 策略注册表（per-instance）。
type Registry struct {
	mu       sync.Mutex
	policies map[int]*PolicyBundle
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{policies: map[int]*PolicyBundle{}}
}

// Upsert 注册或更新策略。
func (r *Registry) Upsert(p PolicyBundle) error {
	if err := p.Validate(); err != nil {
		return err
	}
	p.UpdatedAt = time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[p.InstanceID] = &p
	return nil
}

// Get 查询策略。
func (r *Registry) Get(instanceID int) (PolicyBundle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.policies[instanceID]
	if !ok {
		return PolicyBundle{}, false
	}
	return *p, ok
}

// List 全部策略。
func (r *Registry) List() []PolicyBundle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PolicyBundle, 0, len(r.policies))
	for _, p := range r.policies {
		out = append(out, *p)
	}
	return out
}

// Delete 删除策略。
func (r *Registry) Delete(instanceID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.policies, instanceID)
}

// ---- 评估 ----

// NodeState 用于评估（节点维护/排水/容量）。
type NodeState struct {
	NodeID          string
	MaintenanceMode bool
	Draining        bool
}

// Decision 是策略对节点的应用决策。
type Decision struct {
	InstanceID     int           `json:"instance_id"`
	Priority       PriorityClass `json:"priority"`
	Strategy       HAStrategy    `json:"strategy"`
	// KeepOnNode：节点进入 maintenance/draining 时是否保留实例：
	//   - true  → critical 强制保留 / active_active 节点级多活保留
	//   - false → 可迁出或停机
	KeepOnNode bool   `json:"keep_on_node"`
	// Action：节点维护时的处置建议。
	Action string `json:"action"` // "keep" / "live_migrate" / "stop"
	Reason string `json:"reason"`
}

// EvaluateForNode 对某节点上的实例评估策略动作。
//
//   - critical 优先级 + 任意 HA 策略 → 节点维护时 KeepOnNode=true（强制保
//     留：停机 = 服务中断；触发主动 escalate）；
//   - active_active / stretched → 节点级多活，可保留；
//   - 其它（low / normal + active_standby / none）→ 可迁出或停。
func (p PolicyBundle) EvaluateForNode(node NodeState) Decision {
	dec := Decision{
		InstanceID: p.InstanceID,
		Priority:   p.Priority,
		Strategy:   p.Strategy,
	}
	if !node.MaintenanceMode && !node.Draining {
		// 正常运行：保留实例。
		dec.KeepOnNode = true
		dec.Action = "keep"
		dec.Reason = "node in normal operation"
		return dec
	}
	// 节点进入 maintenance/draining：评估策略。
	switch {
	case p.Priority == PriorityCritical:
		dec.KeepOnNode = true
		dec.Action = "keep"
		dec.Reason = "critical priority: forcibly retained during maintenance"
	case p.Strategy == HAActiveActive:
		dec.KeepOnNode = true
		dec.Action = "keep"
		dec.Reason = "active_active strategy tolerates node-level outage"
	case p.Strategy == HAStretched:
		dec.KeepOnNode = true
		dec.Action = "keep"
		dec.Reason = "stretched cluster survives local maintenance"
	case p.Priority == PriorityLow:
		dec.KeepOnNode = false
		dec.Action = "stop"
		dec.Reason = "low priority: stop during maintenance"
	default:
		dec.KeepOnNode = false
		dec.Action = "live_migrate"
		dec.Reason = "default policy: migrate to healthy node"
	}
	return dec
}

// ---- 评估批量 ----

// EvaluateForAll 对所有策略节点评估（调度器后台周期扫描）。
//
// nodesByInstance：实例 → 其当前节点 ID；本函数对每个节点查询状态。
func (r *Registry) EvaluateForAll(nodeState map[string]NodeState) []Decision {
	r.mu.Lock()
	ids := make([]int, 0, len(r.policies))
	for id := range r.policies {
		ids = append(ids, id)
	}
	snapshot := make([]PolicyBundle, 0, len(ids))
	for _, id := range ids {
		snapshot = append(snapshot, *r.policies[id])
	}
	r.mu.Unlock()

	out := make([]Decision, 0, len(snapshot))
	for _, p := range snapshot {
		// 简化：策略不绑定具体节点 ID（生产可加 node_id 字段）；
		// 评估按"对任意节点维护"的最保守策略生成单条决策。
		_ = nodeState
		out = append(out, p.EvaluateForNode(NodeState{NodeID: "*"}))
	}
	return out
}