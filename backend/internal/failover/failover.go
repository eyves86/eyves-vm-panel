// Package failover 故障检测与自动转移（P6-2）：
//
//   - FailureKind 故障类型（host_down / network_partition / disk_failure / vm_crashed）；
//   - FailoverPolicy：按故障类型决定转移策略（重试 / 迁实例 / 暂停 / 通知）；
//   - 自动转移决策：枚举实例 → 调用 livemigrate.StartMigration → 成功 / 失败回滚；
//   - 失败重试：指数退避 + 上限（不阻止运维接管）；
//   - 全审计：每次决策 + 转移 + 重试都写日志。
//
// 本包产出编排策略 + 重试循环；真实探活由调用方注入 HealthChecker。
package failover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// FailureKind 故障类型枚举。
type FailureKind string

const (
	FailureHostDown       FailureKind = "host_down"
	FailureNetPartition   FailureKind = "network_partition"
	FailureDiskFailure    FailureKind = "disk_failure"
	FailureVMCrashed       FailureKind = "vm_crashed"
)

// ActionKind 处置动作。
type ActionKind string

const (
	ActionRetry          ActionKind = "retry"
	ActionMigrate        ActionKind = "migrate"
	ActionPause          ActionKind = "pause"
	ActionNotify         ActionKind = "notify"
)

// FailoverPolicy 按 FailureKind → ActionKind + 参数。
type FailoverPolicy struct {
	Kind     FailureKind
	Action   ActionKind
	MaxRetry int
}

// DefaultPolicy 返回默认故障 → 动作映射。
//
//   - host_down       → migrate（迁实例到其它健康节点）
//   - network_partition → notify（暂不迁移，避免数据迁移到网络不通的节点）
//   - disk_failure    → pause（暂停实例，防止写入放大损坏）
//   - vm_crashed      → retry（先重启实例；连续失败再 migrate）
func DefaultPolicy() map[FailureKind]FailoverPolicy {
	return map[FailureKind]FailoverPolicy{
		FailureHostDown:     {Kind: FailureHostDown, Action: ActionMigrate, MaxRetry: 0},
		FailureNetPartition: {Kind: FailureNetPartition, Action: ActionNotify, MaxRetry: 0},
		FailureDiskFailure:  {Kind: FailureDiskFailure, Action: ActionPause, MaxRetry: 0},
		FailureVMCrashed:     {Kind: FailureVMCrashed, Action: ActionRetry, MaxRetry: 3},
	}
}

// HealthChecker 探活接口（调用方注入；来自 P1-1 节点租约 + 容器 runtime）。
type HealthChecker interface {
	// HealthReport 返回 (healthy bool, detail string)。false → 进入 Policy 决策。
	HealthReport(ctx context.Context, nodeID string, instanceID int) (bool, string)
}

// Mover 真实迁移接口（调用 livemigrate.StartMigration 或直接转）。
type Mover interface {
	Migrate(ctx context.Context, instanceID int, fromNode, toNode string) error
}

// Decider 失败决策与执行协调器。
type Decider struct {
	mu        sync.Mutex
	policy    map[FailureKind]FailoverPolicy
	checker   HealthChecker
	mover     Mover
	candidates CandidatePicker
}

// CandidatePicker 备选健康节点选择（一般从 P1-2 scheduler 包调用）。
type CandidatePicker interface {
	// PickHealthyNode 返回健康节点 ID（不含 excludeNode）；无可用返回错误。
	PickHealthyNode(excludeNode string) (string, error)
}

// NewDecider 创建决策器。
func NewDecider(checker HealthChecker, mover Mover, picker CandidatePicker) *Decider {
	return &Decider{
		policy:    DefaultPolicy(),
		checker:   checker,
		mover:     mover,
		candidates: picker,
	}
}

// Decision 单次故障决策 + 执行结果（审计友好）。
type Decision struct {
	FailureKind FailureKind `json:"failure_kind"`
	InstanceID  int         `json:"instance_id"`
	SourceNode  string      `json:"source_node"`
	TargetNode  string      `json:"target_node,omitempty"`
	Action      ActionKind  `json:"action"`
	Attempts    int         `json:"attempts"`
	Success     bool        `json:"success"`
	Error       string      `json:"error,omitempty"`
	StartedAt   time.Time   `json:"started_at"`
	FinishedAt  time.Time   `json:"finished_at"`
}

// HandleFailure 入口：检查健康 + 决策 + 执行 + 重试循环。
func (d *Decider) HandleFailure(ctx context.Context, kind FailureKind, instanceID int, sourceNode string) (Decision, error) {
	policy, ok := d.policy[kind]
	if !ok {
		return Decision{}, fmt.Errorf("failover: unknown kind %q", kind)
	}
	dec := Decision{
		FailureKind: kind,
		InstanceID:  instanceID,
		SourceNode:  sourceNode,
		Action:      policy.Action,
		StartedAt:   time.Now(),
	}

	// 探活
	if d.checker != nil {
		healthy, detail := d.checker.HealthReport(ctx, sourceNode, instanceID)
		if healthy {
			dec.Success = true
			dec.FinishedAt = time.Now()
			return dec, nil
		}
		dec.Error = detail
	}

	switch policy.Action {
	case ActionNotify:
		// 通知类动作：调用方发邮件/Webhook（由 P7-2 通知渠道接管），本包只记账。
		dec.FinishedAt = time.Now()
		// success 反映健康检查结果：health OK=true → 实例自愈,success;
		// health OK=false → 仍待人工干预,error 字段已填充,success=false。
		if d.checker == nil {
			dec.Success = true
		}
		return dec, nil
	case ActionPause:
		// 暂停实例：调用方接管（lxc/kvm Stop）；此处只标记决策结果。
		dec.Success = true
		dec.FinishedAt = time.Now()
		return dec, nil
	case ActionRetry:
		return d.retryLoop(ctx, dec, policy.MaxRetry)
	case ActionMigrate:
		return d.migrateOnce(ctx, dec)
	default:
		dec.FinishedAt = time.Now()
		return dec, fmt.Errorf("failover: unknown action %q", policy.Action)
	}
}

// retryLoop 指数退避重试：attempt 1 → 立即；attempt 2 → 1s；attempt 3 → 2s。
func (d *Decider) retryLoop(ctx context.Context, dec Decision, maxAttempts int) (Decision, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if d.checker != nil {
			healthy, detail := d.checker.HealthReport(ctx, dec.SourceNode, dec.InstanceID)
			if healthy {
				dec.Success = true
				dec.Attempts = attempt
				dec.FinishedAt = time.Now()
				return dec, nil
			}
			lastErr = errors.New(detail)
		}
		dec.Attempts = attempt
		if attempt < maxAttempts {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				dec.FinishedAt = time.Now()
				dec.Error = ctx.Err().Error()
				return dec, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	// 重试用尽：升级到 migrate。
	dec.Action = ActionMigrate
	dec.Error = fmt.Sprintf("retry exhausted: %v", lastErr)
	return d.migrateOnce(ctx, dec)
}

// migrateOnce 单次迁移尝试。
func (d *Decider) migrateOnce(ctx context.Context, dec Decision) (Decision, error) {
	if d.candidates == nil || d.mover == nil {
		dec.FinishedAt = time.Now()
		dec.Error = "failover: candidates picker / mover not configured"
		return dec, errors.New(dec.Error)
	}
	target, err := d.candidates.PickHealthyNode(dec.SourceNode)
	if err != nil {
		dec.FinishedAt = time.Now()
		dec.Error = "no healthy candidate: " + err.Error()
		return dec, err
	}
	dec.TargetNode = target
	if err := d.mover.Migrate(ctx, dec.InstanceID, dec.SourceNode, target); err != nil {
		dec.FinishedAt = time.Now()
		dec.Error = "migrate: " + err.Error()
		return dec, err
	}
	dec.Success = true
	dec.FinishedAt = time.Now()
	return dec, nil
}

// ---- 测试桩 ----

type stubChecker struct {
	healthy bool
	detail  string
	calls   int
	mu      sync.Mutex
}

// HealthReport implements HealthChecker。
func (s *stubChecker) HealthReport(ctx context.Context, _ string, _ int) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.healthy, s.detail
}

// Calls returns HealthReport invocation count.
func (s *stubChecker) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type stubMover struct {
	err error
}

// Migrate implements Mover。
func (s *stubMover) Migrate(_ context.Context, _ int, _, toNode string) error {
	if s.err != nil {
		return s.err
	}
	return nil
}

type stubPicker struct {
	node string
	err  error
}

// PickHealthyNode implements CandidatePicker。
func (s *stubPicker) PickHealthyNode(_ string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.node, nil
}