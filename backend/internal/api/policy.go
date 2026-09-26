package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// HandlePolicies lists or creates CPU/bandwidth auto-adjustment policies.
// GET  /api/v1/policies  -> { rules: [...], history: [...] }
// POST /api/v1/policies  -> create a rule
func HandlePolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "policy:read") {
			return
		}
		config.AppConfigMu.RLock()
		rules := append([]config.PolicyRule(nil), config.AppConfig.PolicyRules...)
		history := append([]config.PolicyTriggerRecord(nil), config.AppConfig.PolicyHistory...)
		config.AppConfigMu.RUnlock()
		if rules == nil {
			rules = []config.PolicyRule{}
		}
		if history == nil {
			history = []config.PolicyTriggerRecord{}
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"rules":   rules,
			"history": history,
		}})
	case http.MethodPost:
		if !requireScope(w, r, "policy:write") {
			return
		}
		var req config.PolicyRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if err := normalizePolicyRule(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if req.ID == "" {
			req.ID = fmt.Sprintf("pol_%d", time.Now().UnixNano())
		}
		req.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			cfg.PolicyRules = append(cfg.PolicyRules, req)
		})
		if err := config.SavePolicyRules(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.AddAuditLog("policy_create", req.ID, req.Name+" ("+req.Metric+" "+req.Operator+" "+fmt.Sprint(req.Threshold)+")", requestUser(r))
		jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Message: "Policy created", Data: req})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandlePolicyItem updates or deletes a single policy rule.
// PUT    /api/v1/policies/{id}
// DELETE /api/v1/policies/{id}
func HandlePolicyItem(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/v1/policies/")
	path = strings.TrimPrefix(path, "/api/policies/")
	id := strings.Trim(path, "/")
	if id == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Policy not found"})
		return
	}
	// Existence check (snapshot read)
	config.AppConfigMu.RLock()
	exists := false
	for i := range config.AppConfig.PolicyRules {
		if config.AppConfig.PolicyRules[i].ID == id {
			exists = true
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if !exists {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Policy not found"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !requireScope(w, r, "policy:write") {
			return
		}
		var req config.PolicyRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		if err := normalizePolicyRule(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		req.ID = id
		var updated *config.PolicyRule
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.PolicyRules {
				if cfg.PolicyRules[i].ID != id {
					continue
				}
				createdAt := cfg.PolicyRules[i].CreatedAt
				if createdAt == "" {
					createdAt = time.Now().Format("2006-01-02 15:04:05")
				}
				req.CreatedAt = createdAt
				cfg.PolicyRules[i] = req
				copyRule := cfg.PolicyRules[i]
				updated = &copyRule
				return
			}
		})
		if updated == nil {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Policy not found"})
			return
		}
		if err := config.SavePolicyRules(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.AddAuditLog("policy_update", id, req.Name, requestUser(r))
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Policy updated", Data: req})
	case http.MethodDelete:
		if !requireScope(w, r, "policy:write") {
			return
		}
		name := id
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.PolicyRules {
				if cfg.PolicyRules[i].ID != id {
					continue
				}
				name = cfg.PolicyRules[i].Name
				cfg.PolicyRules = append(cfg.PolicyRules[:i], cfg.PolicyRules[i+1:]...)
				return
			}
		})
		if err := config.SavePolicyRules(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.AddAuditLog("policy_delete", id, name, requestUser(r))
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Policy deleted"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func normalizePolicyRule(req *config.PolicyRule) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	switch req.Metric {
	case config.PolicyMetricCPU, config.PolicyMetricMemory, config.PolicyMetricNetworkRX, config.PolicyMetricNetworkTX, config.PolicyMetricDiskIO:
	default:
		return fmt.Errorf("invalid metric: %s", req.Metric)
	}
	if req.Operator != "gt" && req.Operator != "lt" {
		return fmt.Errorf("operator must be gt or lt")
	}
	if req.Threshold <= 0 {
		return fmt.Errorf("threshold must be positive")
	}
	switch req.Action {
	case config.PolicyActionRaiseCPU:
		if req.AdjustVCPU <= 0 {
			return fmt.Errorf("adjust_vcpu is required for raise_cpu action")
		}
	case config.PolicyActionRaiseRAM:
		if req.AdjustRAMMB <= 0 {
			return fmt.Errorf("adjust_ram_mb is required for raise_ram action")
		}
	case config.PolicyActionAdjustBW:
		if req.AdjustBWMbps <= 0 {
			return fmt.Errorf("adjust_bw_mbps is required for adjust_bw action")
		}
	case config.PolicyActionShutdown:
	case config.PolicyActionNotify:
	default:
		return fmt.Errorf("invalid action: %s", req.Action)
	}
	scope := strings.TrimSpace(req.TargetScope)
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && !strings.HasPrefix(scope, "tenant:") && !strings.HasPrefix(scope, "container:") {
		return fmt.Errorf("target_scope must be all, tenant:<name> or container:<name>")
	}
	req.TargetScope = scope
	if req.CooldownMin < 0 {
		req.CooldownMin = 0
	}
	return nil
}
