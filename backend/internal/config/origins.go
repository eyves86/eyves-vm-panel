package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NormalizeAllowedOrigin accepts a browser Origin value such as
// https://www.example.com and returns a canonical form for exact matching.
func NormalizeAllowedOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("Origin must include scheme and host: %s", value)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("Origin scheme must be http or https: %s", value)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Origin must not include path, query, or fragment: %s", value)
	}
	host := normalizeOriginHostPort(u.Host, scheme)
	if host == "" {
		return "", fmt.Errorf("Origin host is required: %s", value)
	}
	return scheme + "://" + host, nil
}

func NormalizeAllowedOrigins(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		origin, err := NormalizeAllowedOrigin(value)
		if err != nil {
			return nil, err
		}
		if origin == "" || seen[origin] {
			continue
		}
		seen[origin] = true
		result = append(result, origin)
	}
	return result, nil
}

func IsOriginAllowed(origin string, requestHost string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	if isSameRequestOrigin(origin, requestHost) {
		return true
	}
	normalized, err := NormalizeAllowedOrigin(origin)
	if err != nil {
		return false
	}
	if AppConfig == nil {
		return false
	}
	// Snapshot the allowlist under the read lock; the setting handler can
	// rewrite the slice concurrently on every request path (CORS, WebSocket).
	AppConfigMu.RLock()
	allowedOrigins := append([]string(nil), AppConfig.WebSSHAllowedOrigins...)
	AppConfigMu.RUnlock()
	for _, allowed := range allowedOrigins {
		allowed, err := NormalizeAllowedOrigin(allowed)
		if err == nil && normalized == allowed {
			return true
		}
	}
	return false
}

func isSameRequestOrigin(origin string, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	originHost := normalizeHostOnly(u.Hostname())
	host := normalizeHostOnly(requestHost)
	if originHost == "" || host == "" {
		return false
	}
	if originHost == host {
		return true
	}
	return isLoopbackHost(originHost) && isLoopbackHost(host)
}

func normalizeOriginHostPort(raw string, scheme string) string {
	host := raw
	port := ""
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host = h
		port = p
	}
	host = normalizeHostOnly(host)
	if host == "" {
		return ""
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") && net.ParseIP(host) != nil {
		return "[" + host + "]"
	}
	return host
}

func normalizeHostOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	raw = strings.Trim(raw, "[]")
	if ip := net.ParseIP(raw); ip != nil {
		return strings.ToLower(ip.String())
	}
	return strings.TrimSuffix(strings.ToLower(raw), ".")
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
