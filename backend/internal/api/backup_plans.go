package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/backuppolicy"
	"eyvescloud/internal/config"
)

// 备份计划（定时备份）：
//
//   - 每个计划 = 目标容器（0 表示全部运行中容器）+ cron + 保留份数 + 启用开关；
//   - 调度器每分钟扫描一次，到期后为每个目标容器创建一份磁盘备份（复用
//     createInstanceBackup 的快照→归档链路），并按 keep 做 keep-N 轮换；
//   - cron 解析复用 backuppolicy 引擎（5 字段：分 时 日 月 周），避免重复造轮子；
//   - 运行结果（成功/失败份数、耗时、错误）写回计划，供管理页回看。
//
// 说明：全局的 InstanceBackupSettings 仍保留（对所有容器按固定周期备份）；
// 备份计划用于更精细的按目标 / 按 cron 调度，两者互不影响。

const (
	backupPlanSchedulerTick = time.Minute
	defaultBackupPlanKeep   = 7
	maxBackupPlanKeep       = 365
)

var (
	backupPlanSchedulerOnce sync.Once
	// backupPlanRunning 标记正在运行的计划，避免调度与「立即运行」重复触发。
	backupPlanRunningMu sync.Mutex
	backupPlanRunning   = map[string]bool{}
)

// StartBackupPlanScheduler 启动备份计划调度器（每分钟检查一次）。
func StartBackupPlanScheduler() {
	backupPlanSchedulerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(backupPlanSchedulerTick)
			defer ticker.Stop()
			for range ticker.C {
				backupPlanTick()
			}
		}()
	})
}

// backupPlanTick 扫描全部计划，执行到期的计划（每个计划独立 goroutine）。
func backupPlanTick() {
	if !maintenanceLeaseActive() {
		return
	}
	now := time.Now()
	for _, plan := range config.BackupPlans() {
		if !plan.Enabled {
			continue
		}
		next, ok := parseBackupPlanTime(plan.NextRunAt)
		if !ok {
			// 首次或时间非法：重新计算下次运行时间，本轮不执行。
			if nf, err := backupPlanNextFire(plan.Cron, now); err == nil {
				config.SetBackupPlanNextRun(plan.ID, nf)
			}
			continue
		}
		if now.Before(next) {
			continue
		}
		if !claimBackupPlanRun(plan.ID) {
			continue
		}
		// 先推进下次运行时间再执行，避免备份耗时较长导致下轮重复触发。
		nextFire, err := backupPlanNextFire(plan.Cron, now)
		if err != nil {
			nextFire = ""
		}
		config.SetBackupPlanNextRun(plan.ID, nextFire)
		go func(p config.BackupPlan) {
			defer releaseBackupPlanRun(p.ID)
			run := executeBackupPlan(p)
			config.RecordBackupPlanRun(p.ID, run, "")
		}(plan)
	}
}

// backupPlanNextFire 计算 cron 在 from 之后的下次触发时间（本地时间字符串）。
func backupPlanNextFire(cron string, from time.Time) (string, error) {
	policy := backuppolicy.Policy{Cron: strings.TrimSpace(cron)}
	fire, err := policy.NextFireAfter(from)
	if err != nil {
		return "", err
	}
	return fire.Format("2006-01-02 15:04:05"), nil
}

// validateBackupPlanCron 校验 cron 是否可解析。
func validateBackupPlanCron(cron string) error {
	policy := backuppolicy.Policy{Cron: strings.TrimSpace(cron)}
	if _, err := policy.NextFireAfter(time.Now()); err != nil {
		return fmt.Errorf("cron 表达式无效（需为 5 字段：分 时 日 月 周）: %v", err)
	}
	return nil
}

// parseBackupPlanTime 解析计划时间字符串；空或非法返回 ok=false。
func parseBackupPlanTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func claimBackupPlanRun(id string) bool {
	backupPlanRunningMu.Lock()
	defer backupPlanRunningMu.Unlock()
	if backupPlanRunning[id] {
		return false
	}
	backupPlanRunning[id] = true
	return true
}

func releaseBackupPlanRun(id string) {
	backupPlanRunningMu.Lock()
	delete(backupPlanRunning, id)
	backupPlanRunningMu.Unlock()
}

// executeBackupPlan 执行一次计划：为所有目标容器创建备份，汇总结果。
func executeBackupPlan(plan config.BackupPlan) config.BackupPlanRun {
	start := time.Now()
	run := config.BackupPlanRun{At: start.Format("2006-01-02 15:04:05"), Status: "success"}

	targets := planTargets(plan)
	if len(targets) == 0 {
		run.Status = "failed"
		run.Error = "没有可备份的运行中容器"
		run.DurationMs = time.Since(start).Milliseconds()
		return run
	}

	keep := plan.Keep
	if keep < 1 {
		keep = defaultBackupPlanKeep
	}
	for _, c := range targets {
		if _, err := createInstanceBackup(c.ID, "backup-plan:"+plan.ID, keep, true); err != nil {
			run.Failed++
			if run.Error == "" {
				run.Error = fmt.Sprintf("%s: %v", c.Name, err)
			}
			continue
		}
		run.Backups++
	}

	switch {
	case run.Failed == 0:
		run.Status = "success"
	case run.Backups == 0:
		run.Status = "failed"
	default:
		run.Status = "partial"
	}
	run.DurationMs = time.Since(start).Milliseconds()
	return run
}

// planTargets 解析计划的作用目标：指定容器或全部运行中容器。
func planTargets(plan config.BackupPlan) []config.Container {
	if plan.ContainerID > 0 {
		c := config.FindContainer(plan.ContainerID)
		if c == nil {
			return nil
		}
		return []config.Container{*c}
	}
	targets := make([]config.Container, 0)
	for _, c := range config.GetContainers() {
		if c.Status == "running" {
			targets = append(targets, c)
		}
	}
	return targets
}

// ---- HTTP handlers ----

// backupPlanAllowedForRequest 校验受限请求（子用户/绑定容器的 API Key）能否
// 访问目标备份计划（F6：backup-plans 补归属校验，与容器其他操作对齐）。
// container_id=0（作用全部容器）对受限请求一律拒绝——其影响面必然越过绑定范围。
func backupPlanAllowedForRequest(r *http.Request, containerID int) bool {
	allowed, restricted := requestAllowedContainers(r)
	if !restricted {
		return true
	}
	if containerID <= 0 {
		return false
	}
	c := config.FindContainer(containerID)
	if c == nil {
		return false
	}
	return isContainerAllowed(allowed, c)
}

// requireBackupPlanAccess 在归属校验失败时写 403 并返回 false。
func requireBackupPlanAccess(w http.ResponseWriter, r *http.Request, containerID int) bool {
	if !backupPlanAllowedForRequest(r, containerID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return false
	}
	return true
}

// HandleBackupPlans 处理 /api[/v1]/backup-plans：GET 列表、POST 新建。
func HandleBackupPlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "backup:read") {
			return
		}
		plans := config.BackupPlans()
		// F6：受限请求只能看到绑定容器范围内的计划（全局计划隐藏）。
		if _, restricted := requestAllowedContainers(r); restricted {
			filtered := make([]config.BackupPlan, 0, len(plans))
			for _, p := range plans {
				if backupPlanAllowedForRequest(r, p.ContainerID) {
					filtered = append(filtered, p)
				}
			}
			plans = filtered
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: plans})
	case http.MethodPost:
		if !requireScope(w, r, "backup:write") {
			return
		}
		createBackupPlan(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleBackupPlanSubRoutes 分发 /api[/v1]/backup-plans/{id}[/run]。
func HandleBackupPlanSubRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/backup-plans/")
	rest = strings.TrimPrefix(rest, "/api/backup-plans/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	// POST {id}/run → 立即运行一次。
	if r.Method == http.MethodPost && strings.HasSuffix(rest, "/run") {
		id := strings.Trim(strings.TrimSuffix(rest, "/run"), "/")
		if id == "" || strings.Contains(id, "/") {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
			return
		}
		runBackupPlanNow(w, r, id)
		return
	}
	if strings.Contains(rest, "/") {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan action not found"})
		return
	}

	id := rest
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "backup:read") {
			return
		}
		plan := config.FindBackupPlan(id)
		if plan == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
			return
		}
		// F6：归属校验（受限请求不能读非绑定容器/全局计划）
		if !requireBackupPlanAccess(w, r, plan.ContainerID) {
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: plan})
	case http.MethodPut:
		if !requireScope(w, r, "backup:write") {
			return
		}
		updateBackupPlan(w, r, id)
	case http.MethodDelete:
		if !requireScope(w, r, "backup:write") {
			return
		}
		// F6：删除前先做归属校验，不能只凭 plan ID 删
		existing := config.FindBackupPlan(id)
		if existing == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
			return
		}
		if !requireBackupPlanAccess(w, r, existing.ContainerID) {
			return
		}
		if !config.RemoveBackupPlan(id) {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
			return
		}
		auditRequest(r, "backup_plan.delete", id, "", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Backup plan deleted"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// backupPlanRequest 是新建/更新计划的请求体。
type backupPlanRequest struct {
	Name        string `json:"name"`
	ContainerID int    `json:"container_id"`
	Cron        string `json:"cron"`
	Enabled     *bool  `json:"enabled"`
	Keep        int    `json:"keep"`
}

func createBackupPlan(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBackupPlanRequest(w, r)
	if !ok {
		return
	}
	// F6：受限请求只能为绑定容器建计划（container_id=0 全局计划拒绝）
	if !requireBackupPlanAccess(w, r, req.ContainerID) {
		return
	}
	plan, err := buildBackupPlan(req, nil)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	plan.ID = newBackupPlanID()
	plan.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	if nf, ferr := backupPlanNextFire(plan.Cron, time.Now()); ferr == nil {
		plan.NextRunAt = nf
	}
	stored, added := config.AddBackupPlan(*plan)
	if !added {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Backup plan already exists"})
		return
	}
	auditRequest(r, "backup_plan.create", stored.ID,
		fmt.Sprintf("container_id=%d cron=%s keep=%d", stored.ContainerID, stored.Cron, stored.Keep), true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: stored})
}

func updateBackupPlan(w http.ResponseWriter, r *http.Request, id string) {
	existing := config.FindBackupPlan(id)
	if existing == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
		return
	}
	// F6：既有计划与目标容器双重归属校验（防受限 Key 借更新改挂他人容器）
	if !requireBackupPlanAccess(w, r, existing.ContainerID) {
		return
	}
	req, ok := decodeBackupPlanRequest(w, r)
	if !ok {
		return
	}
	if !requireBackupPlanAccess(w, r, req.ContainerID) {
		return
	}
	plan, err := buildBackupPlan(req, existing)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	plan.ID = existing.ID
	plan.CreatedAt = existing.CreatedAt
	// cron 变化或下次运行时间缺失时重算，保证调度不漂移。
	if plan.Cron != existing.Cron || plan.NextRunAt == "" {
		if nf, ferr := backupPlanNextFire(plan.Cron, time.Now()); ferr == nil {
			plan.NextRunAt = nf
		}
	}
	if !config.UpdateBackupPlan(*plan) {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
		return
	}
	auditRequest(r, "backup_plan.update", plan.ID,
		fmt.Sprintf("container_id=%d cron=%s keep=%d enabled=%v", plan.ContainerID, plan.Cron, plan.Keep, plan.Enabled), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: plan})
}

func runBackupPlanNow(w http.ResponseWriter, r *http.Request, id string) {
	if !requireScope(w, r, "backup:write") {
		return
	}
	plan := config.FindBackupPlan(id)
	if plan == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Plan not found"})
		return
	}
	// F6：立即执行同样过归属校验
	if !requireBackupPlanAccess(w, r, plan.ContainerID) {
		return
	}
	if !claimBackupPlanRun(plan.ID) {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "计划正在运行中，请稍后再试"})
		return
	}
	auditRequest(r, "backup_plan.run", plan.ID, "manual", true, "")
	go func(p config.BackupPlan) {
		defer releaseBackupPlanRun(p.ID)
		run := executeBackupPlan(p)
		config.RecordBackupPlanRun(p.ID, run, "")
	}(*plan)
	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "备份计划已开始执行"})
}

func decodeBackupPlanRequest(w http.ResponseWriter, r *http.Request) (backupPlanRequest, bool) {
	var req backupPlanRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return req, false
		}
	}
	return req, true
}

// buildBackupPlan 校验并构造计划；existing 非空时表示更新（保留运行历史）。
func buildBackupPlan(req backupPlanRequest, existing *config.BackupPlan) (*config.BackupPlan, error) {
	cron := strings.TrimSpace(req.Cron)
	if cron == "" {
		return nil, fmt.Errorf("cron 不能为空")
	}
	if err := validateBackupPlanCron(cron); err != nil {
		return nil, err
	}
	if req.ContainerID < 0 {
		return nil, fmt.Errorf("container_id 非法")
	}
	if req.ContainerID > 0 && config.FindContainer(req.ContainerID) == nil {
		return nil, fmt.Errorf("容器不存在: %d", req.ContainerID)
	}
	keep := req.Keep
	if keep <= 0 {
		keep = defaultBackupPlanKeep
	}
	if keep > maxBackupPlanKeep {
		keep = maxBackupPlanKeep
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if req.ContainerID > 0 {
			name = fmt.Sprintf("容器 %d 备份计划", req.ContainerID)
		} else {
			name = "全部容器备份计划"
		}
	}

	// enabled 缺省时：新建默认启用，更新则保持原状态（避免漏传字段把已停用计划重新打开）。
	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	plan := &config.BackupPlan{
		Name:        name,
		ContainerID: req.ContainerID,
		Cron:        cron,
		Keep:        keep,
		Enabled:     enabled,
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if existing != nil {
		plan.Runs = existing.Runs
		plan.LastRunAt = existing.LastRunAt
		plan.LastStatus = existing.LastStatus
		plan.LastError = existing.LastError
		plan.NextRunAt = existing.NextRunAt
	}
	return plan, nil
}

// newBackupPlanID 生成唯一计划 ID。
func newBackupPlanID() string {
	return "bp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}