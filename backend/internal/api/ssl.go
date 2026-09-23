package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

type sslSettingsRequest struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Target   string `json:"target"`
	Email    string `json:"email"`
	CertPEM  string `json:"cert_pem"`
	KeyPEM   string `json:"key_pem"`
	ApplyNow bool   `json:"apply_now"`
}

type sslCertificateInfo struct {
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	DNSNames  []string `json:"dns_names"`
	IPNames   []string `json:"ip_names"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	Valid     bool     `json:"valid"`
}

type sslSavedCertificateStatus struct {
	config.SSLConfig
	Certificate *sslCertificateInfo `json:"certificate,omitempty"`
}

type sslSettingsResponse struct {
	config.SSLConfig
	DetectedHost     string                               `json:"detected_host"`
	Certificate      *sslCertificateInfo                  `json:"certificate,omitempty"`
	ModeCertificates map[string]sslSavedCertificateStatus `json:"mode_certificates"`
	NeedsRestart     bool                                 `json:"needs_restart,omitempty"`
}

func HandleSSLSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: sslSettingsStatus(r, false)})
	case http.MethodPut:
		updateSSLSettings(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func updateSSLSettings(w http.ResponseWriter, r *http.Request) {
	var req sslSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	mode := config.NormalizeSSLMode(req.Mode)
	if !req.Enabled || mode == config.SSLModeDisabled {
		saveCurrentSSLSlot()
		config.AppConfig.SSL = config.SSLConfig{Enabled: false, Mode: config.SSLModeDisabled}
		if err := config.SaveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Save SSL settings failed"})
			return
		}
		restartIfRequested(req.ApplyNow)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "SSL disabled", Data: sslSettingsStatus(r, true)})
		return
	}

	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = detectedRequestHost(r)
	}
	normalizedTarget, err := config.NormalizeSSLCertificateTarget(target)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	target = normalizedTarget

	next, err := resolveSSLModeCertificate(mode, target, strings.TrimSpace(req.Email), req.CertPEM, req.KeyPEM)
	if err != nil {
		_ = config.SaveConfig()
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error(), Data: sslSettingsStatus(r, false)})
		return
	}

	if err := validateCertificatePair(next.CertPath, next.KeyPath); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	next.LastIssuedAt = time.Now().Format(time.RFC3339)
	next.Enabled = true
	config.AppConfig.SSL = next
	saveSSLSlot(next)
	if err := config.SaveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Save SSL settings failed"})
		return
	}

	restartIfRequested(req.ApplyNow)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "SSL settings saved", Data: sslSettingsStatus(r, true)})
}

func sslSettingsStatus(r *http.Request, needsRestart bool) sslSettingsResponse {
	cfg := config.AppConfig.SSL
	cfg.KeyPath = maskExistingPath(cfg.KeyPath)
	resp := sslSettingsResponse{
		SSLConfig:        cfg,
		DetectedHost:     detectedRequestHost(r),
		ModeCertificates: sslModeCertificatesStatus(),
		NeedsRestart:     needsRestart,
	}
	if cert, err := readCertificateInfo(config.AppConfig.SSL.CertPath); err == nil {
		resp.Certificate = cert
	}
	return resp
}

func resolveSSLModeCertificate(mode, target, email, certPEM, keyPEM string) (config.SSLConfig, error) {
	if config.AppConfig.SSLCertificates == nil {
		config.AppConfig.SSLCertificates = map[string]config.SSLConfig{}
	}
	next := config.AppConfig.SSLCertificates[mode]
	next.Mode = mode
	next.Target = target
	if email != "" || next.Email == "" {
		next.Email = email
	}

	var err error
	switch mode {
	case config.SSLModeUploaded:
		if strings.TrimSpace(certPEM) != "" || strings.TrimSpace(keyPEM) != "" {
			next.CertPath, next.KeyPath, err = saveUploadedCertificate(certPEM, keyPEM)
		} else if next.CertPath == "" || next.KeyPath == "" {
			err = fmt.Errorf("certificate and private key are required")
		} else if !certificateUsable(next.CertPath, next.KeyPath, target) {
			err = fmt.Errorf("uploaded certificate is expired, invalid, or does not match the target")
		}
	case config.SSLModeSelfSigned:
		if !certificateUsable(next.CertPath, next.KeyPath, target) {
			next.CertPath, next.KeyPath, err = generateSelfSignedCertificate(target)
		}
	case config.SSLModeLetsEncrypt:
		if !certificateUsable(next.CertPath, next.KeyPath, target) {
			next.CertPath, next.KeyPath, err = requestLetsEncryptCertificate(target, next.Email)
		}
	default:
		err = fmt.Errorf("unsupported SSL mode")
	}
	if err != nil {
		next.LastError = err.Error()
		saveSSLSlot(next)
		return next, err
	}
	next.LastError = ""
	return next, nil
}

func sslModeCertificatesStatus() map[string]sslSavedCertificateStatus {
	result := map[string]sslSavedCertificateStatus{}
	for _, mode := range []string{config.SSLModeLetsEncrypt, config.SSLModeSelfSigned, config.SSLModeUploaded} {
		cfg := config.AppConfig.SSLCertificates[mode]
		cfg.KeyPath = maskExistingPath(cfg.KeyPath)
		status := sslSavedCertificateStatus{SSLConfig: cfg}
		if cert, err := readCertificateInfo(config.AppConfig.SSLCertificates[mode].CertPath); err == nil {
			status.Certificate = cert
		}
		result[mode] = status
	}
	return result
}

func saveCurrentSSLSlot() {
	if config.AppConfig.SSL.Mode == config.SSLModeDisabled || config.AppConfig.SSL.CertPath == "" {
		return
	}
	saveSSLSlot(config.AppConfig.SSL)
}

func saveSSLSlot(ssl config.SSLConfig) {
	mode := config.NormalizeSSLMode(ssl.Mode)
	if mode == config.SSLModeDisabled {
		return
	}
	if config.AppConfig.SSLCertificates == nil {
		config.AppConfig.SSLCertificates = map[string]config.SSLConfig{}
	}
	ssl.Mode = mode
	ssl.Enabled = false
	config.AppConfig.SSLCertificates[mode] = ssl
}

func saveUploadedCertificate(certPEM, keyPEM string) (string, string, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" || keyPEM == "" {
		return "", "", fmt.Errorf("certificate and private key are required")
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return "", "", fmt.Errorf("certificate/private key mismatch: %v", err)
	}
	certPath, keyPath, err := config.UploadedSSLPaths()
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, []byte(certPEM+"\n"), 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM+"\n"), 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func generateSelfSignedCertificate(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	normalizedTarget, err := config.NormalizeSSLCertificateTarget(target)
	if err != nil {
		return "", "", err
	}
	target = normalizedTarget
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: target,
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(target); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{target}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPath, keyPath, err := config.SelfSignedSSLPaths()
	if err != nil {
		return "", "", err
	}
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certOut, 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyOut, 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func requestLetsEncryptCertificate(target, email string) (string, string, error) {
	if _, err := exec.LookPath("certbot"); err != nil {
		return "", "", fmt.Errorf("certbot is not installed on this server")
	}
	target = strings.TrimSpace(target)
	normalizedTarget, err := config.NormalizeSSLCertificateTarget(target)
	if err != nil {
		return "", "", err
	}
	target = normalizedTarget
	args := []string{"certonly", "--non-interactive", "--agree-tos", "--standalone"}
	if email != "" {
		args = append(args, "--email", email)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	if net.ParseIP(target) != nil {
		if err := ensureCertbotSupportsIPCertificates(); err != nil {
			return "", "", err
		}
		args = append(args, "--preferred-profile", "shortlived", "--ip-address", target)
	} else {
		args = append(args, "-d", target)
	}
	cmd := exec.Command("certbot", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("Let's Encrypt request failed: %s", strings.TrimSpace(string(output)))
	}
	certPath, keyPath, err := config.LetsEncryptSSLPaths(target)
	if err != nil {
		return "", "", err
	}
	if _, err := config.ReadableFileStat(certPath); err != nil {
		return "", "", fmt.Errorf("Let's Encrypt certificate file not found after issuance: %s", certPath)
	}
	if _, err := config.ReadableFileStat(keyPath); err != nil {
		return "", "", fmt.Errorf("Let's Encrypt private key file not found after issuance: %s", keyPath)
	}
	return certPath, keyPath, nil
}

func ensureCertbotSupportsIPCertificates() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "certbot", "--help", "all")
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("certbot check timed out")
	}
	if err != nil {
		return fmt.Errorf("certbot capability check failed: %s", strings.TrimSpace(string(output)))
	}
	help := string(output)
	if !strings.Contains(help, "--ip-address") || !strings.Contains(help, "--preferred-profile") {
		return fmt.Errorf("current certbot does not support IP certificates; install Certbot 5.4+ from snap or another current source")
	}
	return nil
}

func validateCertificatePair(certPath, keyPath string) error {
	safeCertPath, err := config.ResolveSSLPath(certPath)
	if err != nil {
		return err
	}
	safeKeyPath, err := config.ResolveSSLPath(keyPath)
	if err != nil {
		return err
	}
	certPEM, err := os.ReadFile(safeCertPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(safeKeyPath)
	if err != nil {
		return err
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("certificate/private key mismatch: %v", err)
	}
	return nil
}

func certificateUsable(certPath, keyPath, target string) bool {
	if certPath == "" || keyPath == "" {
		return false
	}
	if err := validateCertificatePair(certPath, keyPath); err != nil {
		return false
	}
	cert, err := readLeafCertificate(certPath)
	if err != nil {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return false
	}
	return certificateMatchesTarget(cert, target)
}

func certificateNeedsRenewal(certPath, keyPath, target string, renewBefore time.Duration) bool {
	if certPath == "" || keyPath == "" {
		return true
	}
	if err := validateCertificatePair(certPath, keyPath); err != nil {
		return true
	}
	cert, err := readLeafCertificate(certPath)
	if err != nil {
		return true
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return true
	}
	if !certificateMatchesTarget(cert, target) {
		return true
	}
	return cert.NotAfter.Sub(now) <= renewBefore
}

func certificateMatchesTarget(cert *x509.Certificate, target string) bool {
	target = strings.TrimSpace(strings.Trim(target, "[]"))
	if target == "" {
		return true
	}
	if ip := net.ParseIP(target); ip != nil {
		for _, certIP := range cert.IPAddresses {
			if certIP.Equal(ip) {
				return true
			}
		}
		return false
	}
	if err := cert.VerifyHostname(target); err != nil {
		return false
	}
	return true
}

func readCertificateInfo(certPath string) (*sslCertificateInfo, error) {
	cert, err := readLeafCertificate(certPath)
	if err != nil {
		return nil, err
	}
	ipNames := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ipNames = append(ipNames, ip.String())
	}
	return &sslCertificateInfo{
		Subject:   cert.Subject.String(),
		Issuer:    cert.Issuer.String(),
		DNSNames:  cert.DNSNames,
		IPNames:   ipNames,
		NotBefore: cert.NotBefore.Format(time.RFC3339),
		NotAfter:  cert.NotAfter.Format(time.RFC3339),
		Valid:     time.Now().After(cert.NotBefore) && time.Now().Before(cert.NotAfter),
	}, nil
}

func readLeafCertificate(certPath string) (*x509.Certificate, error) {
	if certPath == "" {
		return nil, errors.New("certificate path is empty")
	}
	safeCertPath, err := config.ResolveSSLPath(certPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(safeCertPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("certificate PEM is invalid")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return cert, nil
}

func detectedRequestHost(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return firstPublicInterfaceIP()
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		if ip := firstPublicInterfaceIP(); ip != "" {
			return ip
		}
	}
	return host
}

func firstPublicInterfaceIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip.String()
	}
	return ""
}

func maskExistingPath(path string) string {
	if path == "" {
		return ""
	}
	return path
}

func restartIfRequested(applyNow bool) {
	if !applyNow {
		return
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("systemctl", "restart", "eyvescloud").Start()
	}()
}

func StartSSLRenewalMonitor() {
	go func() {
		time.Sleep(30 * time.Second)
		renewSavedSSLCertificates()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			renewSavedSSLCertificates()
		}
	}()
}

func renewSavedSSLCertificates() {
	if config.AppConfig == nil || len(config.AppConfig.SSLCertificates) == 0 {
		return
	}
	changed := false
	for mode, cert := range config.AppConfig.SSLCertificates {
		mode = config.NormalizeSSLMode(mode)
		if cert.Target == "" || mode == config.SSLModeDisabled || mode == config.SSLModeUploaded {
			continue
		}

		var certPath, keyPath string
		var err error
		switch mode {
		case config.SSLModeLetsEncrypt:
			if !certificateNeedsRenewal(cert.CertPath, cert.KeyPath, cert.Target, 48*time.Hour) {
				continue
			}
			certPath, keyPath, err = requestLetsEncryptCertificate(cert.Target, cert.Email)
		case config.SSLModeSelfSigned:
			if !certificateNeedsRenewal(cert.CertPath, cert.KeyPath, cert.Target, 30*24*time.Hour) {
				continue
			}
			certPath, keyPath, err = generateSelfSignedCertificate(cert.Target)
		}
		if err != nil {
			cert.LastError = err.Error()
			config.AppConfig.SSLCertificates[mode] = cert
			changed = true
			continue
		}
		cert.CertPath = certPath
		cert.KeyPath = keyPath
		cert.LastIssuedAt = time.Now().Format(time.RFC3339)
		cert.LastError = ""
		config.AppConfig.SSLCertificates[mode] = cert
		if config.AppConfig.SSL.Enabled && config.AppConfig.SSL.Mode == mode {
			active := cert
			active.Enabled = true
			config.AppConfig.SSL = active
		}
		changed = true
	}
	if changed {
		_ = config.SaveConfig()
	}
}
