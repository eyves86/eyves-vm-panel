package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

// versionString reports the current product version.
func versionString() string {
	return version.Current()
}

// ===========================================================================
// 模块 B —— 审计合规：导出 / 保留期 / 失败告警
// ===========================================================================

// HandleAuditLogExport 导出审计日志为 CSV 或 JSON。
func HandleAuditLogExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "audit:read") {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}
	config.AppConfigMu.RLock()
	logs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()

	// 返回顺序：最新在前
	reversed := make([]config.AuditLog, len(logs))
	for i, l := range logs {
		reversed[len(logs)-1-i] = l
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-logs.json"`)
		json.NewEncoder(w).Encode(reversed)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-logs.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"time", "action", "target", "detail", "user", "ip", "user_agent", "success", "error"})
	for _, l := range reversed {
		success := ""
		if l.Success != nil {
			success = strconv.FormatBool(*l.Success)
		}
		_ = cw.Write([]string{l.Time, l.Action, l.Target, l.Detail, l.User, l.IP, l.UserAgent, success, l.Error})
	}
}

// HandleAuditSettings 获取/更新审计保留期等设置。
func HandleAuditSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "audit:read") {
			return
		}
		config.AppConfigMu.RLock()
		days := config.AppConfig.AuditRetentionDays
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"retention_days":  days,
			"audit_log_count": config.GetAuditLogCount(),
			"retention_notes": "0 表示永久保留；超过保留期的审计与登录日志会被后台定时清理",
		}})
	case http.MethodPut:
		if !requireScope(w, r, "audit:settings") {
			return
		}
		var req struct {
			RetentionDays int `json:"retention_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.RetentionDays < 0 || req.RetentionDays > 3650 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "retention_days must be 0-3650"})
			return
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.AuditRetentionDays = req.RetentionDays })
		auditRequest(r, "audit.settings", "audit", fmt.Sprintf("retention_days=%d", req.RetentionDays), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]int{"retention_days": req.RetentionDays}})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// StartAuditRetention launches the periodic purge of expired audit/login logs.
func StartAuditRetention() {
	retentionOnce.Do(func() {
		go func() {
			purgeRetainedLogs()
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				purgeRetainedLogs()
			}
		}()
	})
}

var retentionOnce sync.Once

func purgeRetainedLogs() {
	if config.AppConfig == nil {
		return
	}
	config.AppConfigMu.RLock()
	days := config.AppConfig.AuditRetentionDays
	config.AppConfigMu.RUnlock()
	if days <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)

	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		audit := cfg.AuditLogs[:0]
		for _, l := range cfg.AuditLogs {
			if logTimeAfterCutoff(l.Time, cutoff) {
				audit = append(audit, l)
			}
		}
		cfg.AuditLogs = audit

		login := cfg.LoginLogs[:0]
		for _, l := range cfg.LoginLogs {
			if logTimeAfterCutoff(l.Time, cutoff) {
				login = append(login, l)
			}
		}
		cfg.LoginLogs = login
	})
}

// logTimeAfterCutoff reports whether the log entry's time is after (newer than) cutoff.
func logTimeAfterCutoff(entryTime string, cutoff time.Time) bool {
	if strings.TrimSpace(entryTime) == "" {
		return true
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05 MST"} {
		if t, err := time.ParseInLocation(layout, entryTime, time.Local); err == nil {
			return t.After(cutoff)
		}
	}
	return true
}

// ===========================================================================
// 模块 C —— 容灾恢复：配置自动备份 / 手动备份 / 下载 / 还原
// ===========================================================================

type configSnapshot struct {
	Kind      string                  `json:"kind"`
	Version   string                  `json:"version"`
	CreatedAt string                  `json:"created_at"`
	Config    config.EyvescloudConfig `json:"config"`
}

// defaultBackupDirectory returns the fixed, safe backup directory.
func defaultBackupDirectory() string {
	dir := config.BackupDirectory()
	_ = os.MkdirAll(dir, 0700)
	return dir
}

// createConfigurationBackup serializes the live config to an atomic JSON file.
func createConfigurationBackup() (config.BackupRecord, error) {
	dir := defaultBackupDirectory()
	ts := time.Now().Format("20060102-150405")
	filename := "config-" + ts + ".json"
	path := filepath.Join(dir, filename)

	snap := configSnapshot{
		Kind:      "config",
		Version:   "1",
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	config.AppConfigMu.RLock()
	snap.Config = *config.AppConfig
	config.AppConfigMu.RUnlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return config.BackupRecord{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return config.BackupRecord{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return config.BackupRecord{}, err
	}
	rec := config.BackupRecord{
		ID:        "bk-" + ts,
		Filename:  filename,
		SizeBytes: int64(len(data)),
		Kind:      "config",
		CreatedAt: snap.CreatedAt,
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Backups = append([]config.BackupRecord{rec}, cfg.Backups...)
		cfg.BackupSettings.LastBackupAt = snap.CreatedAt
		cfg.BackupSettings.LastBackupFile = filename
	})
	pruneBackupFiles(dir)
	return rec, nil
}

// pruneBackupFiles removes the oldest backup files (config records + on-disk files)
// beyond the configured keep count.
func pruneBackupFiles(dir string) {
	keep := config.GetBackupSettings().Keep
	if keep <= 0 {
		keep = 14
	}
	config.AppConfigMu.RLock()
	records := append([]config.BackupRecord(nil), config.AppConfig.Backups...)
	config.AppConfigMu.RUnlock()

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt == records[j].CreatedAt {
			return records[i].Filename < records[j].Filename
		}
		return records[i].CreatedAt > records[j].CreatedAt
	})
	if len(records) <= keep {
		return
	}
	toDelete := records[keep:]
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Backups = records[:keep]
		for _, rec := range toDelete {
			if rec.Filename != "" && filepath.Base(rec.Filename) == rec.Filename {
				_ = os.Remove(filepath.Join(dir, rec.Filename))
			}
		}
	})
}

// StartBackupScheduler runs automatic config backups on the configured interval.
func StartBackupScheduler() {
	backupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				cfg := config.GetBackupSettings()
				if !cfg.Enabled || cfg.IntervalHours < 1 {
					continue
				}
				if cfg.LastBackupAt != "" {
					if last, err := time.ParseInLocation("2006-01-02 15:04:05", cfg.LastBackupAt, time.Local); err == nil {
						if time.Since(last) < time.Duration(cfg.IntervalHours)*time.Hour {
							continue
						}
					}
				}
				_, _ = createConfigurationBackup()
			}
		}()
	})
}

var backupOnce sync.Once

// HandleBackupSettings 获取/更新备份设置。备份目录为固定安全路径，不接受任意配置。
func HandleBackupSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.GetBackupSettings()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"enabled":          cfg.Enabled,
			"interval_hours":   cfg.IntervalHours,
			"keep":             cfg.Keep,
			"directory":        config.BackupDirectory(),
			"last_backup_at":   cfg.LastBackupAt,
			"last_backup_file": cfg.LastBackupFile,
			"backup_count":     config.GetBackupCount(),
		}})
	case http.MethodPut:
		var req config.BackupSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.IntervalHours < 1 {
			req.IntervalHours = 24
		}
		if req.Keep < 1 {
			req.Keep = 14
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			cfg.BackupSettings.Enabled = req.Enabled
			cfg.BackupSettings.IntervalHours = req.IntervalHours
			cfg.BackupSettings.Keep = req.Keep
			// Directory 固定为安全路径，忽略客户端指定的任意目录。
			cfg.BackupSettings.Directory = config.BackupDirectory()
		})
		auditRequest(r, "backup.settings", "backup",
			fmt.Sprintf("enabled=%v interval=%dh keep=%d", req.Enabled, req.IntervalHours, req.Keep), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{"saved": true}})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleBackupCreate 立即创建一份配置备份。
func HandleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	rec, err := createConfigurationBackup()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "backup.create", "backup", "创建配置备份 "+rec.Filename, true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: rec})
}

// HandleBackupList 返回备份记录列表。
func HandleBackupList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	config.AppConfigMu.RLock()
	records := append([]config.BackupRecord(nil), config.AppConfig.Backups...)
	config.AppConfigMu.RUnlock()
	if records == nil {
		records = []config.BackupRecord{}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: records})
}

// HandleBackupDownload 下载指定备份文件的原始内容。
func HandleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	filename := filepath.Base(r.URL.Query().Get("file"))
	if filename == "" || filename == "." || filename == "/" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "file is required"})
		return
	}
	// 仅允许下载已登记的真实备份文件，防止任意文件读取。
	if !config.IsBackupFileKnown(filename) {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "backup not found"})
		return
	}
	path := filepath.Join(defaultBackupDirectory(), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "backup not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Write(data)
}

// HandleBackupRestore 从指定备份文件还原配置（破坏性操作，需管理员二次确认）。
func HandleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		File    string `json:"file"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if !req.Confirm {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "请显式确认以执行破坏性还原"})
		return
	}
	filename := filepath.Base(req.File)
	if filename == "" || filename == "." || filename == "/" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "file is required"})
		return
	}
	// 仅允许还原已登记的真实备份文件，防止还原任意文件以触发任意删除/覆盖。
	if !config.IsBackupFileKnown(filename) {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "backup not found"})
		return
	}
	path := filepath.Join(defaultBackupDirectory(), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "backup not found"})
		return
	}
	var snap configSnapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.Kind != "config" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid backup file"})
		return
	}
	// 还原：以备份内容整体替换当前配置并持久化到 SQLite。
	errRestore := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		*cfg = snap.Config
	})
	if errRestore != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: errRestore.Error()})
		return
	}
	// 对来自旧版本的备份补一次默认归一化/迁移，避免新字段缺失导致运行异常。
	if err := config.ReconcileConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "backup.restore", "backup", "从备份还原配置 "+filename, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "配置已从备份还原，部分运行时状态将在服务重启后完全生效"})
}

// ===========================================================================
// 模块 D —— 可观测性：健康检查 / 运行时指标
// ===========================================================================

var (
	serverStartOnce sync.Once
	serverStartTime time.Time
)

func StartUptimeTracking() {
	serverStartOnce.Do(func() { serverStartTime = time.Now() })
}

func uptimeSeconds() int64 {
	if serverStartTime.IsZero() {
		return 0
	}
	return int64(time.Since(serverStartTime).Seconds())
}

// HandleHealth 返回面向负载均衡/监控的轻量健康状态（公开）。
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"status":  "ok",
		"version": versionString(),
		"uptime":  uptimeSeconds(),
		"time":    time.Now().Format(time.RFC3339),
	}})
}

// HandleHealthDetail 返回管理员可见的更详细运行时指标。
func HandleHealthDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	config.AppConfigMu.RLock()
	containerCount := len(config.AppConfig.Containers)
	nodeCount := len(config.AppConfig.Nodes)
	subUserCount := len(config.AppConfig.SubUsers)
	taskActive := countActiveTasks(config.AppConfig.Tasks)
	config.AppConfigMu.RUnlock()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"status":            "ok",
		"version":           versionString(),
		"uptime":            uptimeSeconds(),
		"go_version":        runtime.Version(),
		"goroutines":        runtime.NumGoroutine(),
		"memory_alloc_mb":   mb(ms.Alloc),
		"memory_sys_mb":     mb(ms.Sys),
		"memory_heap_mb":    mb(ms.HeapAlloc),
		"num_gc":            ms.NumGC,
		"container_count":   containerCount,
		"node_count":        nodeCount,
		"subuser_count":     subUserCount,
		"active_tasks":      taskActive,
		"cpu_cores":         runtime.NumCPU(),
	}})
}

func mb(b uint64) float64 { return float64(b) / (1024 * 1024) }

func countActiveTasks(tasks []config.SavedTask) (n int) {
	for _, t := range tasks {
		if t.Status == "running" || t.Status == "pending" {
			n++
		}
	}
	return n
}

// ===========================================================================
// 模块 E —— API 治理：OpenAPI 契约 / 限流配额
// ===========================================================================

// versionedRateLimiter is a simple per-client-IP sliding-window counter for the
// versioned API. It is intentionally in-memory; the config governs enablement
// and burst. windowSeconds is fixed at 60 (one minute).
type versionedRateLimiter struct {
	mu  sync.Mutex
	win map[string][]time.Time
}

var versionedLimiter = &versionedRateLimiter{win: map[string][]time.Time{}}

func allowVersionedRequest(key string, perMinute int) bool {
	now := time.Now()
	versionedLimiter.mu.Lock()
	defer versionedLimiter.mu.Unlock()
	if perMinute <= 0 {
		perMinute = 120
	}
	cutoff := now.Add(-60 * time.Second)
	kept := versionedLimiter.win[key][:0]
	for _, t := range versionedLimiter.win[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	versionedLimiter.win[key] = kept
	if len(kept) >= perMinute {
		return false
	}
	versionedLimiter.win[key] = append(versionedLimiter.win[key], now)
	// Bounded cleanup: drop empty buckets and, when large, purge all stale keys
	// so the map cannot grow unbounded under distributed traffic.
	if len(versionedLimiter.win) > 5000 {
		for k, times := range versionedLimiter.win {
			if len(times) == 0 || times[len(times)-1].Before(cutoff) {
				delete(versionedLimiter.win, k)
			}
		}
	}
	return true
}

// AllowVersionedRequest is the export used by the server middleware. It accepts
// the client IP string and the per-minute budget; returns whether the request is
// within quota. perMinute is derived from config by the caller.
func AllowVersionedRequest(clientIP string, perMinute int) bool {
	return allowVersionedRequest(ensureString(clientIP), perMinute)
}

func ensureString(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// HandleOpenAPI 返回一份精简的 OpenAPI 3.0 契约，描述核心版本化接口。
func HandleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":       "EyvesCloud API",
			"version":     versionString(),
			"description": "EyvesCloud 云容器管理平台版本化接口契约（精简版）。",
		},
		"servers": []map[string]string{{"url": "/api/v1"}},
		"security": []map[string][]string{{"bearerAuth": []string{}}},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"},
			},
		},
		"paths": map[string]interface{}{
			"/containers": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "列出容器",
					"security":    []map[string][]string{{"bearerAuth": []string{}}},
					"responses":   map[string]interface{}{"200": map[string]string{"description": "OK"}},
				},
			},
			"/tasks": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":   "任务队列",
					"responses": map[string]interface{}{"200": map[string]string{"description": "OK"}},
				},
			},
			"/dashboard": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":   "面板概览",
					"responses": map[string]interface{}{"200": map[string]string{"description": "OK"}},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

// HandleRateLimitSettings 获取/更新 API 限流配置。
func HandleRateLimitSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.GetAPIRateLimit()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"enabled":    cfg.Enabled,
			"per_minute": cfg.PerMinute,
			"scope":      "/api/v1",
			"notes":      "按客户端 IP 在 1 分钟窗口内限制版本化接口的请求次数；通过反向代理时请先配置可信代理",
		}})
	case http.MethodPut:
		var req config.APIRateLimitConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.PerMinute < 1 {
			req.PerMinute = 120
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.APIRateLimit = req })
		auditRequest(r, "rate_limit.settings", "api",
			fmt.Sprintf("enabled=%v per_minute=%d", req.Enabled, req.PerMinute), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{"saved": true}})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// ===========================================================================
// 模块 F —— 多租户：租户 CRUD + 资源配额
// ===========================================================================

// HandleTenants 列出或创建租户。
func HandleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		tenants := append([]config.Tenant(nil), config.AppConfig.Tenants...)
		containers := config.GetContainers()
		config.AppConfigMu.RUnlock()
		if tenants == nil {
			tenants = []config.Tenant{}
		}
		data := make([]map[string]interface{}, 0, len(tenants))
		for _, t := range tenants {
			usage := tenantUsage(t.ID, containers)
			data = append(data, map[string]interface{}{
				"id":               t.ID,
				"name":             t.Name,
				"description":      t.Description,
				"container_quota":  t.ContainerQuota,
				"vcpu_quota":       t.VCPUQuota,
				"ram_quota_mb":     t.RAMQuotaMB,
				"disk_quota_gb":    t.DiskQuotaGB,
				"enabled":          t.Enabled,
				"created_at":       t.CreatedAt,
				"usage_containers": usage.Containers,
				"usage_vcpu":       usage.VCPU,
				"usage_ram_mb":     usage.RAMMB,
				"usage_disk_gb":    usage.DiskGB,
			})
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: data})
	case http.MethodPost:
		var req struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Description    string `json:"description"`
			ContainerQuota int    `json:"container_quota"`
			VCPUQuota      int    `json:"vcpu_quota"`
			RAMQuotaMB     int64  `json:"ram_quota_mb"`
			DiskQuotaGB    int64  `json:"disk_quota_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		id := strings.TrimSpace(req.ID)
		name := strings.TrimSpace(req.Name)
		if id == "" || name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "id and name are required"})
			return
		}
		tenantExists := false
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for _, t := range cfg.Tenants {
				if t.ID == id {
					tenantExists = true
					return
				}
			}
			cfg.Tenants = append(cfg.Tenants, config.Tenant{
				ID:             id,
				Name:           name,
				Description:    strings.TrimSpace(req.Description),
				ContainerQuota: req.ContainerQuota,
				VCPUQuota:      req.VCPUQuota,
				RAMQuotaMB:     req.RAMQuotaMB,
				DiskQuotaGB:    req.DiskQuotaGB,
				Enabled:        true,
				CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
			})
		})
		if tenantExists {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "租户 ID 已存在"})
			return
		}
		auditRequest(r, "tenant.create", "tenant:"+id, name, true, "")
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Message: "租户已创建"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleTenantItem 更新或删除单个租户。
func HandleTenantItem(w http.ResponseWriter, r *http.Request) {
	// 同时支持 /api/tenants/{id} 与 /api/v1/tenants/{id} 两种前缀。
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tenants/")
	id = strings.TrimPrefix(id, "/api/tenants/")
	id = strings.Trim(id, "/")

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			ContainerQuota int    `json:"container_quota"`
			VCPUQuota      int    `json:"vcpu_quota"`
			RAMQuotaMB     int64  `json:"ram_quota_mb"`
			DiskQuotaGB    int64  `json:"disk_quota_gb"`
			Enabled        *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		updated := false
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.Tenants {
				if cfg.Tenants[i].ID != id {
					continue
				}
				if req.Name != "" {
					cfg.Tenants[i].Name = strings.TrimSpace(req.Name)
				}
				cfg.Tenants[i].Description = strings.TrimSpace(req.Description)
				cfg.Tenants[i].ContainerQuota = req.ContainerQuota
				cfg.Tenants[i].VCPUQuota = req.VCPUQuota
				cfg.Tenants[i].RAMQuotaMB = req.RAMQuotaMB
				cfg.Tenants[i].DiskQuotaGB = req.DiskQuotaGB
				if req.Enabled != nil {
					cfg.Tenants[i].Enabled = *req.Enabled
				}
				updated = true
				return
			}
		})
		if !updated {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "租户不存在"})
			return
		}
		auditRequest(r, "tenant.update", "tenant:"+id, "更新租户", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "租户已更新"})
	case http.MethodDelete:
		// 仅允许删除空租户（未绑定任何容器）。
		config.AppConfigMu.RLock()
		containers := config.GetContainers()
		config.AppConfigMu.RUnlock()
		for _, c := range containers {
			if c.Tenant == id {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "租户下仍有容器，无法删除"})
				return
			}
		}
		removed := false
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			filtered := cfg.Tenants[:0]
			for _, t := range cfg.Tenants {
				if t.ID != id {
					filtered = append(filtered, t)
				}
			}
			removed = len(filtered) != len(cfg.Tenants)
			cfg.Tenants = filtered
		})
		if !removed {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "租户不存在"})
			return
		}
		auditRequest(r, "tenant.delete", "tenant:"+id, "删除租户", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "租户已删除"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// tenantUsage aggregates container resource usage for one tenant.
func tenantUsage(tenantID string, containers []config.Container) struct {
	Containers int
	VCPU       int
	RAMMB      int64
	DiskGB     int64
} {
	var u struct {
		Containers int
		VCPU       int
		RAMMB      int64
		DiskGB     int64
	}
	for _, c := range containers {
		if c.Tenant != tenantID {
			continue
		}
		u.Containers++
		u.VCPU += int(math.Ceil(c.VCPU))
		u.RAMMB += int64(c.RAMMB)
		u.DiskGB += int64(math.Ceil(c.DiskGB))
	}
	return u
}

// checkTenantQuota validates whether a new container fits within the tenant's quota.
func checkTenantQuota(tenantID string, vcpu float64, ramMB int, diskGB float64) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil
	}
	var tenant config.Tenant
	found := false
	config.AppConfigMu.RLock()
	for i := range config.AppConfig.Tenants {
		if config.AppConfig.Tenants[i].ID == tenantID {
			tenant = config.AppConfig.Tenants[i] // 值拷贝，避免锁外使用共享切片指针
			found = true
			break
		}
	}
	containers := config.GetContainers()
	config.AppConfigMu.RUnlock()
	if !found {
		return fmt.Errorf("租户 %q 不存在", tenantID)
	}
	if !tenant.Enabled {
		return fmt.Errorf("租户 %q 已禁用", tenantID)
	}
	usage := tenantUsage(tenantID, containers)
	nextContainers := usage.Containers + 1
	nextVCPU := usage.VCPU + int(math.Ceil(vcpu))
	nextRAM := usage.RAMMB + int64(ramMB)
	nextDisk := usage.DiskGB + int64(math.Ceil(diskGB))
	if tenant.ContainerQuota > 0 && nextContainers > tenant.ContainerQuota {
		return fmt.Errorf("租户 %q 容器配额超限（%d/%d）", tenantID, nextContainers, tenant.ContainerQuota)
	}
	if tenant.VCPUQuota > 0 && nextVCPU > tenant.VCPUQuota {
		return fmt.Errorf("租户 %q vCPU 配额超限（%d/%d）", tenantID, nextVCPU, tenant.VCPUQuota)
	}
	if tenant.RAMQuotaMB > 0 && nextRAM > tenant.RAMQuotaMB {
		return fmt.Errorf("租户 %q 内存配额超限（%dMB/%dMB）", tenantID, nextRAM, tenant.RAMQuotaMB)
	}
	if tenant.DiskQuotaGB > 0 && nextDisk > tenant.DiskQuotaGB {
		return fmt.Errorf("租户 %q 磁盘配额超限（%dGB/%dGB）", tenantID, nextDisk, tenant.DiskQuotaGB)
	}
	return nil
}