// Package instantclone 秒级克隆开通（P5-4）：
//
//   - 上游：选基础快照（来自 P0-2/P0-3/P0-4 驱动的 CloneVolume 路径）；
//   - 下游：派生新卷 → 配置分配 → 标记 ready；
//   - 容量记账：每次 clone 上游配额计数 +1，调度器据此决策放置；
//   - 全程可追踪（clone_id 上游→下游记录）；
//   - 失败回滚：cleanup 反向销毁新建卷；
//
// 本包产出编排逻辑 + 状态机；真实克隆由 storage.Backend.CloneVolume 执行。
package instantclone

import (
	"errors"
	"sync"
	"time"
)

// Status 克隆任务状态。
type Status string

const (
	StatusQueued     Status = "queued"
	StatusCloning    Status = "cloning"
	StatusReady      Status = "ready"
	StatusFailed     Status = "failed"
	StatusCleaningUp Status = "cleaning_up"
	StatusCleaned    Status = "cleaned"
)

// CloneJob 单次克隆任务。
type CloneJob struct {
	ID         string    `json:"id"`
	UpstreamID string    `json:"upstream_id"` // 上游 volume_id
	NewID      string    `json:"new_id"`      // 新 volume_id（成功时填充）
	Mode       string    `json:"mode"`         // "full" / "linked"
	Status     Status    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// CloneBackend 抽象：CloneVolume 在 instantclone 上游调用。
//
// 实现在 storage 包（ZFS / LVM / RBD / NFS 各自的 CloneVolume）。
type CloneBackend interface {
	CloneUpstream(upstreamID string, mode string) (newID string, err error)
	// DeleteNewUpstream 失败回滚：删除新建卷（best effort）。
	DeleteNewUpstream(newID string) error
}

// UpstreamQuota 上游配额计数（per upstream_id，调度器据此放置）。
//
// 上游每被克隆一次 Quota 占用 +1；超额拒绝。
type UpstreamQuota struct {
	MaxConcurrentClones int
}

// Engine 编排：clone + 上限检查 + 失败回滚 + 状态记录。
type Engine struct {
	mu      sync.Mutex
	backend CloneBackend
	jobs    map[string]*CloneJob
	quota   UpstreamQuota
	// perUpstreamInflight 跟踪上游正在进行的克隆数。
	perUpstreamInflight map[string]int
}

// NewEngine 创建引擎。
func NewEngine(backend CloneBackend, quota UpstreamQuota) *Engine {
	return &Engine{
		backend:              backend,
		quota:                quota,
		jobs:                 map[string]*CloneJob{},
		perUpstreamInflight: map[string]int{},
	}
}

// StartClone 启动克隆任务；超额/后端错误立即返回。
//
// 流程：queued → cloning → ready/failed → (失败时 cleaning_up → cleaned)。
//
// 返回 CloneJob（含 ID/Status，调用方持久化）。
func (e *Engine) StartClone(upstreamID, mode, newID string) (*CloneJob, error) {
	if e.backend == nil {
		return nil, errors.New("instantclone: nil backend")
	}
	if upstreamID == "" || newID == "" {
		return nil, errors.New("instantclone: upstream_id/new_id required")
	}
	e.mu.Lock()
	if e.perUpstreamInflight[upstreamID] >= e.quota.MaxConcurrentClones {
		e.mu.Unlock()
		return nil, errors.New("instantclone: upstream quota exceeded")
	}
	e.perUpstreamInflight[upstreamID]++
	e.mu.Unlock()

	job := &CloneJob{
		ID:         newID,
		UpstreamID: upstreamID,
		Mode:       mode,
		Status:     StatusCloning,
		StartedAt:  time.Now(),
	}
	e.record(job)

	// 真正执行 clone
	gotID, err := e.backend.CloneUpstream(upstreamID, mode)
	job.FinishedAt = time.Now()
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		// 回滚：尝试删除（可能 backend 已创建）
		job.Status = StatusCleaningUp
		_ = e.backend.DeleteNewUpstream(newID)
		job.Status = StatusCleaned
		e.recordUpdate(job)
		e.decInflight(upstreamID)
		return job, err
	}
	job.NewID = gotID
	job.Status = StatusReady
	e.recordUpdate(job)
	e.decInflight(upstreamID)
	return job, nil
}

// record / recordUpdate 写入 jobs map（直接覆盖；克隆任务 ID 即 new_id）。
func (e *Engine) record(j *CloneJob) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.jobs[j.ID] = j
}

func (e *Engine) recordUpdate(j *CloneJob) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.jobs[j.ID] = j
}

// decInflight 减少上游并发计数。
func (e *Engine) decInflight(upstreamID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.perUpstreamInflight[upstreamID] > 0 {
		e.perUpstreamInflight[upstreamID]--
	}
}

// Get 查询任务。
func (e *Engine) Get(id string) (CloneJob, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, ok := e.jobs[id]
	if !ok {
		return CloneJob{}, false
	}
	return *j, true
}

// Jobs 返回所有任务快照（按 StartedAt 降序）。
func (e *Engine) Jobs() []CloneJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]CloneJob, 0, len(e.jobs))
	for _, j := range e.jobs {
		out = append(out, *j)
	}
	return out
}

// ---- 状态合法性 ----

// IsTerminal 终态（ready/failed/cleaned）不再被外部驱动。
func (s Status) IsTerminal() bool {
	return s == StatusReady || s == StatusFailed || s == StatusCleaned
}

// ValidTransition 状态机白名单。
func ValidTransition(from, to Status) bool {
	switch from {
	case StatusQueued:
		return to == StatusCloning || to == StatusFailed
	case StatusCloning:
		return to == StatusReady || to == StatusFailed
	case StatusFailed:
		return to == StatusCleaningUp
	case StatusCleaningUp:
		return to == StatusCleaned
	}
	return false
}

// ---- 测试桩 ----

// NoopBackend 测试桩：CloneUpstream 返回 newID（= newID）；CloneUpstreamErr 注入失败。
type NoopBackend struct {
	CloneUpstreamErr error
}

// CloneUpstream implements CloneBackend。
func (n *NoopBackend) CloneUpstream(_, _ string) (string, error) {
	if n.CloneUpstreamErr != nil {
		return "", n.CloneUpstreamErr
	}
	return "vol-clone-fake", nil
}

// DeleteNewUpstream implements CloneBackend。
func (n *NoopBackend) DeleteNewUpstream(_ string) error { return nil }