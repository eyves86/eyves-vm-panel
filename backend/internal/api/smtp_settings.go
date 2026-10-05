package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/notify"
)

// smtpDispatcher 是进程级通知调度器：配置了 SMTP 后由 suspend/unsuspend、
// 到期扫描等路径复用，给容器属主（SubUser.Email）发送邮件。
var smtpDispatcher = notify.NewDispatcher()

// InitSMTPSender 在配置可用后注入 email 渠道 sender（幂等，可热更新）。
// 未启用时 sender 返回配置错误，Dispatch 会走重试后死信，调用方只记审计。
func InitSMTPSender() {
	smtpDispatcher.RegisterSender(&notify.SMTPSender{
		Config: func() notify.SMTPConfig {
			s := config.AppConfig.SMTPSettings
			return notify.SMTPConfig{
				Host:     s.Host,
				Port:     s.Port,
				Username: s.Username,
				Password: s.Password,
				From:     s.From,
				TLSMode:  s.TLSMode,
			}
		},
	})
}

// smtpConfigured 返回 SMTP 是否已启用且基础字段完整。
func smtpConfigured() bool {
	s := config.AppConfig.SMTPSettings
	return s.Enabled && strings.TrimSpace(s.Host) != "" && s.Port > 0 && strings.TrimSpace(s.From) != ""
}

// notifyContainerOwner 给容器属主子用户发送邮件通知（异步，不阻塞请求）。
// 未配置 SMTP / 容器无属主 / 属主无邮箱时静默跳过。
func notifyContainerOwner(containerName, subject, body string, severity notify.Severity) {
	if !smtpConfigured() {
		return
	}
	c := config.FindContainerByName(containerName)
	if c == nil || c.OwnerSubUserID == "" {
		return
	}
	var ownerEmail string
	config.AppConfigMu.RLock()
	for i := range config.AppConfig.SubUsers {
		if config.AppConfig.SubUsers[i].ID == c.OwnerSubUserID {
			ownerEmail = strings.TrimSpace(config.AppConfig.SubUsers[i].Email)
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if ownerEmail == "" {
		return
	}
	to := ownerEmail
	eventType := "container." + strings.ToLower(string(severity))
	go func() {
		failed := smtpDispatcher.Dispatch(notify.Message{
			Subject:   subject,
			Body:      body,
			Severity:  severity,
			EventType: eventType,
			Recipient: to,
		}, eventType)
		for _, err := range failed {
			fmt.Printf("notify: container %s owner email failed: %v\n", containerName, err)
		}
	}()
}

// smtpSettingsResponse 是 SMTP 设置的 API 回显（密码永不回显）。
type smtpSettingsResponse struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	From     string `json:"from"`
	TLSMode  string `json:"tls_mode,omitempty"`
	// HasPassword 表示已存有密码（前端据此显示"已保存"而非明文）。
	HasPassword bool `json:"has_password"`
}

// HandleSMTPSettings GET 读取 / PUT 更新 SMTP 配置（仅管理员）。
func HandleSMTPSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s := config.AppConfig.SMTPSettings
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: smtpSettingsResponse{
			Enabled:     s.Enabled,
			Host:        s.Host,
			Port:        s.Port,
			Username:    s.Username,
			From:        s.From,
			TLSMode:     s.TLSMode,
			HasPassword: s.Password != "",
		}})
	case http.MethodPut:
		var req struct {
			Enabled  bool   `json:"enabled"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			From     string `json:"from"`
			TLSMode  string `json:"tls_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		req.Host = strings.TrimSpace(req.Host)
		req.From = strings.TrimSpace(req.From)
		req.Username = strings.TrimSpace(req.Username)
		if req.TLSMode != "smtps" {
			req.TLSMode = "starttls"
		}
		if req.Enabled || req.Host != "" {
			if req.Host == "" || req.Port <= 0 || req.Port > 65535 {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "host and port (1-65535) are required"})
				return
			}
			if req.From == "" {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "from address is required"})
				return
			}
			if _, err := mail.ParseAddress(req.From); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid from address"})
				return
			}
		}
		if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			cfg.SMTPSettings.Enabled = req.Enabled
			cfg.SMTPSettings.Host = req.Host
			cfg.SMTPSettings.Port = req.Port
			cfg.SMTPSettings.Username = req.Username
			cfg.SMTPSettings.From = req.From
			cfg.SMTPSettings.TLSMode = req.TLSMode
			// 密码：请求里为空则保留已存密码（避免回显-清空循环）
			if req.Password != "" {
				cfg.SMTPSettings.Password = req.Password
			}
		}); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		InitSMTPSender()
		auditRequest(r, "smtp.settings.update", "smtp", "enabled="+fmt.Sprint(req.Enabled), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "SMTP settings updated"})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleSMTPTest 发送测试邮件验证 SMTP 配置（仅管理员）。
func HandleSMTPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "to is required"})
		return
	}
	if _, err := mail.ParseAddress(to); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid to address"})
		return
	}
	if !smtpConfigured() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "SMTP is not enabled or incompletely configured"})
		return
	}
	InitSMTPSender()
	sender := &notify.SMTPSender{
		Config: func() notify.SMTPConfig {
			s := config.AppConfig.SMTPSettings
			return notify.SMTPConfig{Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, From: s.From, TLSMode: s.TLSMode}
		},
	}
	body, err := notify.RenderEmailBody(notify.Message{
		Subject:   "EyvesCloud SMTP test",
		Body:      "This is a test email from your EyvesCloud panel. If you received it, SMTP is working.",
		Severity:  notify.SeverityInfo,
		EventType: "smtp.test",
	})
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := sender.Send(notify.Message{
		Subject:   "EyvesCloud SMTP test",
		Body:      body,
		Severity:  notify.SeverityInfo,
		EventType: "smtp.test",
		Recipient: to,
	}); err != nil {
		auditRequest(r, "smtp.test", to, "send failed", false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "Send failed: " + err.Error()})
		return
	}
	auditRequest(r, "smtp.test", to, "sent", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Test email sent to " + to})
}
