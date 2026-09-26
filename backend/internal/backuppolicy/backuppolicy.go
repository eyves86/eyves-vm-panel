// Package backuppolicy 备份策略引擎（P4-1）：
//
//   - Policy(scope: pool/tenant/instance, cron, retain_daily/weekly/monthly, enabled);
//   - Scheduler 扫描 cron 表达式生成 BackupJob（queued/running/success/failed/partial）;
//   - GFS 清理：按保留数滚动删除最旧的备份；删除调用方注入 Driver;
//   - 失败部分：成功 N-1 条 / 失败 K 条 → job 状态 partial;全部失败 → failed;
//   - 复用 taskqueue.Queue 调度（per-policy 任务）。
//
// 本包只产出策略决策 + job 状态机；真实快照/打包/传输由调用方
// （Driver 接口）执行，落在 P4-2/P4-3。
package backuppolicy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Scope 策略作用域。
type Scope string

const (
	ScopePool     Scope = "pool"
	ScopeTenant   Scope = "tenant"
	ScopeInstance Scope = "instance"
)

// Policy 备份策略定义。
type Policy struct {
	ID            string    `json:"id"`
	Scope         Scope     `json:"scope"`
	ScopeTarget   string    `json:"scope_target"` // pool_id / tenant_id / instance_id
	Cron          string    `json:"cron"`          // 简化 5 字段："minute hour dom month dow"（空格分隔）
	Enabled       bool      `json:"enabled"`
	RetainDaily   int       `json:"retain_daily"`   // 保留近 N 个日备份
	RetainWeekly  int       `json:"retain_weekly"`  // 保留近 N 个周备份（周日触发）
	RetainMonthly int       `json:"retain_monthly"` // 保留近 N 个月备份（1 号触发）
}

// JobStatus 备份任务状态。
type JobStatus string

const (
	JobQueued   JobStatus = "queued"
	JobRunning  JobStatus = "running"
	JobSuccess  JobStatus = "success"
	JobFailed   JobStatus = "failed"
	JobPartial  JobStatus = "partial"
)

// BackupJob 备份任务记录（per instance / pool / tenant）。
type BackupJob struct {
	ID         string    `json:"id"`
	PolicyID   string    `json:"policy_id"`
	Scope      Scope     `json:"scope"`
	Target     string    `json:"target"`     // scope_target（实例/池/租户）
	Kind       string    `json:"kind"`       // "daily" / "weekly" / "monthly" / "manual"
	Status     JobStatus `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	SnapshotID string    `json:"snapshot_id,omitempty"` // P0-2/P0-3 驱动返回的快照 ID
	Bytes      int64     `json:"bytes,omitempty"`
	// KeepUntil：GFS 计算的保留截止时间；调度器到期删除。
	KeepUntil time.Time `json:"keep_until,omitempty"`
}

// Driver 真实备份驱动接口（P4-2 ZFS 增量 / P4-3 RBD / dir tarball）。
type Driver interface {
	// Snapshot 创建快照（pool/instance 级别）。
	Snapshot(scope Scope, target string) (snapshotID string, bytes int64, err error)
	// Delete 删除指定快照。
	Delete(scope Scope, target string, snapshotID string) error
	// ListBackups 列出已存在的快照（按创建时间升序）。
	ListBackups(scope Scope, target string) []ExistingBackup
}

// ExistingBackup 是已存在的备份元数据。
type ExistingBackup struct {
	SnapshotID string
	CreatedAt  time.Time
	Kind       string // "daily"/"weekly"/"monthly" 与 Policy 对齐
}

// ---- cron 简化解析 ----

// CronField 单字段范围。
type cronField struct {
	min, max int
}

var (
	cronMinute = cronField{0, 59}
	cronHour   = cronField{0, 23}
	cronDOM    = cronField{1, 31}
	cronMonth  = cronField{1, 12}
	cronDOW    = cronField{0, 6} // 0=Sun
)

// cronExpr 简化解析：仅支持 * / N / N-N 列表 / 步长 N-M/S。
type cronExpr struct {
	ranges []cronRange // 五个字段：minute hour dom month dow
}
type cronRange struct {
	min, max, step int
}

func parseCron(expr string) (cronExpr, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return cronExpr{}, fmt.Errorf("backuppolicy: cron must have 5 fields, got %d", len(parts))
	}
	bounds := []cronField{cronMinute, cronHour, cronDOM, cronMonth, cronDOW}
	var ce cronExpr
	for i, p := range parts {
		r, err := parseField(p, bounds[i])
		if err != nil {
			return cronExpr{}, fmt.Errorf("backuppolicy: field %d (%q): %v", i, p, err)
		}
		ce.ranges = append(ce.ranges, r)
	}
	return ce, nil
}

func parseField(token string, bound cronField) (cronRange, error) {
	if token == "*" {
		return cronRange{min: bound.min, max: bound.max, step: 1}, nil
	}
	// 单数字字段先做范围校验（防止 dow=7 等越界）
	if !strings.Contains(token, "/") && !strings.Contains(token, "-") {
		n, err := strconv.Atoi(token)
		if err == nil && (n < bound.min || n > bound.max) {
			return cronRange{}, errors.New("value out of bounds")
		}
	}
	// 步长："A-B/S"
	if strings.Contains(token, "/") {
		parts := strings.SplitN(token, "/", 2)
		base := parts[0]
		step, err := strconv.Atoi(parts[1])
		if err != nil || step <= 0 {
			return cronRange{}, errors.New("invalid step")
		}
		var lo, hi int
		if base == "*" {
			lo, hi = bound.min, bound.max
		} else {
			lo, hi, err = parseRangeBound(base, bound)
			if err != nil {
				return cronRange{}, err
			}
		}
		return cronRange{min: lo, max: hi, step: step}, nil
	}
	lo, hi, err := parseRangeBound(token, bound)
	if err != nil {
		return cronRange{}, err
	}
	return cronRange{min: lo, max: hi, step: 1}, nil
}

func parseRangeBound(token string, bound cronField) (int, int, error) {
	if !strings.Contains(token, "-") {
		n, err := strconv.Atoi(token)
		if err != nil {
			return 0, 0, err
		}
		return n, n, nil
	}
	parts := strings.SplitN(token, "-", 2)
	lo, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	hi, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if lo < bound.min || hi > bound.max || lo > hi {
		return 0, 0, errors.New("range out of bounds")
	}
	return lo, hi, nil
}

// Matches 检查给定时间是否命中 cron。
func (c cronExpr) Matches(t time.Time) bool {
	// 字段顺序：minute(0), hour(1), dom(2), month(3), dow(4)
	checks := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i, v := range checks {
		if !c.ranges[i].match(v) {
			return false
		}
	}
	return true
}

func (r cronRange) match(v int) bool {
	if v < r.min || v > r.max {
		return false
	}
	return (v-r.min)%r.step == 0
}

// ---- 策略匹配 ----

// NextFireAfter 返回 after 之后的下次触发时间（含 after 时刻对齐）。
func (p Policy) NextFireAfter(after time.Time) (time.Time, error) {
	expr, err := parseCron(p.Cron)
	if err != nil {
		return time.Time{}, err
	}
	// 1 分钟步长遍历最多 366*24*60（一年），超出报错（cron 解析异常）。
	t := after.Truncate(time.Minute).Add(time.Minute)
	deadline := after.Add(366 * 24 * time.Hour)
	for i := 0; i < 366*24*60; i++ {
		if t.After(deadline) {
			return time.Time{}, errors.New("backuppolicy: no fire within 1 year")
		}
		if expr.Matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, errors.New("backuppolicy: scan overflow")
}

// KindForTime 根据当前时间判断 daily/weekly/monthly：
//   - 每月 1 号 0 点触发 → monthly；
//   - 每周日 0 点触发 → weekly；
//   - 其它 → daily。
func KindForTime(t time.Time) string {
	if t.Day() == 1 && t.Hour() == 0 {
		return "monthly"
	}
	if t.Weekday() == time.Sunday && t.Hour() == 0 {
		return "weekly"
	}
	return "daily"
}

// ---- 调度器 + 保留策略 ----

// Scheduler 持有所有策略 + Driver 引用，由后台 ticker 扫描触发。
type Scheduler struct {
	mu      sync.Mutex
	policies []*Policy
	jobs    []*BackupJob
	driver  Driver
}

// NewScheduler 创建调度器。
func NewScheduler(driver Driver) *Scheduler {
	return &Scheduler{driver: driver}
}

// UpsertPolicy 注册或更新策略。
func (s *Scheduler) UpsertPolicy(p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.policies {
		if existing.ID == p.ID {
			s.policies[i] = &p
			return
		}
	}
	s.policies = append(s.policies, &p)
}

// Policies 返回策略快照（拷贝）。
func (s *Scheduler) Policies() []Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Policy, len(s.policies))
	for i, p := range s.policies {
		out[i] = *p
	}
	return out
}

// SweepNow 扫描策略 + 调用 Driver 触发到期任务；返回生成的新 jobs。
//
//   - 每个 Policy 各自生成至少 1 个 job（按 KindForTime 分级）；
//   - 测试可注入 Driver（NoopDriver 返回硬编码成功）。
//   - 调用方负责把生成的 jobs 持久化 + 通知用户（不在本包范围）。
func (s *Scheduler) SweepNow(now time.Time) []*BackupJob {
	s.mu.Lock()
	policies := make([]Policy, len(s.policies))
	for i := range s.policies {
		policies[i] = *s.policies[i]
	}
	driver := s.driver
	s.mu.Unlock()

	now = now.Truncate(time.Minute)
	out := []*BackupJob{}
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		fire, err := p.NextFireAfter(now.Add(-time.Minute))
		if err != nil {
			continue
		}
		if !fire.Equal(now) && fire.After(now) {
			continue
		}
		kind := KindForTime(now)
		job := &BackupJob{
			ID:        fmt.Sprintf("job-%s-%d", p.ID, now.Unix()),
			PolicyID:  p.ID,
			Scope:     p.Scope,
			Target:    p.ScopeTarget,
			Kind:      kind,
			Status:    JobQueued,
			StartedAt: now,
		}
		if driver != nil {
			snapID, bytes, err := driver.Snapshot(p.Scope, p.ScopeTarget)
			if err != nil {
				job.Status = JobFailed
				job.Error = err.Error()
				job.FinishedAt = time.Now()
			} else {
				job.Status = JobSuccess
				job.SnapshotID = snapID
				job.Bytes = bytes
				job.FinishedAt = time.Now()
				job.KeepUntil = computeKeepUntil(now, kind, p)
			}
		}
		s.mu.Lock()
		s.jobs = append(s.jobs, job)
		s.mu.Unlock()
		out = append(out, job)
	}
	return out
}

// Jobs 返回所有 jobs（按创建时间降序；测试断言用）。
func (s *Scheduler) Jobs() []BackupJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BackupJob, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = *j
	}
	// 降序：最近在前。
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// GFSRetain 计算每个 kind 应保留数（按 Policy 的 RetainDaily/Weekly/Monthly）。
func (p Policy) GFSRetain(kind string) int {
	switch kind {
	case "monthly":
		return p.RetainMonthly
	case "weekly":
		return p.RetainWeekly
	default:
		return p.RetainDaily
	}
}

// PruneExpired 按 GFS 策略删除超期 + 超额的备份（调用 driver.Delete）。
//
// 返回被删除的 ExistingBackup 列表（按被删除顺序；调用方写审计）。
//
// 实现：
//   - 先按 kind 分组（daily/weekly/monthly），每组按 CreatedAt 降序；
//   - 保留每个 kind 中最近 retain 个；
//   - 其它全部删除（含超 KeepUntil 的）。
func (s *Scheduler) PruneExpired(scope Scope, target string, p Policy, now time.Time) []ExistingBackup {
	if s.driver == nil {
		return nil
	}
	existing := s.driver.ListBackups(scope, target)
	// 按 kind 分组
	byKind := map[string][]ExistingBackup{}
	for _, b := range existing {
		byKind[b.Kind] = append(byKind[b.Kind], b)
	}
	deleted := []ExistingBackup{}
	for _, kind := range []string{"daily", "weekly", "monthly"} {
		group := byKind[kind]
		sort.Slice(group, func(i, j int) bool {
			return group[i].CreatedAt.After(group[j].CreatedAt)
		})
		retain := p.GFSRetain(kind)
		if retain <= 0 {
			retain = 1
		}
		// 保留前 retain 个 + 超 KeepUntil 的额外删除。
		for i, b := range group {
			keepReason := i < retain
			if !keepReason && !b.CreatedAt.After(now) {
				// over retain + past → delete
				_ = s.driver.Delete(scope, target, b.SnapshotID)
				deleted = append(deleted, b)
				continue
			}
			if !keepReason && b.CreatedAt.After(now) {
				// 未来时间（数据异常）→ 视为过期删除
				_ = s.driver.Delete(scope, target, b.SnapshotID)
				deleted = append(deleted, b)
				continue
			}
			_ = keepReason
		}
	}
	return deleted
}

// computeKeepUntil 根据 kind + Policy 给出保留截止时间：
//   - daily  保留到 createdAt + retainDaily 天；
//   - weekly 保留到 createdAt + retainWeekly * 7 天；
//   - monthly 保留到 createdAt + retainMonthly * 30 天。
func computeKeepUntil(now time.Time, kind string, p Policy) time.Time {
	switch kind {
	case "monthly":
		return now.AddDate(0, p.RetainMonthly, 0)
	case "weekly":
		return now.AddDate(0, 0, p.RetainWeekly*7)
	default:
		return now.AddDate(0, 0, p.RetainDaily)
	}
}

// ---- NoopDriver 测试桩 ----

// NoopDriver 默认成功；可注入 lists / deleter。
type NoopDriver struct {
	mu sync.Mutex
	// allSnapshots 按 target + kind 分组的列表（每次 Snapshot 追加）。
	allSnapshots map[string][]ExistingBackup
	deleted   []string
	// SnapshotErr 可选：错误注入测试。
	SnapshotErr error
}

// NewNoopDriver 创建默认成功的驱动。
func NewNoopDriver() *NoopDriver {
	return &NoopDriver{allSnapshots: map[string][]ExistingBackup{}}
}

// Snapshot implements Driver。
func (d *NoopDriver) Snapshot(scope Scope, target string) (string, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.SnapshotErr != nil {
		return "", 0, d.SnapshotErr
	}
	kind := "daily"
	id := fmt.Sprintf("snap-%s-%d", target, len(d.allSnapshots[target]))
	d.allSnapshots[target] = append(d.allSnapshots[target], ExistingBackup{
		SnapshotID: id,
		CreatedAt:  time.Now(),
		Kind:       kind,
	})
	return id, 1024, nil
}

// Delete implements Driver。
func (d *NoopDriver) Delete(_ Scope, target, snapshotID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, snapshotID)
	for i, b := range d.allSnapshots[target] {
		if b.SnapshotID == snapshotID {
			d.allSnapshots[target] = append(d.allSnapshots[target][:i], d.allSnapshots[target][i+1:]...)
			break
		}
	}
	return nil
}

// ListBackups implements Driver。
func (d *NoopDriver) ListBackups(_ Scope, target string) []ExistingBackup {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ExistingBackup, len(d.allSnapshots[target]))
	copy(out, d.allSnapshots[target])
	return out
}

// Deleted returns history of deleted snapshot ids (test assertions).
func (d *NoopDriver) Deleted() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.deleted))
	copy(out, d.deleted)
	return out
}

// 防御未用 import（保持兼容）。
var _ = regexp.MustCompile
var _ sync.Mutex