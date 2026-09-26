package api

import (
	"encoding/json"
	"net/http"

	"eyvescloud/internal/config"
)

// Handle2FAStatus 返回管理员两步验证状态。
func Handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	config.AppConfigMu.RLock()
	enabled := config.AppConfig.AdminTOTPEnabled
	hasSecret := config.AppConfig.AdminTOTPSecret != ""
	config.AppConfigMu.RUnlock()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{
		"enabled":    enabled,
		"has_secret": hasSecret,
	}})
}

// Handle2FASetup 生成新的 TOTP 密钥（仅当未启用时），返回 otpauth URI 供录入。
func Handle2FASetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	config.AppConfigMu.RLock()
	enabled := config.AppConfig.AdminTOTPEnabled
	config.AppConfigMu.RUnlock()
	if enabled {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "两步验证已启用，请先禁用再重新设置"})
		return
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.AdminTOTPSecret = secret
		cfg.AdminTOTPEnabled = false
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	account := ""
	config.AppConfigMu.RLock()
	account = config.AppConfig.AdminUser
	config.AppConfigMu.RUnlock()
	otpauth := totpSetupURI(secret, account)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"secret":      secret,
		"otpauth_uri": otpauth,
		"qr_data_url": totpQRDataURL(otpauth),
	}})
}

// Handle2FAEnable 用当前动态口令确认后启用两步验证，并生成一次性备份码。
func Handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Code      string `json:"code"`
		BackupCnt int    `json:"backup_codes_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if req.BackupCnt < 1 || req.BackupCnt > 20 {
		req.BackupCnt = 8
	}
	config.AppConfigMu.RLock()
	secret := config.AppConfig.AdminTOTPSecret
	enabled := config.AppConfig.AdminTOTPEnabled
	config.AppConfigMu.RUnlock()
	if enabled {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "两步验证已启用"})
		return
	}
	if !validTOTP(secret, req.Code) {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "验证码错误或已过期"})
		return
	}
	plain := generateBackupCodes(req.BackupCnt)
	hashes := hashBackupCodes(plain)
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.AdminTOTPEnabled = true
		cfg.AdminBackupCodes = hashes
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"backup_codes": plain,
	}})
}

// Handle2FADisable 需提供当前动态口令或一次性备份码。
func Handle2FADisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	config.AppConfigMu.RLock()
	secret := config.AppConfig.AdminTOTPSecret
	enabled := config.AppConfig.AdminTOTPEnabled
	backup := append([]string(nil), config.AppConfig.AdminBackupCodes...)
	config.AppConfigMu.RUnlock()
	if !enabled {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "两步验证未启用"})
		return
	}
	passed, _ := adminVerify2FA(secret, enabled, req.Code, backup)
	if !passed {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "验证码错误"})
		return
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.AdminTOTPEnabled = false
		cfg.AdminTOTPSecret = ""
		cfg.AdminBackupCodes = nil
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "两步验证已关闭"})
}

// Handle2FARegenerateBackupCodes 需提供当前动态口令（或现有备份码），换发一批新备份码。
func Handle2FARegenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Code      string `json:"code"`
		BackupCnt int    `json:"backup_codes_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if req.BackupCnt < 1 || req.BackupCnt > 20 {
		req.BackupCnt = 8
	}
	config.AppConfigMu.RLock()
	secret := config.AppConfig.AdminTOTPSecret
	enabled := config.AppConfig.AdminTOTPEnabled
	backup := append([]string(nil), config.AppConfig.AdminBackupCodes...)
	config.AppConfigMu.RUnlock()
	if !enabled || secret == "" {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "两步验证未启用"})
		return
	}
	passed := validTOTP(secret, req.Code)
	if !passed {
		_, passed = verifyAndConsumeBackupCode(req.Code, backup)
	}
	if !passed {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "验证码错误"})
		return
	}
	plain := generateBackupCodes(req.BackupCnt)
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.AdminBackupCodes = hashBackupCodes(plain)
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"backup_codes": plain,
	}})
}