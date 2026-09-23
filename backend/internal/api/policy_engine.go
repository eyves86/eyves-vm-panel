package api

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

var (
	policyEngineOnce     sync.Once
	policyTriggerMu      sync.Mutex
	policyLastTrigger    = map[string]time.Time{}
	policyEngineInterval = 60 * time.Second
)

// StartPolicyEngine launches the background evaluator for CPU/bandwidth
// auto-adjustment policies. It samples running containers every minute and
// triggers matching actions with a per-rule cooldown.
func StartPolicyEngine() {
	policyEngineOnce.Do(func() {
		go func() {
			// Seed last-trigger from persisted rule metadata after restart.
			seedPolicyLastTrigger()
			evaluatePolicyRules()
			ticker := time.NewTicker(policyEngineInterval)
			defer ticker.Stop()
			for range ticker.C {
				evaluatePolicyRules()
			}
		}()
	})
}

func seedPolicyLastTrigger() {
	if config.AppConfig == nil {
		return
	}
	config.AppConfigMu.RLock()
	rules := append([]config.PolicyRule(nil), config.AppConfig.PolicyRules...)
	containers := config.GetContainers()
	config.AppConfigMu.RUnlock()
	policyTriggerMu.Lock()
	defer policyTriggerMu.Unlock()
	for i := range rules {
		rule := &rules[i]
		if rule.LastTriggered != "" {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", rule.LastTriggered, time.Local); err == nil {
				for _, c := range containers {
					policyLastTrigger[policyCooldownKey(rule.ID, c)] = t
				}
			}
		}
	}
}

func policyCooldownKey(ruleID string, c config.Container) string {
	return ruleID + "|" + strconv.Itoa(c.ID)
}

func policyCooldownMinutes(rule config.PolicyRule) int {
	if rule.CooldownMin <= 0 {
		return config.DefaultPolicyCooldownMinutes
	}
	return rule.CooldownMin
}

func evaluatePolicyRules() {
	if config.AppConfig == nil {
		return
	}
	// Snapshot under the config read lock so concurrent container/rule writes
	// (HTTP handlers, metric samplers) cannot tear the slices being evaluated.
	config.AppConfigMu.RLock()
	rules := append([]config.PolicyRule(nil), config.AppConfig.PolicyRules...)
	containers := config.GetContainers()
	config.AppConfigMu.RUnlock()
	now := time.Now()

	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}
		cooldown := time.Duration(policyCooldownMinutes(*rule)) * time.Minute
		for j := range containers {
			c := &containers[j]
			if c.Status != "running" {
				continue
			}
			if !policyMatchesScope(*rule, *c) {
				continue
			}
			key := policyCooldownKey(rule.ID, *c)
			policyTriggerMu.Lock()
			last, ok := policyLastTrigger[key]
			policyTriggerMu.Unlock()
			if ok && now.Sub(last) < cooldown {
				continue
			}
			usage, err := usageByRuntime(c.ID)
			if err != nil {
				continue
			}
			value := policyMetricValue(*rule, *c, usage)
			if !policyConditionHolds(rule.Operator, value, rule.Threshold) {
				continue
			}
			triggerPolicyAction(*rule, c.ID, c.Name, value)
			policyTriggerMu.Lock()
			policyLastTrigger[key] = now
			policyTriggerMu.Unlock()
		}
	}
}

func policyMatchesScope(rule config.PolicyRule, c config.Container) bool {
	scope := rule.TargetScope
	if scope == "" || scope == "all" {
		return true
	}
	if len(scope) > 7 && scope[:7] == "tenant:" {
		return c.Tenant == scope[7:]
	}
	if len(scope) > 10 && scope[:10] == "container:" {
		return c.Name == scope[10:] || scope[10:] == fmt.Sprintf("%d", c.ID)
	}
	return false
}

func policyMetricValue(rule config.PolicyRule, c config.Container, usage map[string]interface{}) float64 {
	switch rule.Metric {
	case config.PolicyMetricCPU:
		vcpu := c.VCPU
		if vcpu <= 0 {
			vcpu = 1
		}
		return numberFromUsage(usage, "cpu_usage_pct") / vcpu
	case config.PolicyMetricMemory:
		total := numberFromUsage(usage, "memory_total_bytes")
		if total <= 0 {
			total = float64(c.RAMMB) * 1024 * 1024
		}
		if total <= 0 {
			return 0
		}
		return numberFromUsage(usage, "memory_usage_bytes") / total * 100
	case config.PolicyMetricNetworkRX:
		return positiveNumberFromUsage(usage, "network_rx_bps")
	case config.PolicyMetricNetworkTX:
		return positiveNumberFromUsage(usage, "network_tx_bps")
	case config.PolicyMetricDiskIO:
		return positiveNumberFromUsage(usage, "disk_read_bps") + positiveNumberFromUsage(usage, "disk_write_bps")
	default:
		return 0
	}
}

func policyConditionHolds(operator string, value, threshold float64) bool {
	if operator == "lt" {
		return value < threshold
	}
	return value > threshold
}

// triggerPolicyAction applies a policy action to the LIVE container config so
// the change is both persisted (SaveConfig) and applied to the runtime. The
// caller passes only the identity captured from its read snapshot; mutations
// always go through the config package's locked helpers to avoid modifying a
// stale copy or racing with HTTP handlers.
func triggerPolicyAction(rule config.PolicyRule, containerID int, containerName string, value float64) {
	detail := ""
	switch rule.Action {
	case config.PolicyActionRaiseCPU:
		old := 0.0
		saved, live := config.MutateContainerByID(containerID, func(l *config.Container) {
			old = l.VCPU
			l.VCPU += rule.AdjustVCPU
		})
		if saved && live != nil {
			_ = applyLimitsByRuntime(live)
		}
		detail = fmt.Sprintf("%s vcpu %.1f -> %.1f", containerName, old, old+rule.AdjustVCPU)
	case config.PolicyActionRaiseRAM:
		old := 0
		saved, live := config.MutateContainerByID(containerID, func(l *config.Container) {
			old = l.RAMMB
			l.RAMMB += rule.AdjustRAMMB
		})
		if saved && live != nil {
			_ = applyLimitsByRuntime(live)
		}
		detail = fmt.Sprintf("%s ram %dMB -> %dMB", containerName, old, old+rule.AdjustRAMMB)
	case config.PolicyActionAdjustBW:
		oldDown, oldUp := 0, 0
		saved, live := config.MutateContainerByID(containerID, func(l *config.Container) {
			oldDown, oldUp = l.NetworkDownMbps, l.NetworkUpMbps
			l.NetworkDownMbps = rule.AdjustBWMbps
			l.NetworkUpMbps = rule.AdjustBWMbps
		})
		if saved && live != nil {
			_ = applyLimitsByRuntime(live)
		}
		detail = fmt.Sprintf("%s bw %d/%dMbps -> %d/%dMbps", containerName, oldDown, oldUp, rule.AdjustBWMbps, rule.AdjustBWMbps)
	case config.PolicyActionShutdown:
		if err := stopByRuntime(containerID); err == nil {
			config.UpdateContainerStatus(containerID, "stopped")
		}
		detail = fmt.Sprintf("%s 已按策略关机", containerName)
	case config.PolicyActionNotify:
		// 仅告警：不修改任何资源，只推送外部通知并记录（安全无副作用）。
		pushPolicyNotification(rule, containerName, value)
		detail = fmt.Sprintf("%s 仅告警", containerName)
	default:
		return
	}

	rule.TriggeredCount++
	rule.LastTriggered = time.Now().Format("2006-01-02 15:04:05")
	config.UpdatePolicyRuleMeta(rule.ID, rule.TriggeredCount, rule.LastTriggered)
	config.AppendPolicyHistory(config.PolicyTriggerRecord{
		RuleID:    rule.ID,
		RuleName:  rule.Name,
		Container: containerName,
		Metric:    rule.Metric,
		Value:     value,
		Action:    rule.Action,
		Detail:    detail,
	})
	_ = config.SavePolicyRules()
	config.AddAuditLog("policy_trigger", rule.ID, rule.Name+" -> "+detail, "system")
}
