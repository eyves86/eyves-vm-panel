package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const letsEncryptLiveDir = "/etc/letsencrypt/live"

var dnsNamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func SSLStorageDir() string {
	dataDir := ""
	if AppConfig != nil {
		dataDir = AppConfig.DataDir
	}
	if dataDir == "" {
		dataDir = getDataDir()
	}
	return filepath.Join(dataDir, "ssl")
}

func UploadedSSLPaths() (string, string, error) {
	dir, err := safeSSLStorageDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "uploaded-fullchain.pem"), filepath.Join(dir, "uploaded-privkey.pem"), nil
}

func SelfSignedSSLPaths() (string, string, error) {
	dir, err := safeSSLStorageDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "self-signed-fullchain.pem"), filepath.Join(dir, "self-signed-privkey.pem"), nil
}

func LetsEncryptSSLPaths(target string) (string, string, error) {
	name, err := NormalizeSSLCertificateTarget(target)
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(letsEncryptLiveDir, name)
	return filepath.Join(base, "fullchain.pem"), filepath.Join(base, "privkey.pem"), nil
}

func ResolveSSLConfigPaths(ssl SSLConfig) (string, string, error) {
	mode := NormalizeSSLMode(ssl.Mode)
	switch mode {
	case SSLModeUploaded:
		if ssl.CertPath != "" && ssl.KeyPath != "" {
			return ResolveSSLPathPair(ssl.CertPath, ssl.KeyPath)
		}
		return UploadedSSLPaths()
	case SSLModeSelfSigned:
		if ssl.CertPath != "" && ssl.KeyPath != "" {
			return ResolveSSLPathPair(ssl.CertPath, ssl.KeyPath)
		}
		return SelfSignedSSLPaths()
	case SSLModeLetsEncrypt:
		if strings.TrimSpace(ssl.Target) == "" && ssl.CertPath != "" && ssl.KeyPath != "" {
			return ResolveSSLPathPair(ssl.CertPath, ssl.KeyPath)
		}
		return LetsEncryptSSLPaths(ssl.Target)
	default:
		return "", "", fmt.Errorf("SSL is disabled")
	}
}

func ResolveSSLPathPair(certPath, keyPath string) (string, string, error) {
	safeCertPath, err := ResolveSSLPath(certPath)
	if err != nil {
		return "", "", err
	}
	safeKeyPath, err := ResolveSSLPath(keyPath)
	if err != nil {
		return "", "", err
	}
	return safeCertPath, safeKeyPath, nil
}

func ResolveSSLPath(path string) (string, error) {
	cleaned, err := cleanAbsolutePath(path)
	if err != nil {
		return "", err
	}
	if isPathUnder(cleaned, SSLStorageDir()) || isPathUnder(cleaned, letsEncryptLiveDir) || isPathUnder(cleaned, "/etc/letsencrypt/archive") {
		return cleaned, nil
	}
	return "", fmt.Errorf("SSL path is outside allowed certificate directories")
}

func ReadableFileStat(path string) (os.FileInfo, error) {
	safePath, err := ResolveSSLPath(path)
	if err != nil {
		return nil, err
	}
	return os.Stat(safePath)
}

func NormalizeSSLCertificateTarget(target string) (string, error) {
	target = strings.TrimSpace(strings.Trim(target, "[]"))
	if target == "" {
		return "", fmt.Errorf("SSL target is required")
	}
	if strings.Contains(target, "/") || strings.Contains(target, "\\") || strings.Contains(target, "..") {
		return "", fmt.Errorf("SSL target contains invalid path characters")
	}
	if ip := net.ParseIP(target); ip != nil {
		return ip.String(), nil
	}
	if len(target) > 253 || !dnsNamePattern.MatchString(target) {
		return "", fmt.Errorf("SSL target must be a valid IP address or DNS name")
	}
	labels := strings.Split(target, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("SSL target must be a valid IP address or DNS name")
		}
	}
	return strings.ToLower(target), nil
}

func safeSSLStorageDir() (string, error) {
	dir, err := cleanAbsolutePath(SSLStorageDir())
	if err != nil {
		return "", err
	}
	dataDir := ""
	if AppConfig != nil {
		dataDir = AppConfig.DataDir
	}
	if dataDir == "" {
		dataDir = getDataDir()
	}
	if !isPathUnder(dir, dataDir) {
		return "", fmt.Errorf("SSL storage directory is outside the data directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func cleanAbsolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func isPathUnder(path, root string) bool {
	cleanPath, err := cleanAbsolutePath(path)
	if err != nil {
		return false
	}
	cleanRoot, err := cleanAbsolutePath(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
