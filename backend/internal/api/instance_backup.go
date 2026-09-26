package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

const (
	instanceBackupSchedulerTick   = 30 * time.Minute
	defaultInstanceBackupInterval = 24
	defaultInstanceBackupKeep     = 7
)

var instanceBackupSchedulerOnce sync.Once

// StartInstanceBackupScheduler 启动实例磁盘自动备份调度器。
// 每 30 分钟检查一次是否到达配置的备份周期，到期则为所有运行中的容器创建磁盘备份。
func StartInstanceBackupScheduler() {
	instanceBackupSchedulerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(instanceBackupSchedulerTick)
			defer ticker.Stop()
			for range ticker.C {
				runDueInstanceBackups()
			}
		}()
	})
}

// runDueInstanceBackups 在到达备份周期时，为所有运行中的容器创建一份磁盘备份。
func runDueInstanceBackups() {
	settings := config.GetInstanceBackupSettings()
	if !settings.Enabled {
		return
	}

	interval := settings.IntervalHours
	if interval < 1 {
		interval = defaultInstanceBackupInterval
	}
	if settings.LastRunAt != "" {
		if last, err := time.ParseInLocation("2006-01-02 15:04:05", settings.LastRunAt, time.Local); err == nil {
			if time.Since(last) < time.Duration(interval)*time.Hour {
				return
			}
		}
	}

	keep := settings.Keep
	if keep < 1 {
		keep = defaultInstanceBackupKeep
	}

	// 先记录运行时间，避免单轮备份耗时导致下一轮重复触发。
	config.UpdateInstanceBackupLastRun(time.Now().Format("2006-01-02 15:04:05"))

	for _, c := range config.GetContainers() {
		if c.Status != "running" {
			continue
		}
		if _, err := createInstanceBackup(c.ID, "scheduler", keep, true); err != nil {
			fmt.Printf("Warning: scheduled instance backup failed for %s: %v\n", c.Name, err)
		}
	}
	_ = config.SaveConfig()
}

// HandleInstanceBackupSettings 读取/更新实例磁盘自动备份设置。
func HandleInstanceBackupSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "backup:read") {
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: config.GetInstanceBackupSettings()})
	case http.MethodPut:
		if !requireScope(w, r, "backup:write") {
			return
		}
		var req config.InstanceBackupSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if req.IntervalHours < 1 {
			req.IntervalHours = defaultInstanceBackupInterval
		}
		if req.Keep < 1 {
			req.Keep = defaultInstanceBackupKeep
		}
		// 保留已有的上次运行时间，避免更新设置导致周期被重置。
		req.LastRunAt = config.GetInstanceBackupSettings().LastRunAt
		config.UpdateInstanceBackupSettings(req)
		if err := config.SaveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "instance_backup.settings", "settings",
			fmt.Sprintf("enabled=%v interval_hours=%d keep=%d", req.Enabled, req.IntervalHours, req.Keep), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: req})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}
