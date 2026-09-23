package config

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// PolicyRule is a conditional auto-adjustment rule. When a running container's
// metric crosses the threshold, the rule triggers an action (raise vcpu/ram/bw
// or shutdown) after a cooldown period to avoid flapping.
type PolicyRule struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Enabled        bool    `json:"enabled"`
	Metric         string  `json:"metric"`    // cpu | memory | network_rx | network_tx | disk_io
	Operator       string  `json:"operator"`  // gt | lt
	Threshold      float64 `json:"threshold"` // metric value, e.g. cpu 90 (%), network 1048576 (bps)
	Action         string  `json:"action"`    // raise_cpu | raise_ram | adjust_bw | shutdown
	AdjustVCPU     float64 `json:"adjust_vcpu,omitempty"`
	AdjustRAMMB    int     `json:"adjust_ram_mb,omitempty"`
	AdjustBWMbps   int     `json:"adjust_bw_mbps,omitempty"`
	CooldownMin    int     `json:"cooldown_minutes,omitempty"` // 0 = default 10
	TargetScope    string  `json:"target_scope"`               // all | tenant:<name> | container:<name>
	CreatedAt      string  `json:"created_at"`
	LastTriggered  string  `json:"last_triggered,omitempty"`
	TriggeredCount int     `json:"triggered_count,omitempty"`
}

// PolicyTriggerRecord is an audit trail entry for policy-triggered actions.
type PolicyTriggerRecord struct {
	Time      string  `json:"time"`
	RuleID    string  `json:"rule_id"`
	RuleName  string  `json:"rule_name"`
	Container string  `json:"container"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Action    string  `json:"action"`
	Detail    string  `json:"detail"`
}

const (
	PolicyMetricCPU       = "cpu"
	PolicyMetricMemory    = "memory"
	PolicyMetricNetworkRX = "network_rx"
	PolicyMetricNetworkTX = "network_tx"
	PolicyMetricDiskIO    = "disk_io"

	PolicyActionRaiseCPU = "raise_cpu"
	PolicyActionRaiseRAM = "raise_ram"
	PolicyActionAdjustBW = "adjust_bw"
	PolicyActionShutdown = "shutdown"
	PolicyActionNotify   = "notify"

	PolicyHistoryLimit = 500
)

// DefaultPolicyCooldownMinutes is used when a rule does not specify a cooldown.
const DefaultPolicyCooldownMinutes = 10

// SavePolicyRules persists the policy rules and trigger history via app_meta.
func SavePolicyRules() error {
	// Snapshot the in-memory slices under the config lock before serializing
	// so concurrent writers cannot tear the data while it is marshaled.
	AppConfigMu.RLock()
	rulesJSON, _ := json.Marshal(AppConfig.PolicyRules)
	historyJSON, _ := json.Marshal(AppConfig.PolicyHistory)
	AppConfigMu.RUnlock()
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return errors.New("database not initialized")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO app_meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"policy_rules", string(rulesJSON)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO app_meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"policy_history", string(historyJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// loadPolicyState reads policy rules and trigger history from app_meta.
func loadPolicyState(cfg *EyvescloudConfig, meta map[string]string) {
	if raw := strings.TrimSpace(meta["policy_rules"]); raw != "" {
		var rules []PolicyRule
		if err := json.Unmarshal([]byte(raw), &rules); err == nil {
			cfg.PolicyRules = rules
		}
	}
	if raw := strings.TrimSpace(meta["policy_history"]); raw != "" {
		var history []PolicyTriggerRecord
		if err := json.Unmarshal([]byte(raw), &history); err == nil {
			cfg.PolicyHistory = history
		}
	}
	if cfg.PolicyRules == nil {
		cfg.PolicyRules = []PolicyRule{}
	}
	if cfg.PolicyHistory == nil {
		cfg.PolicyHistory = []PolicyTriggerRecord{}
	}
}

// AppendPolicyHistory records a trigger event and trims the history to the limit.
func AppendPolicyHistory(record PolicyTriggerRecord) {
	if AppConfig == nil {
		return
	}
	AppConfigMu.Lock()
	defer AppConfigMu.Unlock()
	if record.Time == "" {
		record.Time = time.Now().Format("2006-01-02 15:04:05")
	}
	AppConfig.PolicyHistory = append(AppConfig.PolicyHistory, record)
	if len(AppConfig.PolicyHistory) > PolicyHistoryLimit {
		AppConfig.PolicyHistory = AppConfig.PolicyHistory[len(AppConfig.PolicyHistory)-PolicyHistoryLimit:]
	}
	// Keep newest first for the API response.
	sort.SliceStable(AppConfig.PolicyHistory, func(i, j int) bool {
		return AppConfig.PolicyHistory[i].Time > AppConfig.PolicyHistory[j].Time
	})
}
