package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// HandleTaskHistory 返回任务历史（终态留档）分页列表：GET /api[/v1]/tasks/history。
func HandleTaskHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "task:read") {
		return
	}

	query := r.URL.Query()
	statuses := splitCommaList(query.Get("status"))
	taskType := strings.TrimSpace(query.Get("type"))
	// 子用户只能看自己的任务：把过滤条件限定为其 actor 值（与 Task.User 写入一致）。
	userFilter := ""
	if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeSubUser {
		userFilter = subUserHistoryUser(ctx)
	}

	page := parseHistoryInt(query.Get("page"), 1)
	pageSize := parseHistoryInt(query.Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	items, total, err := config.ListTaskHistory(statuses, taskType, userFilter, pageSize, offset)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "查询任务历史失败: " + err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     items,
		"timenow":   time.Now().UTC().Format(time.RFC3339),
	}})
}

// HandleTaskDetail 返回单个任务的详情与日志：GET /api[/v1]/tasks/{id}。
// 优先命中内存中的实时任务；否则回退到历史留档。
func HandleTaskDetail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "task:read") {
		return
	}
	id = strings.Trim(strings.TrimSpace(id), "/")
	if id == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Task not found"})
		return
	}

	// 内存实时任务优先：包含进度、阶段等运行中信息。
	for _, task := range globalQueue.GetTasks() {
		if task.ID != id {
			continue
		}
		if !isTaskAllowedForRequest(r, task) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this task"})
			return
		}
		logs, _ := config.ListTaskLogs(id)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"task": task,
			"logs": logs,
			"live": true,
		}})
		return
	}

	entry, found, err := config.GetTaskHistory(id)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "查询任务历史失败: " + err.Error()})
		return
	}
	if !found {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Task not found"})
		return
	}
	if !isTaskHistoryAllowedForRequest(r, entry) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this task"})
		return
	}
	logs, _ := config.ListTaskLogs(id)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"task": entry,
		"logs": logs,
		"live": false,
	}})
}

// HandleTaskStats 返回任务统计：历史统计 + 内存队列实时信息。仅管理员可访问。
func HandleTaskStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireTaskAdmin(w, r) {
		return
	}
	history, err := config.TaskHistoryStats()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "查询任务统计失败: " + err.Error()})
		return
	}
	live := map[string]interface{}{}
	if globalQueue != nil {
		settings := globalQueue.Settings()
		live["concurrency"] = settings.Concurrency
		live["active"] = settings.Active
		live["pending"] = settings.Pending
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"history": history,
		"live":    live,
	}})
}

// HandleTaskCancel 取消一个排队中或运行中的任务：POST /api[/v1]/tasks/{id}/cancel。仅管理员可访问。
func HandleTaskCancel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireTaskAdmin(w, r) {
		return
	}
	id = strings.Trim(strings.TrimSpace(id), "/")
	if id == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Task not found"})
		return
	}

	globalQueue.mu.Lock()
	task := globalQueue.tasks[id]
	if task == nil {
		globalQueue.mu.Unlock()
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Task not found"})
		return
	}
	if task.Status != "pending" && task.Status != "running" {
		current := task.Status
		globalQueue.mu.Unlock()
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "只有排队中或运行中的任务可以取消（当前状态: " + current + "）"})
		return
	}

	running := task.Status == "running"
	if running {
		// 运行中的任务没有强制中断能力：仅标记取消，当前步骤结束后不再推进后续流程。
		task.StageDetail = "运行中任务已标记取消，将在当前步骤结束后停止"
	} else {
		// 排队中的任务直接从调度队列移除，避免被调度器取走后执行。
		globalQueue.createQueue = removeTaskFromQueue(globalQueue.createQueue, id)
		globalQueue.opQueue = removeTaskFromQueue(globalQueue.opQueue, id)
	}
	task.Status = "cancelled"
	// 在锁内拷贝落库所需数据，锁外再写历史/日志。
	entry := config.TaskHistoryEntry{
		ID:            task.ID,
		Type:          string(task.Type),
		ContainerID:   task.ContainerID,
		ContainerName: task.ContainerName,
		Status:        "cancelled",
		Stage:         task.Stage,
		StageDetail:   task.StageDetail,
		Percent:       task.Percent,
		User:          task.User,
		IP:            task.IP,
		UserAgent:     task.UserAgent,
		CreatedAt:     task.CreatedAt,
		StartedAt:     task.StartedAt,
	}
	globalQueue.persistTasks()
	globalQueue.mu.Unlock()

	endedAt := time.Now().UTC()
	entry.EndedAt = endedAt.Format(time.RFC3339)
	if start, err := time.Parse(time.RFC3339, entry.StartedAt); err == nil {
		entry.DurationMs = endedAt.Sub(start).Milliseconds()
	}
	_ = config.UpsertTaskHistory(entry)
	logMessage := "任务已取消"
	if running {
		logMessage = "运行中任务已标记取消，将在当前步骤结束后停止"
	}
	_ = config.AppendTaskLog(id, "WARN", logMessage)

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Task cancelled", Data: map[string]interface{}{
		"id":      id,
		"status":  "cancelled",
		"running": running,
	}})
}

// HandleTaskCompat 返回 Virtualizor 风格字段视图：GET /api[/v1]/tasks/compat。仅管理员可访问。
func HandleTaskCompat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireTaskAdmin(w, r) {
		return
	}

	// 历史优先（含结束时间等更完整信息），实时任务补齐历史中尚不存在的条目，避免同 ID 重复。
	type compatEntry struct {
		item      map[string]interface{}
		createdAt string
	}
	merged := make([]compatEntry, 0, 200)
	seen := make(map[string]bool)
	if history, _, err := config.ListTaskHistory(nil, "", "", 100, 0); err == nil {
		for _, e := range history {
			seen[e.ID] = true
			merged = append(merged, compatEntry{item: compatFromHistoryEntry(e), createdAt: e.CreatedAt})
		}
	}
	for _, task := range globalQueue.GetTasks() {
		if seen[task.ID] {
			continue
		}
		merged = append(merged, compatEntry{item: compatFromTask(task), createdAt: task.CreatedAt})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].createdAt > merged[j].createdAt
	})
	if len(merged) > 200 {
		merged = merged[:200]
	}
	items := make([]map[string]interface{}, 0, len(merged))
	for _, m := range merged {
		items = append(items, m.item)
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"tasks":   items,
		"total":   len(items),
		"timenow": time.Now().UTC().Format(time.RFC3339),
	}})
}

// HandleTaskSubRoutes 处理 /api[/v1]/tasks/{id}[...] 的统一子路由分发，
// 同时兼容 /api/tasks/ 与 /api/v1/tasks/ 两种前缀。
func HandleTaskSubRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	rest = strings.TrimPrefix(rest, "/api/tasks/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	// POST /tasks/{id}/cancel → 取消任务（管理员限定，在 handler 内校验）。
	if r.Method == http.MethodPost && strings.HasSuffix(rest, "/cancel") {
		id := strings.Trim(strings.TrimSuffix(rest, "/cancel"), "/")
		if id == "" || strings.Contains(id, "/") {
			jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
			return
		}
		HandleTaskCancel(w, r, id)
		return
	}

	// 其余动作只接受裸 {id}（rest 中不含额外路径段）。
	if strings.Contains(rest, "/") {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		HandleTaskDetail(w, r, rest)
	case http.MethodDelete:
		// 保持既有语义：删除任务需要 task:delete scope 且为管理员，
		// 具体删除逻辑复用原 HandleTaskDelete（其内部会再次校验 scope）。
		if !requireTaskAdmin(w, r) {
			return
		}
		HandleTaskDelete(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// requireTaskAdmin 要求请求来自管理员账号（拒绝子用户与普通 API Key）。
func requireTaskAdmin(w http.ResponseWriter, r *http.Request) bool {
	ctx, ok := authContextFromRequest(r)
	if !ok || ctx.Type != authTypeAdmin {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Administrator permission required"})
		return false
	}
	return true
}

// subUserHistoryUser 返回子用户历史过滤所用的 user 值。
// 子用户任务的 user 写入形如 "user:<用户名>"（见 requestActor），
// 因此优先取 actor，缺失时用用户名补出同样的前缀，保证过滤命中。
func subUserHistoryUser(ctx AuthContext) string {
	if actor := strings.TrimSpace(ctx.Actor); actor != "" {
		return actor
	}
	if username := strings.TrimSpace(ctx.Username); username != "" {
		return "user:" + username
	}
	return ""
}

// isTaskHistoryAllowedForRequest 用历史条目构造最小 Task，复用容器维度的可见性判定。
func isTaskHistoryAllowedForRequest(r *http.Request, entry config.TaskHistoryEntry) bool {
	return isTaskAllowedForRequest(r, &Task{
		ContainerID:   entry.ContainerID,
		ContainerName: entry.ContainerName,
	})
}

// removeTaskFromQueue 从切片队列中移除指定 ID 的任务，返回新切片。
func removeTaskFromQueue(queue []*Task, id string) []*Task {
	filtered := make([]*Task, 0, len(queue))
	for _, t := range queue {
		if t != nil && t.ID == id {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// splitCommaList 把 "a,b,c" 拆分为去空白的字符串切片，空输入返回 nil。
func splitCommaList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseHistoryInt 解析查询参数中的整数，缺失或非法时返回 fallback。
func parseHistoryInt(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// compatActionName 把内部任务类型映射为 Virtualizor 风格的 action 名。
func compatActionName(taskType string) string {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "create":
		return "vm.create"
	case "start":
		return "vm.start"
	case "stop":
		return "vm.stop"
	case "restart":
		return "vm.restart"
	case "delete":
		return "vm.delete"
	case "reinstall":
		return "vm.rebuild"
	default:
		return taskType
	}
}

// compatStatusNumber 把任务状态映射为 Virtualizor 风格数字状态码。
func compatStatusNumber(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "running":
		return 1
	case "completed", "done":
		return 2
	case "failed":
		return -1
	case "cancelled":
		return -2
	default:
		return 0
	}
}

// compatFirstNonEmpty 返回首个非空（去空白后）字符串，全空时返回空串。
func compatFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// compatFromTask 把内存实时任务转换为 compat 视图条目。
func compatFromTask(task *Task) map[string]interface{} {
	return map[string]interface{}{
		"actid":      parseIDNum(task.ID),
		"action":     compatActionName(string(task.Type)),
		"status":     compatStatusNumber(task.Status),
		"status_txt": task.Status,
		"progress":   task.Percent,
		"vpsid":      task.ContainerID,
		"started":    task.StartedAt,
		"updated":    compatFirstNonEmpty(task.StartedAt, task.CreatedAt),
		"ended":      "",
		"err_msg":    task.Error,
		"data": map[string]interface{}{
			"id":             task.ID,
			"container_name": task.ContainerName,
			"name":           task.Name,
			"user":           task.User,
		},
	}
}

// compatFromHistoryEntry 把历史留档转换为 compat 视图条目。
func compatFromHistoryEntry(e config.TaskHistoryEntry) map[string]interface{} {
	return map[string]interface{}{
		"actid":      parseIDNum(e.ID),
		"action":     compatActionName(e.Type),
		"status":     compatStatusNumber(e.Status),
		"status_txt": e.Status,
		"progress":   e.Percent,
		"vpsid":      e.ContainerID,
		"started":    e.StartedAt,
		"updated":    compatFirstNonEmpty(e.EndedAt, e.StartedAt, e.CreatedAt),
		"ended":      e.EndedAt,
		"err_msg":    e.Error,
		"data": map[string]interface{}{
			"id":             e.ID,
			"container_name": e.ContainerName,
			"user":           e.User,
		},
	}
}
