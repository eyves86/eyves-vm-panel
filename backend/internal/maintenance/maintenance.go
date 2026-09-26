// Package maintenance 节点维护模式与排水（P6-3）：
//
//   - MaintenanceMode:节点进入 maintenance 状态（来自 P1-1 node.AllowsNewInstance
//     拒绝新实例），当前实例继续运行；
//   - DrainMode:节点进入 draining 状态（P1-1 拒绝新实例 + 拒操作），
//     同时把现有实例按策略迁出：
//       - evacuate：全部迁出，迁完才能进入 maintenance；
//       - live-migrate：实时迁移（共享存储）；本地存储走 stop+copy+start；
//       - graceful-stop：直接停（驱逐场景）；
//   - DrainPolicy:per node 节点默认排水策略；
//   - 全审计：每个迁出/停动作记录在 AuditLog（调用方持久化）。
//
// 排水计划：检查节点当前实例列表 → 选目标节点（来自 P1-2 scheduler）→
// 调 livemigrate.Mover → 进度跟踪。
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DrainStrategy 排水策略。
type DrainStrategy string

const (
	StrategyEvacuate   DrainStrategy = "evacuate"    // 实时迁移全部实例到其它节点
	StrategyLiveMigrate DrainStrategy = "live_migrate" // 实时迁移（共享存储）
	StrategyGracefulStop DrainStrategy = "graceful_stop" // 停机（数据丢失风险）
)

// DrainState 节点排水进度。
type DrainState string

const (
	DrainIdle        DrainState = "idle"
	DrainInProgress  DrainState = "in_progress"
	DrainCompleted   DrainState = "completed"
	DrainFailed      DrainState = "failed"
)

// DrainPlan 一次排水计划。
type DrainPlan struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"node_id"`
	Strategy   DrainStrategy `json:"strategy"`
	State      DrainState `json:"state"`
	Instances  []int     `json:"instance_ids"`
	MovedTo    map[int]string `json:"moved_to"` // instance_id -> target_node
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// InstanceLister 节点上当前实例查询（调用方注入）。
type InstanceLister interface {
	ListInstances(nodeID string) ([]int, error)
}

// Mover 实时迁移接口（与 P6-1 共享）。
type Mover interface {
	Migrate(ctx context.Context, instanceID int, fromNode, toNode string) error
	// Stop 用于 graceful_stop 策略。
	Stop(ctx context.Context, instanceID int, nodeID string) error
}

// TargetPicker 排水目标选择（一般从 P1-2 scheduler）。
type TargetPicker interface {
	PickHealthyNode(excludeNode string) (string, error)
}

// Engine 排水编排器。
type Engine struct {
	mu      sync.Mutex
	lister  InstanceLister
	mover   Mover
	picker  TargetPicker
	plans   map[string]*DrainPlan
}

// NewEngine 创建引擎。
func NewEngine(lister InstanceLister, mover Mover, picker TargetPicker) *Engine {
	return &Engine{lister: lister, mover: mover, picker: picker, plans: map[string]*DrainPlan{}}
}

// StartDrain 启动排水计划；顺序迁出每个实例，任一失败把 DrainState=failed。
//
// 返回 Plan 永远非 nil；调用方持久化并写审计。
func (e *Engine) StartDrain(ctx context.Context, nodeID string, strategy DrainStrategy) (*DrainPlan, error) {
	if e.lister == nil || e.mover == nil || e.picker == nil {
		return nil, errors.New("maintenance: lister/mover/picker required")
	}
	if nodeID == "" {
		return nil, errors.New("maintenance: node_id required")
	}
	instances, err := e.lister.ListInstances(nodeID)
	if err != nil {
		return nil, fmt.Errorf("maintenance: list instances: %w", err)
	}
	plan := &DrainPlan{
		ID:        fmt.Sprintf("drain-%s-%d", nodeID, time.Now().UnixNano()),
		NodeID:    nodeID,
		Strategy:  strategy,
		State:     DrainInProgress,
		Instances: instances,
		MovedTo:   map[int]string{},
		StartedAt: time.Now(),
	}
	e.record(plan)

	for _, inst := range instances {
		target, perr := e.picker.PickHealthyNode(nodeID)
		if perr != nil {
			plan.Error = fmt.Sprintf("pick target for instance %d: %v", inst, perr)
			plan.State = DrainFailed
			plan.FinishedAt = time.Now()
			e.recordUpdate(plan)
			return plan, perr
		}
		var execErr error
		switch strategy {
		case StrategyEvacuate, StrategyLiveMigrate:
			execErr = e.mover.Migrate(ctx, inst, nodeID, target)
		case StrategyGracefulStop:
			execErr = e.mover.Stop(ctx, inst, nodeID)
			if execErr == nil {
				target = "" // graceful_stop 没有目标节点
			}
		default:
			execErr = fmt.Errorf("maintenance: unknown strategy %q", strategy)
		}
		if execErr != nil {
			plan.Error = fmt.Sprintf("instance %d: %v", inst, execErr)
			plan.State = DrainFailed
			plan.FinishedAt = time.Now()
			e.recordUpdate(plan)
			return plan, execErr
		}
		plan.MovedTo[inst] = target
	}

	plan.State = DrainCompleted
	plan.FinishedAt = time.Now()
	e.recordUpdate(plan)
	return plan, nil
}

// record / recordUpdate 写入 plans map。
func (e *Engine) record(p *DrainPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plans[p.ID] = p
}

func (e *Engine) recordUpdate(p *DrainPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plans[p.ID] = p
}

// Get 查询计划。
func (e *Engine) Get(id string) (DrainPlan, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.plans[id]
	if !ok {
		return DrainPlan{}, false
	}
	return *p, ok
}

// ---- 测试桩 ----

type stubLister struct {
	instances []int
	err       error
}

// ListInstances implements InstanceLister。
func (s *stubLister) ListInstances(string) ([]int, error) { return s.instances, s.err }

type stubMover struct {
	migrateErr error
	stopErr    error
}

// Migrate implements Mover。
func (s *stubMover) Migrate(_ context.Context, _ int, _, _ string) error {
	return s.migrateErr
}

// Stop implements Mover。
func (s *stubMover) Stop(_ context.Context, _ int, _ string) error {
	return s.stopErr
}

type stubPicker struct {
	node string
	err  error
}

// PickHealthyNode implements TargetPicker。
func (s *stubPicker) PickHealthyNode(_ string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.node, nil
}