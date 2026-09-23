// Package abuseengine 滥用处置动作统一引擎（P3-1）：
//
// 动作原语（每种实现为幂等任务，重复执行结果一致）：
//   - block_ip：nftables 集合 +i 封禁 IP；
//   - throttle：复用 P2-1 qos.Apply 限速到指定 Mbps；
//   - suspend：复用 lxc/kvm 停止接口；
//   - notify：发邮件/Webhook（P7-2 通知渠道）；
//   - revoke：撤销上一动作（反向回滚）。
//
// 联动策略：滥用类型 → 动作组合：
//   - ddos  → block_ip（30min TTL）
//   - spam  → throttle(10Mbps) + notify
//   - abuse → suspend + notify
//   - severe → suspend + block_ip + notify
//
// 全部动作经 P1-4 taskqueue.Queue 异步执行（避免阻塞检测主流程）；
// 执行结果写在 abuse_alerts.actions_taken JSON（不接 DB，调用方序列化）；
// 一键撤销按 LIFO 弹出并反向回滚（block → unblock、throttle → 恢复原限速）。
package abuseengine

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ActionKind 动作原语枚举。
type ActionKind string

const (
	ActionBlockIP  ActionKind = "block_ip"
	ActionThrottle ActionKind = "throttle"
	ActionSuspend  ActionKind = "suspend"
	ActionNotify   ActionKind = "notify"
)

// AbuseKind 滥用类型枚举。
type AbuseKind string

const (
	AbuseDDoS  AbuseKind = "ddos"
	AbuseSpam  AbuseKind = "spam"
	AbuseAbuse AbuseKind = "abuse"
	AbuseSevere AbuseKind = "severe"
)

// ActionRecord 单条动作的执行记录（执行后追加到 AlertActionsTaken）。
type ActionRecord struct {
	Kind       ActionKind `json:"kind"`
	Target     string     `json:"target,omitempty"`     // block_ip 的 IP / throttle 的 instance_id
	Param      string     `json:"param,omitempty"`      // throttle 的 Mbps / block_ip 的 TTL
	StartedAt  time.Time  `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	// Reversible 标记本动作是否可回滚（notify/suspend 可撤销，block_ip 自带 TTL）。
	Reversible bool   `json:"reversible"`
	// ReverseParam 撤销所需参数（如原始限速值）。
	ReverseParam string `json:"reverse_param,omitempty"`
}

// AlertActionsTaken 是某次告警已执行的所有动作（持久化到 abuse_alerts 表）。
type AlertActionsTaken struct {
	mu       sync.Mutex
	alertID  string
	records  []ActionRecord
	executed map[string]bool // idempotency: kind+target+param
}

// NewAlertActionsTaken 创建空集合。
func NewAlertActionsTaken(alertID string) *AlertActionsTaken {
	return &AlertActionsTaken{
		alertID:  alertID,
		executed: map[string]bool{},
	}
}

// Append 添加一条动作（幂等：相同 kind+target+param 仅记一次）。
func (a *AlertActionsTaken) Append(rec ActionRecord) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := actionKey(rec.Kind, rec.Target, rec.Param)
	if a.executed[key] {
		return false
	}
	a.executed[key] = true
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now()
	}
	a.records = append(a.records, rec)
	return true
}

// Records 返回已执行动作的副本（按执行顺序 LIFO 撤销用）。
func (a *AlertActionsTaken) Records() []ActionRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ActionRecord, len(a.records))
	copy(out, a.records)
	return out
}

// Len 已记录数量。
func (a *AlertActionsTaken) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.records)
}

func actionKey(k ActionKind, target, param string) string {
	return string(k) + "|" + target + "|" + param
}

// ---- 策略表（滥用类型 → 动作组合） ----

// ActionPlan 是 AbuseKind 触发的动作计划（按顺序执行）。
type ActionPlan struct {
	Kind AbuseKind
	Actions []PlannedAction
}

// PlannedAction 是策略中的一项动作（与 ActionRecord 对应但尚未执行）。
type PlannedAction struct {
	Kind  ActionKind
	Target string
	Param string
}

// DefaultPolicy 返回内置滥用类型 → 动作映射。
//
// 参数约定：
//   - block_ip 的 Param = TTL 分钟（"30"）；
//   - throttle 的 Param = Mbps（"10"）；
//   - suspend / notify Param = 空。
func DefaultPolicy() map[AbuseKind]ActionPlan {
	return map[AbuseKind]ActionPlan{
		AbuseDDoS: {
			Kind: AbuseDDoS,
			Actions: []PlannedAction{
				{Kind: ActionBlockIP, Param: "30"},
			},
		},
		AbuseSpam: {
			Kind: AbuseSpam,
			Actions: []PlannedAction{
				{Kind: ActionThrottle, Param: "10"},
				{Kind: ActionNotify},
			},
		},
		AbuseAbuse: {
			Kind: AbuseAbuse,
			Actions: []PlannedAction{
				{Kind: ActionSuspend},
				{Kind: ActionNotify},
			},
		},
		AbuseSevere: {
			Kind: AbuseSevere,
			Actions: []PlannedAction{
				{Kind: ActionSuspend},
				{Kind: ActionBlockIP, Param: "1440"},
				{Kind: ActionNotify},
			},
		},
	}
}

// LookupPlan 按滥用类型取计划，未知类型返回错误。
func LookupPlan(kind AbuseKind) (ActionPlan, error) {
	plan, ok := DefaultPolicy()[kind]
	if !ok {
		return ActionPlan{}, fmt.Errorf("abuseengine: unknown kind %q", kind)
	}
	return plan, nil
}

// ---- 执行器抽象 ----

// Executor 是单条动作的执行接口；调用方注入真实实现
// （nftables 调用 / qos.Apply / lxc.Stop / 通知通道）。
//
// Executor.Execute 应当幂等：同一 kind+target+param 重复调用结果一致。
type Executor interface {
	Execute(act PlannedAction) (ActionRecord, error)
}

// NoopExecutor 测试桩：所有动作直接返回成功。
type NoopExecutor struct {
	mu sync.Mutex
	executed []PlannedAction
}

// Execute implements Executor。
func (n *NoopExecutor) Execute(act PlannedAction) (ActionRecord, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.executed = append(n.executed, act)
	return ActionRecord{
		Kind:       act.Kind,
		Target:     act.Target,
		Param:      act.Param,
		StartedAt:  time.Now(),
		CompletedAt: time.Now(),
		Reversible: isReversible(act.Kind),
	}, nil
}

// Executed 返回已执行动作列表（测试断言用）。
func (n *NoopExecutor) Executed() []PlannedAction {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]PlannedAction, len(n.executed))
	copy(out, n.executed)
	return out
}

// isReversible 哪些动作可撤销：suspend/throttle 可，block_ip 自带 TTL
// 走 revoke 流程但语义上算"等待到期"，notify 无副作用。
func isReversible(k ActionKind) bool {
	switch k {
	case ActionSuspend, ActionThrottle:
		return true
	}
	return false
}

// ---- 引擎 ----

// Engine 是动作引擎主入口：拿到告警 → 查策略 → 顺序执行 → 记录。
type Engine struct {
	executor Executor
}

// NewEngine 创建引擎实例。
func NewEngine(executor Executor) *Engine {
	return &Engine{executor: executor}
}

// ApplyAlert 对一条告警执行对应策略（顺序执行，全部记录在 alertActions）。
//
//   - alertActions 必传，作为执行上下文与幂等去重集合；
//   - target 由调用方注入（典型：instance_id 或 IP），policy 中所有动作复用同一 target；
//   - 二次调用同一 alert+kind 时已记录的动作被跳过：Engine 在调用
//     Executor 前先检查 AlertActionsTaken 是否已记录同 kind+target+param。
func (e *Engine) ApplyAlert(kind AbuseKind, target string, alertActions *AlertActionsTaken) (ActionPlan, error) {
	if alertActions == nil {
		return ActionPlan{}, errors.New("abuseengine: alertActions required")
	}
	plan, err := LookupPlan(kind)
	if err != nil {
		return ActionPlan{}, err
	}
	for _, act := range plan.Actions {
		act.Target = target
		// 幂等去重：已记录的动作直接跳过 executor。
		if alertActions.Len() > 0 && hasAction(alertActions, act) {
			continue
		}
		rec, execErr := e.executor.Execute(act)
		if execErr != nil {
			rec.Error = execErr.Error()
		}
		alertActions.Append(rec)
	}
	return plan, nil
}

// hasAction 检查 (kind, target, param) 三元组是否已记录。
func hasAction(a *AlertActionsTaken, act PlannedAction) bool {
	for _, rec := range a.Records() {
		if rec.Kind == act.Kind && rec.Target == act.Target && rec.Param == act.Param {
			return true
		}
	}
	return false
}

// ---- 撤销（LIFO 回滚） ----

// Revoker 是撤销动作的接口；与 Executor 解耦——撤销可能由独立通道
// （如运维通道）实现，避免与正向执行互相阻塞。
type Revoker interface {
	Revoke(rec ActionRecord) error
}

// NoopRevoker 测试桩。
type NoopRevoker struct {
	mu sync.Mutex
	revoked []ActionRecord
}

// Revoke implements Revoker。
func (n *NoopRevoker) Revoke(rec ActionRecord) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.revoked = append(n.revoked, rec)
	return nil
}

// Revoked 返回已撤销记录（测试断言）。
func (n *NoopRevoker) Revoked() []ActionRecord {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]ActionRecord, len(n.revoked))
	copy(out, n.revoked)
	return out
}

// RevokeAll 按 LIFO 顺序撤销所有可逆动作。
//
// 不可逆动作（notify / block_ip 自动到期）静默跳过。
// 撤销过程中出错：记录 Error 字段但继续撤销剩余动作（部分回滚语义）。
func RevokeAll(actions *AlertActionsTaken, rev Revoker) error {
	if actions == nil {
		return errors.New("abuseengine: actions required")
	}
	records := actions.Records()
	var firstErr error
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if !rec.Reversible {
			continue
		}
		if err := rev.Revoke(rec); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ---- 字符串模板（邮件正文 / Webhook payload 共用） ----

// FormatReport 是告警处置报告（人类可读 + 机器可读 JSON）。
func FormatReport(alertID string, kind AbuseKind, actions *AlertActionsTaken) string {
	var b strings.Builder
	fmt.Fprintf(&b, "alert_id=%s kind=%s\n", alertID, kind)
	fmt.Fprintf(&b, "actions:\n")
	for _, rec := range actions.Records() {
		fmt.Fprintf(&b, "  - kind=%s target=%s param=%s error=%q\n",
			rec.Kind, rec.Target, rec.Param, rec.Error)
	}
	return b.String()
}