package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

type panelAccessPolicyResponse struct {
	Enabled        bool     `json:"enabled"`
	AllowedSources []string `json:"allowed_sources"`
	TrustedProxies []string `json:"trusted_proxies"`
	CurrentSource  string   `json:"current_source"`
	DirectSource   string   `json:"direct_source"`
	UsingForwarded bool     `json:"using_forwarded"`
	Warnings       []string `json:"warnings,omitempty"`
}

func HandlePanelAccessPolicy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: panelAccessPolicyStatus(r, config.SnapshotPanelAccessPolicy())})
	case http.MethodPut:
		updatePanelAccessPolicy(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func updatePanelAccessPolicy(w http.ResponseWriter, r *http.Request) {
	var requested config.PanelAccessPolicy
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	normalized, err := config.NormalizePanelAccessPolicy(requested)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	decision := evaluatePanelRequest(r, normalized)
	if normalized.Enabled && !decision.Allowed {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "The new access policy does not allow your current source address " + decision.CurrentSource,
		})
		return
	}

	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		cfg.PanelAccessPolicy = normalized
	})
	detail := "enabled=" + strings.ToLower(strings.TrimSpace(boolText(normalized.Enabled))) +
		",allowed=" + strings.Join(normalized.AllowedSources, ",") +
		",trusted_proxies=" + strings.Join(normalized.TrustedProxies, ",")
	auditRequest(r, "settings.panel_access", "Panel access policy", detail, true, "")
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Panel access policy saved",
		Data:    panelAccessPolicyStatus(r, normalized),
	})
}

func panelAccessPolicyStatus(r *http.Request, policy config.PanelAccessPolicy) panelAccessPolicyResponse {
	decision := evaluatePanelRequest(r, policy)
	resp := panelAccessPolicyResponse{
		Enabled:        policy.Enabled,
		AllowedSources: append([]string(nil), policy.AllowedSources...),
		TrustedProxies: append([]string(nil), policy.TrustedProxies...),
		CurrentSource:  decision.CurrentSource,
		DirectSource:   decision.DirectSource,
		UsingForwarded: decision.UsedForwarded,
	}
	// 如果管理员显式配置了 TrustedProxies 但没启用访问控制，意味着 IP 解析依赖代理头，
	// 但又允许任何人直接连——这在架构上是自相矛盾的，提示管理员注意。
	if len(policy.TrustedProxies) > 0 && !policy.Enabled {
		resp.Warnings = append(resp.Warnings, "TrustedProxies configured but panel access control is disabled; X-Forwarded-For will still be honoured for client IP resolution, but every source address is allowed")
	}
	return resp
}

func evaluatePanelRequest(r *http.Request, policy config.PanelAccessPolicy) config.PanelAccessDecision {
	return config.EvaluatePanelAccess(policy, r.RemoteAddr, config.ForwardedClientHeaders{
		ForwardedFor:   r.Header.Get("X-Forwarded-For"),
		RealIP:         r.Header.Get("X-Real-IP"),
		CFConnectingIP: r.Header.Get("CF-Connecting-IP"),
	})
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
