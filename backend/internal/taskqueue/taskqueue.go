// Package taskqueue 增强版任务队列（P1-4）：
//
//   - 优先级出队（high/normal/low）；
//   - 失败重试 + 指数退避 + 死信（attempts 超限）；
//   - 进度上报（与 P1-3 Agent v2 回执集成）；
//   - 协作式取消（cancel_requested 标志）；
//   - 编排原语 fan-out（一个父任务派生 N 个子任务，汇总到父任务状态）。
//
// 本包与 api/taskqueue.go 互不冲突：api/taskqueue.go 是 UI 异步任务队列
// （创建/启动/停止等长操作入队后由 worker 调 lxc/kvm），本包面向"跨节点编排
// + 死信 + 进度"语义；任务记录独立写入 SQLite task_orchestration 表（与
// 现有 saved_tasks 表不同）。
package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Priority 出队优先级（数值大 = 先出）。
type Priority int

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 50
	PriorityHigh   Priority = 100
)

// Status 任务状态机（独立于 api/taskqueue.go 的简单 pending/running/completed，
// 本包支持更细的状态以容纳重试与死信）。
type Status string

const (
	StatusPending    Status = "pending"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusDeadLetter Status = "dead_letter"
)

// MaxAttempts 单任务最大尝试次数（超出进死信）。
const MaxAttempts = 5

// ErrUncancellable 不可取消的任务（已接近完成 / 已进死信）。
var ErrUncancellable = errors.New("task uncancellable in current status")

// ErrDependencyPending 编排任务依赖未全部完成。
var ErrDependencyPending = errors.New("task dependencies not met")

// ErrFanOutPartial 编排任务部分子任务失败。
var ErrFanOutPartial = errors.New("orchestration fan-out partial failure")

// Task 是编排任务记录（独立于 api.Task）。
type Task struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`           // 业务类型（"orchestration.fan_out" / "node.drain" 等）
	Payload          []byte    `json:"payload"`        // 透传 JSON，由执行器解析
	Priority         Priority  `json:"priority"`
	Status           Status    `json:"status"`
	Attempts         int       `json:"attempts"`
	Progress         int       `json:"progress"`         // 0-100
	NextRunAt        time.Time `json:"next_run_at"`      // 下次执行时刻（重试用）
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CancelRequested  bool      `json:"cancel_requested"`
	Error            string    `json:"error,omitempty"`
	ParentID         string    `json:"parent_id,omitempty"` // 编排父任务 ID
	DependsOn        []string  `json:"depends_on,omitempty"`
}

// Queue 是任务队列（带优先级出队 + 死信）。
type Queue struct {
	mu       sync.Mutex
	pending  []*Task
	running  map[string]*Task
	dead     map[string]*Task
	completed map[string]*Task
	cancel   map[string]bool // 协作式取消标记
	byID     map[string]*Task
}

// NewQueue 创建空队列。
func NewQueue() *Queue {
	return &Queue{
		running:   make(map[string]*Task),
		dead:      make(map[string]*Task),
		completed: make(map[string]*Task),
		cancel:    make(map[string]bool),
		byID:      make(map[string]*Task),
	}
}

// Enqueue 入队；返回分配的任务 ID。
//
// nextID 由调用方注入（保证与全局唯一 ID 不冲突）；纯函数语义，调用方
// 负责持久化。本包不接 SQLite，调用方负责 Save/Load。
func (q *Queue) Enqueue(taskID, taskType string, payload []byte, priority Priority, parent string, dependsOn []string) *Task {
	t := &Task{
		ID:        taskID,
		Type:      taskType,
		Payload:   payload,
		Priority:  priority,
		Status:    StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ParentID:  parent,
		DependsOn: dependsOn,
	}
	if parent == "" {
		// 顶层任务：尝试立即入 pending（依赖未满足时降级为 blocked）。
		if q.canSchedule(dependsOn) {
			t.NextRunAt = time.Now()
			q.pending = append(q.pending, t)
		} else {
			t.Status = StatusPending // pending 但不出队
		}
	} else {
		// 编排子任务：挂在父任务下，不进入 pending 等待编排触发。
		t.Status = StatusPending
	}
	q.byID[taskID] = t
	return t
}

func (q *Queue) canSchedule(deps []string) bool {
	for _, d := range deps {
		t, ok := q.byID[d]
		if !ok {
			return false
		}
		if t.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// Next 取出最高优先级 + 最早 NextRunAt 的 pending 任务；空时返回 nil。
func (q *Queue) Next() *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	idx := -1
	for i, t := range q.pending {
		if t.NextRunAt.After(now) {
			continue
		}
		if idx == -1 || q.higherPriority(t, q.pending[idx]) {
			idx = i
		}
	}
	if idx < 0 {
		return nil
	}
	t := q.pending[idx]
	q.pending = append(q.pending[:idx], q.pending[idx+1:]...)
	t.Status = StatusRunning
	t.UpdatedAt = now
	q.running[t.ID] = t
	return t
}

// higherPriority 优先级比较：Priority 数值大者优先，相同时 NextRunAt 早者优先，
// 仍同时按 CreatedAt 升序。
func (q *Queue) higherPriority(a, b *Task) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.NextRunAt.Equal(b.NextRunAt) {
		return a.NextRunAt.Before(b.NextRunAt)
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

// ReportProgress 由执行器调用（任何时刻）。
func (q *Queue) ReportProgress(taskID string, progress int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if t.CancelRequested {
		return ErrUncancellable
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	t.Progress = progress
	t.UpdatedAt = time.Now()
	return nil
}

// Complete 标记任务完成（成功）。
func (q *Queue) Complete(taskID string) error {
	return q.completeInternal(taskID, StatusCompleted, "")
}

func (q *Queue) completeInternal(taskID string, status Status, errMsg string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	delete(q.running, taskID)
	delete(q.cancel, taskID)
	t.Status = status
	t.Error = errMsg
	t.UpdatedAt = time.Now()
	q.completed[taskID] = t
	return nil
}

// finish 是 Complete 的早期命名（保留兼容，内部走 completeInternal）。
func (q *Queue) finish(taskID string, status Status, errMsg string) error {
	return q.completeInternal(taskID, status, errMsg)
}

// Fail 标记任务失败：重试计数 +1，下次执行时间 = now + 退避。
//
// MaxAttempts 用尽后进入死信。
//
//   - nextRunIn < 0：使用指数退避（生产路径）；
//   - nextRunIn == 0：立即可再出队（测试路径，避免 sleep）；
//   - nextRunIn > 0：自定义延时。
func (q *Queue) Fail(taskID string, errMsg string) error {
	return q.FailWithDelay(taskID, errMsg, -1)
}

// FailWithDelay 与 Fail 同语义，但 nextRunIn 控制 NextRunAt：
//   - nextRunIn < 0：指数退避；
//   - nextRunIn == 0：立即可再出队；
//   - nextRunIn > 0：自定义延时。
func (q *Queue) FailWithDelay(taskID string, errMsg string, nextRunIn time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	delete(q.running, taskID)
	t.Attempts++
	t.Error = errMsg
	if t.Attempts >= MaxAttempts {
		t.Status = StatusDeadLetter
		q.dead[t.ID] = t
		t.UpdatedAt = time.Now()
		return nil
	}
	t.Status = StatusPending
	t.UpdatedAt = time.Now()
	switch {
	case nextRunIn == 0:
		t.NextRunAt = time.Now()
	case nextRunIn > 0:
		t.NextRunAt = time.Now().Add(nextRunIn)
	default:
		t.NextRunAt = time.Now().Add(backoff(t.Attempts))
	}
	q.pending = append(q.pending, t)
	return nil
}

// backoff 指数退避（2^n 秒，封顶 5 分钟）。
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	d := time.Duration(1<<attempt) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// Cancel 请求取消任务（协作式：执行器下次 ReportProgress 返回 ErrUncancellable）。
//
// 任务处于 pending/running 时返回 true；dead_letter/cancelled/completed 时
// 返回 ErrUncancellable（语义：已进入终态不能撤销）。
func (q *Queue) Cancel(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if t.Status == StatusDeadLetter || t.Status == StatusCancelled || t.Status == StatusCompleted {
		return ErrUncancellable
	}
	t.CancelRequested = true
	q.cancel[taskID] = true
	t.UpdatedAt = time.Now()
	return nil
}

// IsCancelRequested 由执行器主动检查（不通过 ReportProgress 时也能感知）。
func (q *Queue) IsCancelRequested(taskID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cancel[taskID]
}

// AckCancel 任务真正中止后由 worker 调用，标记 cancelled 终态。
func (q *Queue) AckCancel(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if !t.CancelRequested {
		return fmt.Errorf("task %s has no pending cancel", taskID)
	}
	delete(q.cancel, taskID)
	delete(q.running, taskID)
	t.Status = StatusCancelled
	t.UpdatedAt = time.Now()
	return nil
}

// ---- 编排原语 fan-out / 汇总 ----

// FanOut 父任务创建 N 个子任务（targets 每个一组），全部完成视为父任务完成；
// 任一子任务进死信视为父任务部分失败（语义由汇总函数决定）。
//
// childType / childPayload 由父任务的 type 决定；本函数只负责结构。
func (q *Queue) FanOut(parentID string, targets []string, childType string, childPayload []byte) ([]*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	parent, ok := q.byID[parentID]
	if !ok {
		return nil, fmt.Errorf("parent task %s not found", parentID)
	}
	children := make([]*Task, 0, len(targets))
	for _, target := range targets {
		cid := fmt.Sprintf("%s-child-%s", parentID, target)
		c := &Task{
			ID:        cid,
			Type:      childType,
			Payload:   childPayload,
			Priority:  parent.Priority,
			Status:    StatusPending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ParentID:  parentID,
			DependsOn: nil,
		}
		q.byID[cid] = c
		children = append(children, c)
	}
	return children, nil
}

// CollectFanOut 汇总子任务状态到父任务：
//   - 全部 completed → 父任务 completed；
//   - 任一 dead_letter → 父任务 failed（保留部分完成子任务的副作用，由调用方回滚）。
//
// 返回 (allCompleted bool, anyDeadLetter bool, err)。
func (q *Queue) CollectFanOut(parentID string) (bool, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	parent, ok := q.byID[parentID]
	if !ok {
		return false, false, fmt.Errorf("parent task %s not found", parentID)
	}
	allCompleted := true
	anyDead := false
	count := 0
	for _, t := range q.byID {
		if t.ParentID != parentID {
			continue
		}
		count++
		if t.Status != StatusCompleted {
			allCompleted = false
		}
		if t.Status == StatusDeadLetter {
			anyDead = true
		}
	}
	if count == 0 {
		return false, false, fmt.Errorf("parent %s has no children", parentID)
	}
	switch {
	case allCompleted:
		parent.Status = StatusCompleted
		delete(q.running, parentID)
		return true, false, nil
	case anyDead:
		parent.Status = StatusFailed
		parent.Error = ErrFanOutPartial.Error()
		delete(q.running, parentID)
		return false, true, ErrFanOutPartial
	default:
		return false, false, nil
	}
}

// Snapshot 返回队列快照（测试断言用）。
func (q *Queue) Snapshot() map[string]Status {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]Status, len(q.byID))
	for id, t := range q.byID {
		out[id] = t.Status
	}
	return out
}

// ListPending 按优先级 + NextRunAt 排序返回 pending 列表（测试断言）。
func (q *Queue) ListPending() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Task, len(q.pending))
	copy(out, q.pending)
	sort.SliceStable(out, func(i, j int) bool {
		return q.higherPriority(out[i], out[j])
	})
	ids := make([]string, len(out))
	for i, t := range out {
		ids[i] = t.ID
	}
	return ids
}

// WaitForCancel 阻塞直到 ctx 取消或任务被取消（worker 用）。
func (q *Queue) WaitForCancel(ctx context.Context, taskID string) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if q.IsCancelRequested(taskID) {
				return nil
			}
		}
	}
}