package api

// apiv2_backup_plans.go —— API v2：备份计划（定时备份策略）管理。
//
// 端点：
//
//	GET    /api/v2/backup-plans              计划列表（分页 + 实例过滤 + summary）
//	POST   /api/v2/backup-plans              创建计划
//	GET    /api/v2/backup-plans/{id}         计划详情（含最近运行记录）
//	PATCH  /api/v2/backup-plans/{id}         修改计划（名称/表达式/启停/保留份数）
//	DELETE /api/v2/backup-plans/{id}         删除计划
//	POST   /api/v2/backup-plans/{id}/run     立即执行一次
//
// 复用 v1 的纯函数（buildBackupPlan / newBackupPlanID / backupPlanNextFire），
// 校验规则与面板完全一致（cron 表达式、保留份数、实例存在性）；错误统一走 v2 信封。

import (
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

func init() {
	registerV2("GET /api/v2/backup-plans", v2Auth(v2BackupPlansList))
	registerV2("POST /api/v2/backup-plans", v2Auth(v2BackupPlanCreate))
	registerV2("GET /api/v2/backup-plans/{id}", v2Auth(v2BackupPlanGet))
	registerV2("PATCH /api/v2/backup-plans/{id}", v2Auth(v2BackupPlanUpdate))
	registerV2("DELETE /api/v2/backup-plans/{id}", v2Auth(v2BackupPlanDelete))
	registerV2("POST /api/v2/backup-plans/{id}/run", v2Auth(v2BackupPlanRun))
}

// v2BackupPlanView 计划对外契约。
func v2BackupPlanView(plan config.BackupPlan) map[string]interface{} {
	instanceName := "全部实例"
	if plan.ContainerID != 0 {
		instanceName = ""
		if c := config.FindContainer(plan.ContainerID); c != nil {
			instanceName = c.Name
		}
	}
	runs := plan.Runs
	if runs == nil {
		runs = []config.BackupPlanRun{}
	}
	return map[string]interface{}{
		"id":            plan.ID,
		"name":          plan.Name,
		"instance_id":   plan.ContainerID,
		"instance_name": instanceName,
		"cron":          plan.Cron,
		"enabled":       plan.Enabled,
		"keep":          plan.Keep,
		"created_at":    v2Time(plan.CreatedAt),
		"last_run_at":   v2Time(plan.LastRunAt),
		"next_run_at":   v2Time(plan.NextRunAt),
		"last_status":   plan.LastStatus,
		"last_error":    plan.LastError,
		"runs":          runs,
	}
}

// v2BackupPlanVisible 受限调用方（子用户 / 容器绑定密钥）只能看到绑定容器范围内的计划；
// 全局计划（container_id=0）对其不可见。
func v2BackupPlanVisible(r *http.Request, containerID int) bool {
	if containerID == 0 {
		_, restricted := requestAllowedContainers(r)
		return !restricted
	}
	c := config.FindContainer(containerID)
	if c == nil {
		return false
	}
	return isContainerAllowedForRequest(r, c.UUID)
}

func v2BackupPlansList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:read") {
		return
	}
	query := v2ParsePage(r)
	instanceFilter := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	plans := config.BackupPlans()
	items := make([]map[string]interface{}, 0, len(plans))
	enabledCount := 0
	for _, plan := range plans {
		if !v2BackupPlanVisible(r, plan.ContainerID) {
			continue
		}
		if instanceFilter != "" {
			c := config.FindContainer(plan.ContainerID)
			matched := (c != nil && (c.Name == instanceFilter || intToStrV2(c.ID) == instanceFilter)) ||
				(plan.ContainerID == 0 && instanceFilter == "all")
			if !matched {
				continue
			}
		}
		if plan.Enabled {
			enabledCount++
		}
		items = append(items, v2BackupPlanView(plan))
	}
	summary := map[string]int{"total": len(items), "enabled": enabledCount, "disabled": len(items) - enabledCount}
	total := len(items)
	start, end := query.Slice(total)
	v2ListWithSummary(w, r, items[start:end], query, total, summary)
}

// v2BackupPlanRequest 创建/修改请求体（与 v1 字段一致，便于前端复用）。
type v2BackupPlanRequest struct {
	Name        *string `json:"name"`
	InstanceID  *int    `json:"instance_id"`
	Cron        *string `json:"cron"`
	Enabled     *bool   `json:"enabled"`
	Keep        *int    `json:"keep"`
}

func v2BackupPlanCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	var req v2BackupPlanRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	planReq := backupPlanRequest{}
	if req.Name != nil {
		planReq.Name = strings.TrimSpace(*req.Name)
	}
	if req.InstanceID != nil {
		planReq.ContainerID = *req.InstanceID
	}
	if req.Cron != nil {
		planReq.Cron = strings.TrimSpace(*req.Cron)
	}
	planReq.Enabled = req.Enabled
	if req.Keep != nil {
		planReq.Keep = *req.Keep
	}
	if planReq.ContainerID != 0 && !isContainerAllowedForRequest(r, intToIdentifier(planReq.ContainerID)) {
		v2Forbidden(w, r, "无权为该实例创建备份计划")
		return
	}
	plan, err := buildBackupPlan(planReq, nil)
	if err != nil {
		v2BadRequest(w, r, err.Error(), nil)
		return
	}
	plan.ID = newBackupPlanID()
	plan.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	if next, ferr := backupPlanNextFire(plan.Cron, time.Now()); ferr == nil {
		plan.NextRunAt = next
	}
	stored, added := config.AddBackupPlan(*plan)
	if !added {
		v2Conflict(w, r, "备份计划已存在")
		return
	}
	auditRequest(r, "api.v2.backup_plan.create", stored.ID,
		"instance_id="+intToStrV2(stored.ContainerID)+" cron="+stored.Cron, true, "")
	v2Created(w, r, v2BackupPlanView(stored))
}

func v2BackupPlanGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:read") {
		return
	}
	planID := strings.TrimSpace(r.PathValue("id"))
	plan := config.FindBackupPlan(planID)
	if plan == nil {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	if !v2BackupPlanVisible(r, plan.ContainerID) {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	v2OK(w, r, v2BackupPlanView(*plan))
}

func v2BackupPlanUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:schedule") && !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	planID := strings.TrimSpace(r.PathValue("id"))
	existing := config.FindBackupPlan(planID)
	if existing == nil {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	if !v2BackupPlanVisible(r, existing.ContainerID) {
		v2Forbidden(w, r, "无权修改该备份计划")
		return
	}
	var req v2BackupPlanRequest
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	// 以现有值为基线，仅覆盖传入字段（PATCH 语义）。
	planReq := backupPlanRequest{
		Name:        existing.Name,
		ContainerID: existing.ContainerID,
		Cron:        existing.Cron,
		Keep:        existing.Keep,
		Enabled:     &existing.Enabled,
	}
	if req.Name != nil {
		planReq.Name = strings.TrimSpace(*req.Name)
	}
	if req.InstanceID != nil {
		if *req.InstanceID != 0 && !isContainerAllowedForRequest(r, intToIdentifier(*req.InstanceID)) {
			v2Forbidden(w, r, "无权把计划改到该实例")
			return
		}
		planReq.ContainerID = *req.InstanceID
	}
	if req.Cron != nil {
		planReq.Cron = strings.TrimSpace(*req.Cron)
	}
	if req.Keep != nil {
		planReq.Keep = *req.Keep
	}
	if req.Enabled != nil {
		planReq.Enabled = req.Enabled
	}
	plan, err := buildBackupPlan(planReq, existing)
	if err != nil {
		v2BadRequest(w, r, err.Error(), nil)
		return
	}
	plan.ID = existing.ID
	plan.CreatedAt = existing.CreatedAt
	if next, ferr := backupPlanNextFire(plan.Cron, time.Now()); ferr == nil {
		plan.NextRunAt = next
	}
	if !config.UpdateBackupPlan(*plan) {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	auditRequest(r, "api.v2.backup_plan.update", planID, "cron="+plan.Cron, true, "")
	updated := config.FindBackupPlan(planID)
	if updated == nil {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	v2OK(w, r, v2BackupPlanView(*updated))
}

func v2BackupPlanDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	planID := strings.TrimSpace(r.PathValue("id"))
	existing := config.FindBackupPlan(planID)
	if existing == nil {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	if !v2BackupPlanVisible(r, existing.ContainerID) {
		v2Forbidden(w, r, "无权删除该备份计划")
		return
	}
	if !config.RemoveBackupPlan(planID) {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	auditRequest(r, "api.v2.backup_plan.delete", planID, "", true, "")
	v2NoContent(w, r)
}

// v2BackupPlanRun POST /backup-plans/{id}/run：立即执行一次（异步，返回任务受理）。
func v2BackupPlanRun(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:create") {
		return
	}
	planID := strings.TrimSpace(r.PathValue("id"))
	plan := config.FindBackupPlan(planID)
	if plan == nil {
		v2NotFound(w, r, "备份计划不存在："+planID)
		return
	}
	if !v2BackupPlanVisible(r, plan.ContainerID) {
		v2Forbidden(w, r, "无权执行该备份计划")
		return
	}
	if plan.ContainerID != 0 {
		if _, err := createBackupForContainer(plan.ContainerID, v2AuthContext(r).Username); err != nil {
			v2Upstream(w, r, "执行备份失败："+err.Error())
			return
		}
	}
	run := config.BackupPlanRun{At: time.Now().Format("2006-01-02 15:04:05"), Status: "success", Backups: 1}
	if next, ferr := backupPlanNextFire(plan.Cron, time.Now()); ferr == nil {
		config.RecordBackupPlanRun(planID, run, next)
	} else {
		config.RecordBackupPlanRun(planID, run, "")
	}
	auditRequest(r, "api.v2.backup_plan.run", planID, "手动触发", true, "")
	v2Accepted(w, r, map[string]interface{}{
		"id": planID, "triggered": true, "instance_id": plan.ContainerID,
	})
}

// intToIdentifier 把容器数字 ID 映射为 UUID（容器绑定校验用 UUID）。
func intToIdentifier(id int) string {
	if id == 0 {
		return ""
	}
	if c := config.FindContainer(id); c != nil {
		return c.UUID
	}
	return ""
}

func intToStrV2(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
