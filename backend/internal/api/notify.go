package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

// notifyMu guards lastNotify (per-alert push dedupe map).
var notifyMu sync.Mutex
var lastNotify = map[string]time.Time{}

// validateWebhookURL restricts webhook endpoints to http/https URLs without
// embedded credentials, preventing SSRF via non-HTTP schemes and credential
// leakage in the URL.
func validateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https scheme")
	}
	if u.Host == "" {
		return fmt.Errorf("webhook URL must include a host")
	}
	if u.User != nil {
		return fmt.Errorf("webhook URL must not contain embedded credentials")
	}
	return nil
}

// notificationEnabled reports whether at least one push channel is configured.
func notificationEnabled() bool {
	cfg := config.AppConfig.Notifications
	return cfg.SecurityAlertsEnabled && (cfg.WebhookURL != "" || cfg.SMTPEnabled)
}

// pushPolicyNotification 推送策略“仅告警”类通知（webhook + smtp），不做任何资源变更。
func pushPolicyNotification(rule config.PolicyRule, containerName string, value float64) {
	if !notificationEnabled() {
		return
	}
	alert := SecurityAlert{
		ID:            "policy-" + rule.ID,
		ContainerName: containerName,
		Type:          rule.Metric,
		Severity:      "high",
		SourceIP:      "",
		TargetIP:      "",
		TargetPort:    0,
		Detail:        fmt.Sprintf("策略触发: %s (%s %s %.2f)", rule.Name, rule.Metric, rule.Operator, value),
		Timestamp:     time.Now().Format("2006-01-02 15:04:05"),
	}
	go func() {
		cfg := config.AppConfig.Notifications
		if cfg.WebhookURL != "" {
			_ = sendWebhookNotification(cfg.WebhookURL, alert)
		}
		if cfg.SMTPEnabled {
			_ = sendSMTPNotification(cfg, alert)
		}
	}()
}

// notifySeverityOK reports whether the alert severity meets the configured threshold.
func notifySeverityOK(alert SecurityAlert) bool {
	min := strings.ToLower(strings.TrimSpace(config.AppConfig.Notifications.MinSeverity))
	if min == "" {
		min = "medium"
	}
	return severityRank(alert.Severity) >= severityRank(min)
}

// NotifySecurityAlert pushes a newly-created security alert to external channels.
// Dedupes to at most one push per alert signature per 5 minutes to avoid flooding.
func NotifySecurityAlert(alert SecurityAlert) {
	if !notificationEnabled() || !notifySeverityOK(alert) {
		return
	}

	key := alert.ContainerName + "|" + alert.Type + "|" + alert.TargetIP + "|" + fmt.Sprint(alert.TargetPort)
	now := time.Now()
	notifyMu.Lock()
	last, ok := lastNotify[key]
	if ok && now.Sub(last) < 5*time.Minute {
		notifyMu.Unlock()
		return
	}
	lastNotify[key] = now
	for k, t := range lastNotify {
		if now.Sub(t) > time.Hour {
			delete(lastNotify, k)
		}
	}
	notifyMu.Unlock()

	go func() {
		cfg := config.AppConfig.Notifications
		if cfg.WebhookURL != "" {
			_ = sendWebhookNotification(cfg.WebhookURL, alert)
		}
		if cfg.SMTPEnabled {
			_ = sendSMTPNotification(cfg, alert)
		}
	}()
}

type webhookPayload struct {
	Event         string `json:"event"`
	AlertID       string `json:"alert_id"`
	ContainerName string `json:"container_name"`
	Type          string `json:"type"`
	Severity      string `json:"severity"`
	SourceIP      string `json:"source_ip"`
	TargetIP      string `json:"target_ip"`
	TargetPort    int    `json:"target_port"`
	Detail        string `json:"detail"`
	Timestamp     string `json:"timestamp"`
}

func sendWebhookNotification(url string, alert SecurityAlert) error {
	if err := validateWebhookURL(url); err != nil {
		return err
	}
	payload := webhookPayload{
		Event:         "security_alert",
		AlertID:       alert.ID,
		ContainerName: alert.ContainerName,
		Type:          alert.Type,
		Severity:      alert.Severity,
		SourceIP:      alert.SourceIP,
		TargetIP:      alert.TargetIP,
		TargetPort:    alert.TargetPort,
		Detail:        alert.Detail,
		Timestamp:     alert.Timestamp,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EyvesCloud/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func sendSMTPNotification(cfg config.NotificationConfig, alert SecurityAlert) error {
	if cfg.SMTPServer == "" || cfg.SMTPTo == "" {
		return fmt.Errorf("smtp server/recipient not configured")
	}
	from := cfg.SMTPFrom
	if from == "" {
		from = cfg.SMTPUser
	}
	port := cfg.SMTPPort
	if port == 0 {
		port = 25
	}
	addr := fmt.Sprintf("%s:%d", cfg.SMTPServer, port)

	subject := fmt.Sprintf("[%s] 安全告警: %s - %s", strings.ToUpper(alert.Severity), alert.ContainerName, alert.Type)
	body := fmt.Sprintf(
		"EyvesCloud 安全告警\n\n容器: %s\n类型: %s\n级别: %s\n来源 IP: %s\n目标 IP: %s\n端口: %d\n详情: %s\n时间: %s\n",
		alert.ContainerName, alert.Type, alert.Severity, alert.SourceIP, alert.TargetIP, alert.TargetPort, alert.Detail, alert.Timestamp)
	msg := "From: " + from + "\r\n" +
		"To: " + cfg.SMTPTo + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + body

	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPServer)
	}

	if port == 465 {
		return sendSMTPImplicitTLS(cfg.SMTPServer, addr, auth, from, splitEmails(cfg.SMTPTo), []byte(msg))
	}
	return smtp.SendMail(addr, auth, from, splitEmails(cfg.SMTPTo), []byte(msg))
}

// sendSMTPImplicitTLS sends mail over an implicit TLS connection (port 465).
func sendSMTPImplicitTLS(server, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: server})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, server)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err := client.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func splitEmails(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// HandleNotificationSettings returns or updates external notification settings.
func HandleNotificationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "security:read") {
			return
		}
		cfg := config.AppConfig.Notifications
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"security_alerts_enabled": cfg.SecurityAlertsEnabled,
			"min_severity":            cfg.MinSeverity,
			"webhook_url":             cfg.WebhookURL,
			"smtp_enabled":            cfg.SMTPEnabled,
			"smtp_server":             cfg.SMTPServer,
			"smtp_port":               cfg.SMTPPort,
			"smtp_user":               cfg.SMTPUser,
			"smtp_from":               cfg.SMTPFrom,
			"smtp_to":                 cfg.SMTPTo,
			"smtp_password_set":       cfg.SMTPPassword != "",
		}})
	case http.MethodPut:
		if !requireScope(w, r, "security:settings") {
			return
		}
		var req config.NotificationConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		// 密码为空表示未修改，保留原值
		if req.SMTPPassword == "" {
			req.SMTPPassword = config.AppConfig.Notifications.SMTPPassword
		}
		if req.MinSeverity == "" {
			req.MinSeverity = "medium"
		}
		if err := validateWebhookURL(req.WebhookURL); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		// 通过 MutateGlobal 在写锁下更新，避免与后台安全告警协程无锁读产生数据竞争。
		if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			cfg.Notifications = req
		}); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "notifications.settings", "notifications",
			fmt.Sprintf("webhook=%v smtp=%v", req.WebhookURL != "", req.SMTPEnabled), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{"saved": true}})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleNotificationTest sends a test notification through configured channels.
func HandleNotificationTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:settings") {
		return
	}
	cfg := config.AppConfig.Notifications
	if cfg.WebhookURL == "" && !cfg.SMTPEnabled {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "No notification channel configured"})
		return
	}
	alert := SecurityAlert{
		ID:            "test-notification",
		ContainerName: "test",
		Type:          "test",
		Severity:      "high",
		SourceIP:      "0.0.0.0",
		TargetIP:      "*",
		Detail:        "这是一条测试告警，用于验证外部通知通道",
		Timestamp:     time.Now().Format("2006-01-02 15:04:05"),
	}
	errs := []string{}
	if cfg.WebhookURL != "" {
		if err := sendWebhookNotification(cfg.WebhookURL, alert); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	if cfg.SMTPEnabled {
		if err := sendSMTPNotification(cfg, alert); err != nil {
			errs = append(errs, "smtp: "+err.Error())
		}
	}
	if len(errs) > 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: strings.Join(errs, "; ")})
		return
	}
	auditRequest(r, "notifications.test", "notifications", "发送测试告警", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Test notification sent"})
}
