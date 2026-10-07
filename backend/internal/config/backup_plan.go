package config

import (
	"strings"
	"time"
)

// BackupPlan 定时备份计划（按容器或全部运行中容器）。
//
// 与全局的 InstanceBackupSettings 不同：全局设置只有一个固定周期（IntervalHours），
// 备份计划支持每个计划独立的 cron 表达式、作用目标与保留份数，用于精细化运维。
type BackupPlan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContainerID int    `json:"container_id"` // 0 表示对所有运行中的容器生效
	Cron        string `json:"cron"`         // 简化 5 字段：分 时 日 月 周
	Enabled     bool   `json:"enabled"`
	Keep        int    `json:"keep"` // 保留最新 N 份（<=0 时按默认值处理）
	CreatedAt   string `json:"created_at,omitempty"`
	// LastRunAt / NextRunAt 用于调度去重与「下次运行」展示。
	LastRunAt  string `json:"last_run_at,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	LastStatus string `json:"last_status,omitempty"` // success / failed
	LastError  string `json:"last_error,omitempty"`
	// Runs 保留最近若干次运行摘要，供管理页回看（新记录在前）。
	Runs []BackupPlanRun `json:"runs,omitempty"`
}

// BackupPlanRun 是一次备份计划运行的摘要记录。
type BackupPlanRun struct {
	At         string `json:"at"`
	Status     string `json:"status"` // success / failed
	Backups    int    `json:"backups"`
	Failed     int    `json:"failed"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// maxBackupPlanRuns 每个计划保留的运行记录条数上限。
const maxBackupPlanRuns = 20

// BackupPlanNow 统一用本地时间字符串存计划时间，与既有备份记录格式一致。
func backupPlanNow() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// BackupPlans 返回全部备份计划的拷贝快照。
func BackupPlans() []BackupPlan {
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return nil
	}
	out := make([]BackupPlan, len(AppConfig.BackupPlans))
	copy(out, AppConfig.BackupPlans)
	return out
}

// FindBackupPlan 按 ID 查找计划；不存在返回 nil。
func FindBackupPlan(id string) *BackupPlan {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if AppConfig == nil {
		return nil
	}
	for i := range AppConfig.BackupPlans {
		if AppConfig.BackupPlans[i].ID == id {
			p := AppConfig.BackupPlans[i]
			return &p
		}
	}
	return nil
}

// AddBackupPlan 追加一个计划（ID 重复时返回已存在，不覆盖）。
func AddBackupPlan(p BackupPlan) (BackupPlan, bool) {
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return BackupPlan{}, false
	}
	if p.CreatedAt == "" {
		p.CreatedAt = backupPlanNow()
	}
	added := false
	_ = MutateGlobalMetaOnly(func(cfg *EyvescloudConfig) {
		for i := range cfg.BackupPlans {
			if cfg.BackupPlans[i].ID == p.ID {
				return
			}
		}
		cfg.BackupPlans = append(cfg.BackupPlans, p)
		added = true
	})
	if !added {
		return BackupPlan{}, false
	}
	return p, true
}

// UpdateBackupPlan 按 ID 覆盖计划；不存在返回 false。
func UpdateBackupPlan(p BackupPlan) bool {
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return false
	}
	updated := false
	_ = MutateGlobalMetaOnly(func(cfg *EyvescloudConfig) {
		for i := range cfg.BackupPlans {
			if cfg.BackupPlans[i].ID == p.ID {
				cfg.BackupPlans[i] = p
				updated = true
				return
			}
		}
	})
	return updated
}

// RemoveBackupPlan 按 ID 删除计划；返回是否存在并被删除。
func RemoveBackupPlan(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	removed := false
	_ = MutateGlobalMetaOnly(func(cfg *EyvescloudConfig) {
		for i := range cfg.BackupPlans {
			if cfg.BackupPlans[i].ID == id {
				cfg.BackupPlans = append(cfg.BackupPlans[:i], cfg.BackupPlans[i+1:]...)
				removed = true
				return
			}
		}
	})
	return removed
}

// RecordBackupPlanRun 追加一次运行记录并更新计划的最近状态与下次运行时间。
// nextRunAt 为空时保留原有值。
func RecordBackupPlanRun(id string, run BackupPlanRun, nextRunAt string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	_ = MutateGlobalMetaOnly(func(cfg *EyvescloudConfig) {
		for i := range cfg.BackupPlans {
			if cfg.BackupPlans[i].ID != id {
				continue
			}
			p := &cfg.BackupPlans[i]
			p.Runs = append([]BackupPlanRun{run}, p.Runs...)
			if len(p.Runs) > maxBackupPlanRuns {
				p.Runs = p.Runs[:maxBackupPlanRuns]
			}
			p.LastRunAt = run.At
			p.LastStatus = run.Status
			p.LastError = run.Error
			if nextRunAt != "" {
				p.NextRunAt = nextRunAt
			}
			return
		}
	})
}

// SetBackupPlanNextRun 仅更新计划的下次运行时间（新建/修改后重算）。
func SetBackupPlanNextRun(id, nextRunAt string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	_ = MutateGlobalMetaOnly(func(cfg *EyvescloudConfig) {
		for i := range cfg.BackupPlans {
			if cfg.BackupPlans[i].ID == id {
				cfg.BackupPlans[i].NextRunAt = nextRunAt
				return
			}
		}
	})
}
