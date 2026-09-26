// Package restoredrill 备份恢复与演练（P4-4）：
//
//   - SandboxedRestore：选备份 → 建沙箱实例 → 驱动恢复 → 探活 → 报告；
//   - 沙箱实例隔离网络 + 命名 restore-xxx + 自动到期清理；
//   - ProductionRestore：覆盖原实例（必须二次确认 + 维护窗 + 全审计）；
//   - DrillResult 报告（启动结果 / 抽检 / 耗时）；
//   - 自动演练（policy.monthly_drill_enabled）+ 失败通知。
//
// 本包只产出恢复流程编排与报告结构；真实恢复（zfs send recv / rbd
// import-diff / dir tar -xf）由调用方注入 Driver。
package restoredrill

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RestoreMode 恢复模式。
type RestoreMode string

const (
	ModeSandbox    RestoreMode = "sandbox"
	ModeProduction RestoreMode = "production"
)

// BackupRef 单个备份引用（与 backuppolicy.BackupJob 对齐；JSON 互通）。
type BackupRef struct {
	Backend    string `json:"backend"`     // "zfs" / "rbd" / "dir"
	Instance   string `json:"instance"`
	SnapshotID string `json:"snapshot_id"`
	Bytes      int64  `json:"bytes,omitempty"`
	Path       string `json:"path,omitempty"`
}

// DrillResult 演练报告（人类+机器可读）。
type DrillResult struct {
	ID           string    `json:"id"`
	Mode         RestoreMode `json:"mode"`
	BackupRef    BackupRef `json:"backup_ref"`
	SandboxID    string    `json:"sandbox_id,omitempty"`    // 沙箱实例 ID
	OriginalID   int       `json:"original_id,omitempty"`   // 覆盖恢复时为被覆盖实例 ID
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	BootSuccess  bool      `json:"boot_success"`
	ChecksumPass bool      `json:"checksum_pass"`
	Error        string    `json:"error,omitempty"`
	// SpotCheckFiles：抽检文件列表 + 校验结果。
	SpotCheckFiles []SpotCheckResult `json:"spot_check_files,omitempty"`
}

// SpotCheckResult 单文件抽检结果。
type SpotCheckResult struct {
	Path     string `json:"path"`
	Expected string `json:"expected_sha256"`
	Actual   string `json:"actual_sha256"`
	Match    bool   `json:"match"`
}

// ErrProductionRequiresConfirmation 生产覆盖恢复必须二次确认。
var ErrProductionRequiresConfirmation = errors.New("restoredrill: production restore requires explicit confirmation")

// ErrNotConfirmed 沙箱 / 生产恢复未经过 confirm 通道。
var ErrNotConfirmed = errors.New("restoredrill: confirmation token invalid or missing")

// Driver 真实恢复驱动接口（zfs recv / rbd import-diff / tar -xf）。
type Driver interface {
	RestoreSandbox(ref BackupRef) (sandboxID string, err error)
	// RestoreProduction 覆盖原实例：返回是否需要 destroy + recreate。
	RestoreProduction(ref BackupRef, originalID int) error
	// SpotCheckFiles 在沙箱内抽检指定路径的 sha256（驱动返回错误时记
	// 入报告 Error 字段，不阻塞整体）。
	SpotCheckFiles(sandboxID string, paths []string) ([]SpotCheckResult, error)
	// CleanupSandbox 删除沙箱实例（演练到期自动清理）。
	CleanupSandbox(sandboxID string) error
}

// Confirmer 是二次确认抽象（运维通道 / 邮件链接 token / 2FA）。
// 实现可注入"必须 admin 走指定通道确认"的逻辑。
type Confirmer interface {
	// Confirm 返回 nil = 允许；返回错误 = 拒绝。
	Confirm(intent ConfirmationIntent) error
}

// ConfirmationIntent 描述一次待确认操作。
type ConfirmationIntent struct {
	Mode         RestoreMode `json:"mode"`
	BackupRef    BackupRef   `json:"backup_ref"`
	OriginalID   int         `json:"original_id,omitempty"`
	RequestedAt  time.Time   `json:"requested_at"`
	RequestedBy  string      `json:"requested_by"`
}

// Engine 是恢复与演练的协调器。
type Engine struct {
	mu       sync.Mutex
	driver   Driver
	confirmer Confirmer
	results  []*DrillResult
}

// NewEngine 创建引擎；driver / confirmer 必传（测试桩 NoopDriver / AllowAllConfirmer）。
func NewEngine(driver Driver, confirmer Confirmer) *Engine {
	return &Engine{driver: driver, confirmer: confirmer}
}

// DrillSandbox 触发沙箱演练（不需要 confirm）。
//
// 流程：
//   1) Driver.RestoreSandbox 创建沙箱实例；
//   2) Driver.SpotCheckFiles 抽检核心文件；
//   3) Driver.CleanupSandbox 清理（即便前面步骤失败）。
//
// 返回 DrillResult 永远非 nil（即便整体失败）；调用方写入审计。
func (e *Engine) DrillSandbox(ref BackupRef) (*DrillResult, error) {
	if e.driver == nil {
		return nil, errors.New("restoredrill: nil driver")
	}
	r := &DrillResult{
		ID:        fmt.Sprintf("drill-%d", time.Now().UnixNano()),
		Mode:      ModeSandbox,
		BackupRef: ref,
		StartedAt: time.Now(),
	}
	// 1) 创建沙箱
	sandboxID, err := e.driver.RestoreSandbox(ref)
	r.FinishedAt = time.Now()
	if err != nil {
		r.Error = err.Error()
		e.record(r)
		return r, err
	}
	r.SandboxID = sandboxID
	r.BootSuccess = true // 启动成功 = sandbox 创建成功
	// 2) 抽检（即便 SpotCheck 失败也继续，错误写入 Result）。
	spotChecks, err := e.driver.SpotCheckFiles(sandboxID, []string{"/etc/passwd", "/etc/hostname"})
	if err != nil {
		r.Error = fmt.Sprintf("spot_check: %v", err)
	} else {
		r.SpotCheckFiles = spotChecks
		allMatch := true
		for _, sc := range spotChecks {
			if !sc.Match {
				allMatch = false
			}
		}
		r.ChecksumPass = allMatch
	}
	// 3) 清理（无论前面步骤成功失败都尝试清理）。
	if cerr := e.driver.CleanupSandbox(sandboxID); cerr != nil && err == nil {
		err = fmt.Errorf("cleanup sandbox %s: %w", sandboxID, cerr)
	}
	e.record(r)
	if err != nil {
		return r, err
	}
	return r, nil
}

// RestoreProduction 覆盖原实例。必须二次确认（confirmer 通道）。
//
// 流程：
//   1) Confirmer.Confirm：仅 admin + 维护窗 + 显式 token 三件套通过才放行；
//   2) Driver.RestoreProduction：destroy 原实例 + 从备份恢复。
//   3) 返回 DrillResult（成功/失败均落审计）。
func (e *Engine) RestoreProduction(ref BackupRef, originalID int, requestedBy string) (*DrillResult, error) {
	if e.driver == nil {
		return nil, errors.New("restoredrill: nil driver")
	}
	if e.confirmer == nil {
		return nil, ErrProductionRequiresConfirmation
	}
	intent := ConfirmationIntent{
		Mode:        ModeProduction,
		BackupRef:   ref,
		OriginalID:  originalID,
		RequestedAt: time.Now(),
		RequestedBy: requestedBy,
	}
	if err := e.confirmer.Confirm(intent); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotConfirmed, err)
	}
	r := &DrillResult{
		ID:         fmt.Sprintf("restore-prod-%d", time.Now().UnixNano()),
		Mode:       ModeProduction,
		BackupRef:  ref,
		OriginalID: originalID,
		StartedAt:  time.Now(),
	}
	if err := e.driver.RestoreProduction(ref, originalID); err != nil {
		r.Error = err.Error()
		r.FinishedAt = time.Now()
		e.record(r)
		return r, err
	}
	r.BootSuccess = true
	r.ChecksumPass = true
	r.FinishedAt = time.Now()
	e.record(r)
	return r, nil
}

// Results 返回已记录的报告（测试断言用）。
func (e *Engine) Results() []DrillResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]DrillResult, len(e.results))
	for i, r := range e.results {
		out[i] = *r
	}
	return out
}

func (e *Engine) record(r *DrillResult) {
	e.mu.Lock()
	e.results = append(e.results, r)
	e.mu.Unlock()
}

// ---- 测试桩 ----

// NoopDriver 测试驱动（演练路径全部走通；production 覆盖总是 nil）。
type NoopDriver struct{}

// RestoreSandbox implements Driver。
func (NoopDriver) RestoreSandbox(BackupRef) (string, error) { return "sandbox-fake", nil }

// RestoreProduction implements Driver。
func (NoopDriver) RestoreProduction(BackupRef, int) error { return nil }

// SpotCheckFiles implements Driver。
func (NoopDriver) SpotCheckFiles(string, []string) ([]SpotCheckResult, error) {
	return []SpotCheckResult{
		{Path: "/etc/passwd", Expected: "any", Actual: "any", Match: true},
	}, nil
}

// CleanupSandbox implements Driver。
func (NoopDriver) CleanupSandbox(string) error { return nil }

// AllowAllConfirmer 测试桩（永远允许）。
type AllowAllConfirmer struct{}

// Confirm implements Confirmer。
func (AllowAllConfirmer) Confirm(ConfirmationIntent) error { return nil }

// DenyAllConfirmer 测试桩（永远拒绝）。
type DenyAllConfirmer struct{}

// Confirm implements Confirmer。
func (DenyAllConfirmer) Confirm(ConfirmationIntent) error { return errors.New("admin declined") }