// Package trafficlimit 流量计量 + 超额自动处置（P2-2）。
//
// 数据模型（增量累计，避免重启重复计数）：
//
//   traffic_counters(instance_id, period, bytes_in, bytes_out, last_reported)
//
//   - period = "YYYY-MM"（月账期）；
//   - 每月 1 号自动归档上月数据并重置本月字段；
//   - Agent 上报 delta（自上次以来的增量字节），控制面原子加到
//     traffic_counters，避免并发聚合重复计数。
//
// 处置策略（policy JSON 存 Container / Plan）：
//
//   traffic_policy = {
//     "monthly_quota_gb": 100,           // 0 = 不限
//     "soft_limit_pct": 80,              // 达到 80% 触发 soft_action
//     "soft_action": "throttle",         // throttle | notify
//     "hard_limit_pct": 100,             // 达到 100% 触发 hard_action
//     "hard_action": "suspend",          // suspend | notify | shutdown
//     "cooldown_hours": 24               // 触发后冷却时间内不重复升级
//   }
//
// 处置执行：复用 P1-4 taskqueue.Queue 与 P2-1 qos.Apply；全部写审计。
//
// 本包只做策略判定与数据累加，不接 HTTP/数据库（调用方注入 store 接口）。
package trafficlimit

import (
	"fmt"
	"strings"
	"time"
)

// PeriodFormat 月账期格式。
const PeriodFormat = "2006-01"

// Policy 流量处置策略（反序列化自 Container.TrafficPolicy JSON 字段）。
type Policy struct {
	MonthlyQuotaGB  int    `json:"monthly_quota_gb"`
	SoftLimitPct    int    `json:"soft_limit_pct"`
	SoftAction      string `json:"soft_action"`
	HardLimitPct    int    `json:"hard_limit_pct"`
	HardAction      string `json:"hard_action"`
	CooldownHours   int    `json:"cooldown_hours"`
}

// Action 处置动作枚举。
type Action string

const (
	ActionThrottle Action = "throttle"
	ActionSuspend  Action = "suspend"
	ActionShutdown Action = "shutdown"
	ActionNotify   Action = "notify"
)

// Decision 是策略评估的输出（升级路径：notify → throttle → suspend → shutdown）。
type Decision struct {
	// Period：当前账期（如 "2026-09"）。
	Period string
	// UsedBytes：本月已用字节数。
	UsedBytes int64
	// QuotaBytes：策略配额（0 = 不限）。
	QuotaBytes int64
	// UsagePct：使用百分比（0-100+，可能超过 100）。
	UsagePct int
	// Action：本评估应采取的动作（ActionNotify = 无动作）。
	Action Action
	// Level：soft / hard / none。
	Level string
	// Reason：决策原因文本（写入审计）。
	Reason string
	// NextAction：升级阈值（soft→hard）触发的下一动作，nil 表示已达上限。
	NextAction *Action
}

// NormalizePolicy 补齐默认值（0 字段 → 合理兜底）。
func NormalizePolicy(p Policy) Policy {
	if p.SoftLimitPct <= 0 || p.SoftLimitPct >= 100 {
		p.SoftLimitPct = 80
	}
	if p.HardLimitPct <= p.SoftLimitPct || p.HardLimitPct > 100 {
		p.HardLimitPct = 100
	}
	if p.SoftAction == "" {
		p.SoftAction = string(ActionThrottle)
	}
	if p.HardAction == "" {
		p.HardAction = string(ActionSuspend)
	}
	if p.CooldownHours <= 0 {
		p.CooldownHours = 24
	}
	return p
}

// Eval 根据已用字节数 + 策略评估出处置决策。
//
//   - MonthlyQuotaGB == 0：策略未配置，返回 Level=none；
//   - 使用百分比 >= HardLimitPct：返回 hard action；
//   - 使用百分比 >= SoftLimitPct：返回 soft action；
//   - 否则 none。
func Eval(p Policy, usedBytes int64, now time.Time) Decision {
	p = NormalizePolicy(p)
	period := now.Format(PeriodFormat)
	if p.MonthlyQuotaGB <= 0 {
		return Decision{Period: period, UsedBytes: usedBytes, Action: ActionNotify, Level: "none", Reason: "policy not configured"}
	}
	quotaBytes := int64(p.MonthlyQuotaGB) * 1024 * 1024 * 1024
	usagePct := int(usedBytes * 100 / quotaBytes)
	if usagePct >= p.HardLimitPct {
		return Decision{
			Period:     period,
			UsedBytes:  usedBytes,
			QuotaBytes: quotaBytes,
			UsagePct:   usagePct,
			Action:     Action(p.HardAction),
			Level:      "hard",
			Reason:     fmt.Sprintf("usage %d%% >= hard %d%%", usagePct, p.HardLimitPct),
		}
	}
	if usagePct >= p.SoftLimitPct {
		next := Action(p.HardAction)
		return Decision{
			Period:      period,
			UsedBytes:   usedBytes,
			QuotaBytes:  quotaBytes,
			UsagePct:    usagePct,
			Action:      Action(p.SoftAction),
			Level:       "soft",
			Reason:      fmt.Sprintf("usage %d%% >= soft %d%%", usagePct, p.SoftLimitPct),
			NextAction:  &next,
		}
	}
	return Decision{
		Period:     period,
		UsedBytes:  usedBytes,
		QuotaBytes: quotaBytes,
		UsagePct:   usagePct,
		Action:     ActionNotify,
		Level:      "none",
		Reason:     fmt.Sprintf("usage %d%% below soft %d%%", usagePct, p.SoftLimitPct),
	}
}

// IsValidAction 检查动作值合法。
func IsValidAction(a Action) bool {
	switch a {
	case ActionThrottle, ActionSuspend, ActionShutdown, ActionNotify:
		return true
	}
	return false
}

// Counters 是月账期累计字节数（用于上报 Agent delta 与持久化）。
type Counters struct {
	InstanceID   int    `json:"instance_id"`
	Period       string `json:"period"` // YYYY-MM
	BytesIn      int64  `json:"bytes_in"`
	BytesOut     int64  `json:"bytes_out"`
	LastReported time.Time `json:"last_reported"`
}

// ApplyDelta 把 Agent 上报的 deltaIn/deltaOut 原子加到 counters，
// 返回新计数；带 last_reported 更新。
//
// period != counters.Period 时自动 rollover：上月计数归档到
// archiveFields 返回值（不持久化，由调用方写入 history 表），新账期从 0 开始。
func ApplyDelta(c Counters, deltaIn, deltaOut int64, now time.Time) (newC Counters, archive *Counters) {
	newC = c
	curPeriod := now.Format(PeriodFormat)
	if newC.Period == "" {
		newC.Period = curPeriod
	}
	if newC.Period != curPeriod {
		// 跨月 rollover：归档上月数据，从 0 起新账期。
		archive = &Counters{
			InstanceID:   newC.InstanceID,
			Period:       newC.Period,
			BytesIn:      newC.BytesIn,
			BytesOut:     newC.BytesOut,
			LastReported: now,
		}
		newC.Period = curPeriod
		newC.BytesIn = 0
		newC.BytesOut = 0
	}
	newC.BytesIn += deltaIn
	newC.BytesOut += deltaOut
	newC.LastReported = now
	return
}

// IsSamePeriod 比较两个 Period 字符串是否同账期（"YYYY-MM"）。
func IsSamePeriod(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}