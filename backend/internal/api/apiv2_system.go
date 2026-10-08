package api

// apiv2_system.go —— API v2：任务 / 备份 / Webhook / 用户 / 管理员 / API Key /
// 审计日志 / 监控指标 / 系统信息。
//
// 端点：
//
//	GET    /api/v2/tasks                    任务列表（状态/类型/实例过滤）
//	GET    /api/v2/tasks/{id}               任务详情（含日志）
//	GET    /api/v2/backups                  实例备份列表（全局，可按实例过滤）
//	POST   /api/v2/backups/{id}/restore     从备份恢复
//	DELETE /api/v2/backups/{id}             删除备份
//	GET    /api/v2/webhooks                 事件订阅列表
//	GET    /api/v2/users                    子用户列表
//	GET    /api/v2/users/{id}               子用户详情（含绑定实例）
//	GET    /api/v2/admins                   管理员账号列表（脱敏）
//	GET    /api/v2/api-keys                 API Key 列表（不含密钥本体）
//	POST   /api/v2/api-keys                 创建 API Key（仅此一次返回明文密钥）
//	DELETE /api/v2/api-keys/{id}            删除 API Key
//	GET    /api/v2/audit-logs               审计日志（操作人/动作/关键字过滤）
//	GET    /api/v2/metrics/host             宿主机实时指标
//	GET    /api/v2/metrics/instances        实例实时指标（批量）
//	GET    /api/v2/metrics/summary          汇总统计（实例/节点/镜像/存储）
//	GET    /api/v2/system/info              面板与运行环境信息
//	GET    /api/v2/system/health            健康检查
//	GET    /api/v2/system/update-check      版本检测（不下载、不升级）

import (
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

func init() {
	registerV2("GET /api/v2/tasks", v2Auth(v2TasksList))
	registerV2("GET /api/v2/tasks/{id}", v2Auth(v2TaskGet))
	registerV2("POST /api/v2/tasks/{id}/cancel", v2Auth(v2TaskCancel))

	registerV2("GET /api/v2/backups", v2Auth(v2BackupsList))
	registerV2("POST /api/v2/backups/{id}/restore", v2Auth(v2BackupRestore))
	registerV2("DELETE /api/v2/backups/{id}", v2Auth(v2BackupDelete))

	registerV2("GET /api/v2/webhooks", v2Auth(v2WebhooksList))

	registerV2("GET /api/v2/users", v2Auth(v2UsersList))
	registerV2("GET /api/v2/users/{id}", v2Auth(v2UserGet))

	registerV2("GET /api/v2/admins", v2Auth(v2AdminsList))

	registerV2("GET /api/v2/api-keys", v2Auth(v2ApiKeysList))
	registerV2("POST /api/v2/api-keys", v2Auth(v2ApiKeysCreate))
	registerV2("DELETE /api/v2/api-keys/{id}", v2Auth(v2ApiKeysDelete))

	registerV2("GET /api/v2/audit-logs", v2Auth(v2AuditLogsList))

	registerV2("GET /api/v2/metrics/host", v2Auth(v2MetricsHost))
	registerV2("GET /api/v2/metrics/instances", v2Auth(v2MetricsInstances))
	registerV2("GET /api/v2/metrics/summary", v2Auth(v2MetricsSummary))

	registerV2("GET /api/v2/system/info", v2Auth(v2SystemInfoHandler))
	registerV2("GET /api/v2/system/health", v2Auth(v2SystemHealth))
	registerV2("GET /api/v2/system/update-check", v2Auth(v2SystemUpdateCheck))
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

func v2TaskView(task config.SavedTask) map[string]interface{} {
	logs := []string{}
	if strings.TrimSpace(task.Config) != "" {
		// Config 为 JSON 负载，不作为日志返回（可能含凭据字段）。
		logs = nil
	}
	return map[string]interface{}{
		"id": task.ID, "type": task.Type, "status": task.Status,
		"error": task.Error, "instance_id": task.ContainerID, "instance_name": task.ContainerName,
		"template_id": task.TemplateID, "created_at": v2Time(task.CreatedAt),
		"operator": task.User, "logs": logs,
	}
}

func v2TasksList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "task:read") {
		return
	}
	query := v2ParsePage(r)
	params := r.URL.Query()
	config.AppConfigMu.RLock()
	tasks := append([]config.SavedTask(nil), config.AppConfig.Tasks...)
	config.AppConfigMu.RUnlock()

	if raw := strings.TrimSpace(params.Get("status")); raw != "" {
		wanted := map[string]bool{}
		for _, s := range strings.Split(raw, ",") {
			wanted[strings.ToLower(strings.TrimSpace(s))] = true
		}
		filtered := tasks[:0]
		for _, t := range tasks {
			if wanted[strings.ToLower(t.Status)] {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	if raw := strings.TrimSpace(params.Get("type")); raw != "" {
		filtered := tasks[:0]
		for _, t := range tasks {
			if strings.EqualFold(t.Type, raw) {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	if raw := strings.TrimSpace(params.Get("instance_id")); raw != "" {
		filtered := tasks[:0]
		for _, t := range tasks {
			if intToStringSafe(t.ContainerID) == raw || t.ContainerName == raw {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := tasks[:0]
		for _, t := range tasks {
			if strings.Contains(strings.ToLower(t.ContainerName), keyword) ||
				strings.Contains(strings.ToLower(t.ID), keyword) {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	// 最新在前
	sortSliceStable(tasks, func(a, b config.SavedTask) bool { return a.CreatedAt > b.CreatedAt })

	summary := map[string]int{"pending": 0, "running": 0, "success": 0, "failed": 0}
	for _, t := range tasks {
		switch strings.ToLower(t.Status) {
		case "pending", "queued", "waiting":
			summary["pending"]++
		case "running", "processing":
			summary["running"]++
		case "success", "done", "completed":
			summary["success"]++
		default:
			summary["failed"]++
		}
	}
	total := len(tasks)
	start, end := query.Slice(total)
	items := make([]map[string]interface{}, 0, end-start)
	for _, t := range tasks[start:end] {
		items = append(items, v2TaskView(t))
	}
	v2ListWithSummary(w, r, items, query, total, summary)
}

func intToStringSafe(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func v2TaskGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "task:read") {
		return
	}
	taskID := strings.TrimSpace(r.PathValue("id"))
	config.AppConfigMu.RLock()
	var found *config.SavedTask
	for i := range config.AppConfig.Tasks {
		if config.AppConfig.Tasks[i].ID == taskID {
			copyTask := config.AppConfig.Tasks[i]
			found = &copyTask
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if found == nil {
		v2NotFound(w, r, "任务不存在："+taskID)
		return
	}
	// 任务执行日志单独存储（按 task_id 归档），读取失败不影响任务主体返回。
	logs, _ := config.ListTaskLogs(taskID)

	view := v2TaskView(*found)
	logViews := make([]map[string]interface{}, 0, len(logs))
	for _, entry := range logs {
		logViews = append(logViews, map[string]interface{}{
			"time": v2Time(entry.CreatedAt), "level": entry.Level, "message": entry.Message,
		})
	}
	view["logs"] = logViews
	v2OK(w, r, view)
}

// ---------------------------------------------------------------------------
// 备份（全局视角）
// ---------------------------------------------------------------------------

func v2BackupsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:read") {
		return
	}
	query := v2ParsePage(r)
	instanceFilter := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	backups := config.ListInstanceBackups()
	items := []map[string]interface{}{}
	for _, backup := range backups {
		if instanceFilter != "" && intToStringSafe(backup.ContainerID) != instanceFilter && backup.ContainerName != instanceFilter {
			continue
		}
		entry := v2BackupView(backup)
		entry["instance_name"] = backup.ContainerName
		items = append(items, entry)
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(stringOrEmpty(item["instance_name"])), keyword) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	sortSliceStable(items, func(a, b map[string]interface{}) bool {
		return strings.Compare(stringOrEmpty(a["created_at"]), stringOrEmpty(b["created_at"])) > 0
	})
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func stringOrEmpty(value interface{}) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func v2BackupRestore(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:restore") {
		return
	}
	backupID := strings.TrimSpace(r.PathValue("id"))
	found := false
	for _, backup := range config.ListInstanceBackups() {
		if backup.ID == backupID {
			found = true
			break
		}
	}
	if !found {
		v2NotFound(w, r, "备份不存在："+backupID)
		return
	}
	if err := restoreInstanceBackup(backupID); err != nil {
		v2Upstream(w, r, "恢复失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.backup.restore", backupID, "", true, "")
	v2Accepted(w, r, map[string]interface{}{"id": backupID, "restored": true})
}

func v2BackupDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "snapshot:delete") {
		return
	}
	backupID := strings.TrimSpace(r.PathValue("id"))
	found := false
	for _, backup := range config.ListInstanceBackups() {
		if backup.ID == backupID {
			found = true
			break
		}
	}
	if !found {
		v2NotFound(w, r, "备份不存在："+backupID)
		return
	}
	if err := deleteInstanceBackup(backupID); err != nil {
		v2Internal(w, r, "删除失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.backup.delete", backupID, "", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// Webhook
// ---------------------------------------------------------------------------

func v2WebhooksList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	subscriptions := append([]config.WebhookSubscription(nil), config.AppConfig.Webhooks...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(subscriptions))
	for _, sub := range subscriptions {
		items = append(items, map[string]interface{}{
			"id": sub.ID, "name": sub.Name, "url": sub.URL,
			"event_types": sub.EventTypes, "enabled": sub.Enabled,
			"consecutive_failures": sub.ConsecutiveFailures,
			"last_delivery_at":     v2Time(sub.LastDeliveryAt),
			"last_delivery_status": sub.LastDeliveryStatus,
			"created_at":           v2Time(sub.CreatedAt),
			"has_secret":           sub.Secret != "",
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

// ---------------------------------------------------------------------------
// 用户 / 管理员
// ---------------------------------------------------------------------------

func v2UsersList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(subUsers))
	for _, su := range subUsers {
		uuids := activeSubUserContainerUUIDs(&su)
		items = append(items, map[string]interface{}{
			"id": su.ID, "username": su.Username, "email": su.Email,
			"role": subUserRole(su.Role), "tenant": su.Tenant,
			"instance_count": len(uuids), "instance_uuids": uuids,
			"created_at": v2Time(su.CreatedAt), "token_version": su.TokenVersion,
		})
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(stringOrEmpty(item["username"])), keyword) ||
				strings.Contains(strings.ToLower(stringOrEmpty(item["email"])), keyword) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2UserGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	config.AppConfigMu.RLock()
	var found *config.SubUser
	for i := range config.AppConfig.SubUsers {
		su := config.AppConfig.SubUsers[i]
		if su.ID == userID || strings.EqualFold(su.Username, userID) {
			copySU := su
			found = &copySU
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if found == nil {
		v2NotFound(w, r, "用户不存在："+userID)
		return
	}
	uuids := activeSubUserContainerUUIDs(found)
	instances := []map[string]interface{}{}
	for _, uuid := range uuids {
		if c := config.FindContainerByUUID(uuid); c != nil {
			instances = append(instances, map[string]interface{}{
				"id": c.ID, "name": c.Name, "status": v2InstanceStatus(*c), "primary_ip": c.IP,
			})
		}
	}
	v2OK(w, r, map[string]interface{}{
		"id": found.ID, "username": found.Username, "email": found.Email,
		"role": subUserRole(found.Role), "tenant": found.Tenant,
		"instance_uuids": uuids, "instances": instances,
		"created_at": v2Time(found.CreatedAt), "token_version": found.TokenVersion,
	})
}

func v2AdminsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	admins := append([]config.AdminAccount(nil), config.AppConfig.Admins...)
	mainAdmin := config.AppConfig.AdminUser
	config.AppConfigMu.RUnlock()
	items := []map[string]interface{}{{
		"id": "founder", "username": mainAdmin, "role": config.AdminRoleAdmin,
		"is_founder": true, "disabled": false,
	}}
	for _, admin := range admins {
		items = append(items, map[string]interface{}{
			"id": admin.ID, "username": admin.Username, "role": config.NormalizeAdminRole(admin.Role),
			"is_founder": false, "disabled": admin.Disabled,
			"last_login_at": v2Time(admin.LastLoginAt), "created_at": v2Time(admin.CreatedAt),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

// ---------------------------------------------------------------------------
// API Key
// ---------------------------------------------------------------------------

func v2ApiKeysList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	keys := append([]config.ApiKeyConfig(nil), config.AppConfig.ApiKeys...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(keys))
	for _, key := range keys {
		items = append(items, map[string]interface{}{
			"id": key.ID, "name": key.Name, "prefix": key.Prefix,
			"scopes": key.Scopes, "ip_whitelist": key.IPWhitelist,
			"created_at": v2Time(key.CreatedAt), "last_used_at": v2Time(key.LastUsed),
			"expires_at": v2Time(key.ExpiresAt),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2ApiKeysCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	var req struct {
		Name            string   `json:"name"`
		Scopes          []string `json:"scopes"`
		IPWhitelist     string   `json:"ip_whitelist"`
		ContainerUUIDs  []string `json:"container_uuids"`
		ExpiresAt       string   `json:"expires_at"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	// 授予范围不得超出调用方自身权限（防止受限密钥自我提权）。
	if err := validateKeyGrantScopes(r, req.Scopes); err != nil {
		v2Forbidden(w, r, err.Error())
		return
	}
	rawKey := "eyvescloud_sk_" + randomHex(24)
	hash, err := hashAPIKey(rawKey)
	if err != nil {
		v2Internal(w, r, "生成密钥失败："+err.Error())
		return
	}
	for _, uuid := range req.ContainerUUIDs {
		if c := config.FindContainerByUUID(uuid); c == nil {
			v2NotFound(w, r, "容器不存在："+uuid)
			return
		}
	}
	entry := config.ApiKeyConfig{
		ID:             "ak-" + randomHex(8),
		Name:           strings.TrimSpace(req.Name),
		KeyHash:        hash,
		KeyFingerprint: apiKeyFingerprint(rawKey),
		Prefix:         rawKey[:20] + "...",
		IPWhitelist:    strings.TrimSpace(req.IPWhitelist),
		CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
		Scopes:         req.Scopes,
		ExpiresAt:      normalizeV2Date(req.ExpiresAt),
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ApiKeys = append(cfg.ApiKeys, entry)
	})
	auditRequest(r, "api.v2.api_key.create", entry.Name, "scopes="+strings.Join(req.Scopes, ","), true, "")
	v2Created(w, r, map[string]interface{}{
		"id": entry.ID, "name": entry.Name, "api_key": rawKey, // 仅此一次返回明文
		"scopes": entry.Scopes, "expires_at": v2Time(entry.ExpiresAt),
	})
}

func v2ApiKeysDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	keyID := strings.TrimSpace(r.PathValue("id"))
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.ApiKeys[:0]
		for i := range cfg.ApiKeys {
			if cfg.ApiKeys[i].ID == keyID {
				removed = true
				continue
			}
			out = append(out, cfg.ApiKeys[i])
		}
		cfg.ApiKeys = out
	})
	if !removed {
		v2NotFound(w, r, "API Key 不存在："+keyID)
		return
	}
	auditRequest(r, "api.v2.api_key.delete", keyID, "", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// 审计日志
// ---------------------------------------------------------------------------

func v2AuditLogsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	query := v2ParsePage(r)
	params := r.URL.Query()
	config.AppConfigMu.RLock()
	logs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()

	actor := strings.TrimSpace(params.Get("actor"))
	action := strings.TrimSpace(params.Get("action"))
	target := strings.TrimSpace(params.Get("target"))
	onlyFailed := strings.EqualFold(params.Get("success"), "false")

	items := []map[string]interface{}{}
	for i := len(logs) - 1; i >= 0; i-- {
		entry := logs[i]
		if actor != "" && !strings.Contains(strings.ToLower(entry.User), strings.ToLower(actor)) {
			continue
		}
		if action != "" && !strings.HasPrefix(entry.Action, action) {
			continue
		}
		if target != "" && !strings.Contains(strings.ToLower(entry.Target), strings.ToLower(target)) {
			continue
		}
		success := entry.Success == nil || *entry.Success
		if onlyFailed && success {
			continue
		}
		if keyword := strings.ToLower(query.Search); keyword != "" &&
			!strings.Contains(strings.ToLower(entry.Detail), keyword) &&
			!strings.Contains(strings.ToLower(entry.Target), keyword) {
			continue
		}
		items = append(items, map[string]interface{}{
			"time": v2Time(entry.Time), "action": entry.Action, "target": entry.Target,
			"detail": entry.Detail, "actor": entry.User, "ip": entry.IP,
			"user_agent": entry.UserAgent, "success": success, "error": entry.Error,
			"hash": entry.Hash,
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

// ---------------------------------------------------------------------------
// 监控指标
// ---------------------------------------------------------------------------

func v2MetricsHost(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "dashboard:read") {
		return
	}
	v2OK(w, r, map[string]interface{}{
		"host":      hostSummaryForIDC(),
		"server_time": time.Now().Format(time.RFC3339),
	})
}

func v2MetricsInstances(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	containers, _ := listByRuntime()
	containers = filterContainersForRequest(r, containers)
	items := make([]map[string]interface{}, 0, len(containers))
	for _, c := range containers {
		entry := map[string]interface{}{
			"id": c.ID, "name": c.Name, "status": v2InstanceStatus(c),
		}
		if point, ok := latestContainerMetric(c.UUID); ok {
			entry["cpu_percent"] = point.CPU
			entry["memory_percent"] = point.Memory
			entry["network_rx_bps"] = point.NetworkRx
			entry["network_tx_bps"] = point.NetworkTx
			entry["disk_read_bps"] = point.DiskRead
			entry["disk_write_bps"] = point.DiskWrite
			if point.TS > 0 {
				entry["timestamp"] = time.Unix(point.TS, 0).Format(time.RFC3339)
			}
		}
		items = append(items, entry)
	}
	v2OK(w, r, map[string]interface{}{"items": items, "total": len(items)})
}

func v2MetricsSummary(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "dashboard:read") {
		return
	}
	containers, _ := listByRuntime()
	containers = filterContainersForRequest(r, containers)
	running, stopped, suspended := 0, 0, 0
	var vcpuSum float64
	memorySum := 0
	for _, c := range containers {
		switch v2InstanceStatus(c) {
		case "running":
			running++
		case "suspended":
			suspended++
		default:
			stopped++
		}
		vcpuSum += c.VCPU
		memorySum += c.RAMMB
	}
	config.AppConfigMu.RLock()
	nodeTotal, nodeOnline := len(config.AppConfig.Nodes), 0
	for _, n := range config.AppConfig.Nodes {
		if n.Status == "online" {
			nodeOnline++
		}
	}
	imageTotal := len(config.AppConfig.EnabledImages)
	poolTotal := len(config.AppConfig.StoragePools)
	snapshotTotal := len(config.AppConfig.Snapshots)
	backupTotal := len(config.AppConfig.InstanceBackups)
	config.AppConfigMu.RUnlock()
	v2OK(w, r, map[string]interface{}{
		"instances": map[string]interface{}{
			"total": len(containers), "running": running, "stopped": stopped,
			"suspended": suspended, "vcpu": vcpuSum, "memory_mb": memorySum,
		},
		"nodes":     map[string]interface{}{"total": nodeTotal, "online": nodeOnline},
		"images":    map[string]interface{}{"enabled": imageTotal},
		"storage":   map[string]interface{}{"pools": poolTotal},
		"snapshots": map[string]interface{}{"total": snapshotTotal},
		"backups":   map[string]interface{}{"total": backupTotal},
	})
}

// ---------------------------------------------------------------------------
// 系统
// ---------------------------------------------------------------------------

func v2SystemInfoHandler(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "dashboard:read") {
		return
	}
	// admin_path/data_dir 仅管理员可见（动态审计 F-2 修复：防隐藏管理路径泄漏）。
	ctx, _ := authContextFromRequest(r)
	v2OK(w, r, v2SystemInfo(ctx.Type == authTypeAdmin))
}

func v2SystemHealth(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "dashboard:read") {
		return
	}
	config.AppConfigMu.RLock()
	taskActive := 0
	for _, task := range config.AppConfig.Tasks {
		switch strings.ToLower(task.Status) {
		case "pending", "queued", "running", "processing":
			taskActive++
		}
	}
	config.AppConfigMu.RUnlock()
	v2OK(w, r, map[string]interface{}{
		"status":         "ok",
		"version":        versionString(),
		"uptime_seconds": uptimeSeconds(),
		"active_tasks":   taskActive,
		"server_time":    time.Now().Format(time.RFC3339),
		"lxc_enabled":    commandExists("lxc-create"),
		"kvm_enabled":    hostKVMAvailable(),
	})
}

func v2SystemUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	result := checkUpdateCached(10 * time.Minute)
	v2OK(w, r, map[string]interface{}{
		"current":    result.Current,
		"latest":     result.Latest,
		"has_update": result.HasUpdate,
		"error":      result.Err,
	})
}

// v2TaskCancel POST /tasks/{id}/cancel：取消排队中的任务。
//
// 语义（企业集成常见诉求：计费回调超时后撤单）：
//   - 排队中 → 取消成功，返回 200 {canceled:true}
//   - 已在执行 → 409 CONFLICT（运行中的任务无法安全中断，需等其结束）
//   - 不存在/已结束 → 404 / 200（幂等：已结束视为无需取消）
func v2TaskCancel(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "task:read") {
		return
	}
	taskID := strings.TrimSpace(r.PathValue("id"))
	config.AppConfigMu.RLock()
	found := false
	for i := range config.AppConfig.Tasks {
		if config.AppConfig.Tasks[i].ID == taskID {
			found = true
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if !found {
		v2NotFound(w, r, "任务不存在："+taskID)
		return
	}
	canceled, running := globalQueue.Cancel(taskID)
	if running {
		v2Conflict(w, r, "任务已在执行，无法取消（请等待其结束）")
		return
	}
	auditRequest(r, "api.v2.task.cancel", taskID, "", true, "")
	if !canceled {
		// 任务已结束：幂等返回，不视为错误。
		v2OK(w, r, map[string]interface{}{"id": taskID, "canceled": false, "reason": "任务已结束"})
		return
	}
	v2OK(w, r, map[string]interface{}{"id": taskID, "canceled": true})
}
