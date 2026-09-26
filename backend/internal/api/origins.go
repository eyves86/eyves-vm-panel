package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

type webSSHOriginSettingsRequest struct {
	Origins              []string `json:"origins"`
	WebSSHAllowedOrigins []string `json:"webssh_allowed_origins"`
}

type webSSHOriginSettingsResponse struct {
	Origins       []string `json:"origins"`
	CurrentOrigin string   `json:"current_origin,omitempty"`
}

func HandleWebSSHOriginSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: webSSHOriginSettingsStatus(r)})
	case http.MethodPut:
		updateWebSSHOriginSettings(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func updateWebSSHOriginSettings(w http.ResponseWriter, r *http.Request) {
	var req webSSHOriginSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	origins := req.Origins
	if len(origins) == 0 && len(req.WebSSHAllowedOrigins) > 0 {
		origins = req.WebSSHAllowedOrigins
	}
	normalized, err := config.NormalizeAllowedOrigins(origins)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.WebSSHAllowedOrigins = normalized
	})
	auditRequest(r, "settings.webssh_origins", "WebSSH Origin", "origins="+strings.Join(normalized, ","), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Origin allowlist saved", Data: webSSHOriginSettingsStatus(r)})
}

func webSSHOriginSettingsStatus(r *http.Request) webSSHOriginSettingsResponse {
	config.AppConfigMu.RLock()
	origins := append([]string(nil), config.AppConfig.WebSSHAllowedOrigins...)
	config.AppConfigMu.RUnlock()
	if origins == nil {
		origins = []string{}
	}
	return webSSHOriginSettingsResponse{
		Origins:       origins,
		CurrentOrigin: requestOrigin(r),
	}
}

func requestOrigin(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.ToLower(strings.Split(forwarded, ",")[0])
	}
	return scheme + "://" + host
}
